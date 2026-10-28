package portal

import (
	"context"
	"fmt"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// RequestIface is the object the frontend passes the handle of into
// every interactive method, so the app can abort it.
const RequestIface = "org.freedesktop.impl.portal.Request"

// request is an org.freedesktop.impl.portal.Request exported at a
// method's handle for the length of the call. Close() from the app
// closes Cancelled.
type request struct {
	once      sync.Once
	cancelled chan struct{}
	unexport  func()
}

// mountRequest exports a Request at handle; end removes it again.
func mountRequest(conn *dbus.Conn, handle dbus.ObjectPath) (*request, error) {
	r := &request{cancelled: make(chan struct{})}
	unexport, err := dbusx.Export(conn, handle, dbusx.Interface{Name: RequestIface, Methods: requestObject{r}})
	if err != nil {
		return nil, err
	}
	r.unexport = unexport
	return r, nil
}

// Cancelled is closed once the app calls Close().
func (r *request) Cancelled() <-chan struct{} { return r.cancelled }

func (r *request) cancel() { r.once.Do(func() { close(r.cancelled) }) }

// end unexports the Request: it never outlives its method call.
func (r *request) end() { r.unexport() }

// requestObject carries the Request's D-Bus methods.
type requestObject struct{ r *request }

// Close aborts the interaction the request belongs to.
func (o requestObject) Close() *dbus.Error {
	o.r.cancel()
	return nil
}

// call is one method call's Request at its handle. ctx ends when the
// app closes the request, and the shell is then asked to take down the
// dialog the call shows; the call's Request lives until end.
type call struct {
	ctx    context.Context
	caller portaldialogs.Caller
	req    *request
	cancel context.CancelFunc
	done   chan struct{}
	// watching ends once the Close watcher returned, its dismissal
	// (if any) sent.
	watching sync.WaitGroup
}

// begin mounts the Request for one call to the app's request at handle.
// dismiss, when set, is the shell service's Dismiss: it takes the call's
// dialog down should the app close the request mid-dialog.
func begin(conn *dbus.Conn, handle dbus.ObjectPath, appID, parentWindow string, dismiss func(ctx context.Context, handle string) error) (*call, error) {
	req, err := mountRequest(conn, handle)
	if err != nil {
		return nil, fmt.Errorf("mount the request at %s: %w", handle, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &call{
		ctx: ctx, caller: portaldialogs.Caller{Handle: string(handle), AppID: appID, ParentWindow: parentWindow, Modal: true},
		req: req, cancel: cancel, done: make(chan struct{}),
	}
	c.watching.Go(func() {
		select {
		case <-req.Cancelled():
			cancel()
			if dismiss == nil {
				return
			}
			dctx, dcancel := context.WithTimeout(context.Background(), shellCallTimeout)
			defer dcancel()
			if err := dismiss(dctx, string(handle)); err != nil {
				warnf("dismissing the dialog for %s: %v", handle, err)
			}
		case <-c.done:
		}
	})
	return c, nil
}

// modal takes the app's modal option onto the caller; absent, the
// dialog stays modal.
func (c *call) modal(options Vardict) {
	if modal, ok := optBool(options, "modal"); ok {
		c.caller.Modal = modal
	}
}

// closed reports whether the app closed the request.
func (c *call) closed() bool {
	select {
	case <-c.req.Cancelled():
		return true
	default:
		return false
	}
}

// end unexports the Request: it never outlives its method call. A
// dismissal the app's Close set off has reached the shell by then.
func (c *call) end() {
	close(c.done)
	c.watching.Wait()
	c.cancel()
	c.req.end()
}

// beginOr is begin for a method replying (u, a{sv}): a Request that
// cannot be mounted fails the call with Other.
func beginOr(conn *dbus.Conn, handle dbus.ObjectPath, appID, parentWindow string, dismiss func(context.Context, string) error) (*call, bool) {
	c, err := begin(conn, handle, appID, parentWindow, dismiss)
	if err != nil {
		warnf("%v", err)
		return nil, false
	}
	return c, true
}

// response maps a dialog's outcome onto a reply code: the app having
// closed the request is a cancel whatever the shell answered; then a
// failed dialog (no shell) is other, a no a cancel, a yes success.
func (c *call) response(what string, ok bool, err error) uint32 {
	switch {
	case c.closed():
		return ResponseCancelled
	case err != nil:
		warnf("%s: dialog failed: %v", what, err)
		return ResponseOther
	case !ok:
		return ResponseCancelled
	}
	return ResponseSuccess
}
