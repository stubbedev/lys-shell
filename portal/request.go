package portal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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
// dialog the call shows once the call reached it; the call's Request
// lives until end.
type call struct {
	conn   *dbus.Conn
	ctx    context.Context
	caller portaldialogs.Caller
	req    *request
	cancel context.CancelFunc
	done   chan struct{}
	// watching ends once the Close watcher returned, its dismissal
	// (if any) sent.
	watching sync.WaitGroup
	// reached is set once a shell service the call shows a dialog
	// through has an owner, so there may be a dialog to dismiss.
	reached atomic.Bool
	// missing is why a shell service did not appear: every later shell
	// call fails at once, and the reply is other.
	missing error
}

// begin mounts the Request for one call to the app's request at handle.
// dismiss, when set, is the shell service's Dismiss: it takes the call's
// dialog down should the app close the request mid-dialog, or ended
// close: the end of the session the call runs on closes its Request.
func begin(conn *dbus.Conn, handle dbus.ObjectPath, appID, parentWindow string, ended <-chan struct{}, dismiss func(ctx context.Context, handle string) error) (*call, error) {
	req, err := mountRequest(conn, handle)
	if err != nil {
		return nil, fmt.Errorf("mount the request at %s: %w", handle, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &call{
		conn: conn, ctx: ctx, caller: portaldialogs.Caller{Handle: string(handle), AppID: appID, ParentWindow: parentWindow, Modal: true},
		req: req, cancel: cancel, done: make(chan struct{}),
	}
	c.watching.Go(func() {
		select {
		case <-ended:
			req.cancel()
		case <-req.Cancelled():
		case <-c.done:
			return
		}
		cancel()
		if dismiss == nil || !c.reached.Load() {
			return
		}
		dctx, dcancel := context.WithTimeout(context.Background(), shellCallTimeout)
		defer dcancel()
		if err := dismiss(dctx, string(handle)); err != nil {
			warnf("dismissing the dialog for %s: %v", handle, err)
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

// shellAppearBound bounds the wait for the shell to own a service a call
// shows its dialog through: the shell's RestartSec after a crash and its
// start fit well within it.
var shellAppearBound = 10 * time.Second

// on is the context to call the shell's service with. A service without
// an owner (the shell restarting, or not started) is waited for, and
// activated where the bus can, for up to shellAppearBound; one that does
// not appear, or the app closing the request meanwhile, is a cancelled
// context, which fails the call without sending it.
func (c *call) on(service string) context.Context {
	if c.missing == nil {
		err := c.awaitOwner(service)
		if err == nil {
			c.reached.Store(true)
			return c.ctx
		}
		if !c.closed() {
			warnf("%v", err)
		}
		c.missing = err
	}
	ctx, cancel := context.WithCancelCause(c.ctx)
	cancel(c.missing)
	return ctx
}

// awaitOwner returns once service has an owner, or with why it has none.
func (c *call) awaitOwner(service string) error {
	if _, err := dbusx.NameOwner(c.ctx, c.conn, service); err == nil {
		return nil
	}
	appeared := make(chan struct{}, 1)
	stop, err := dbusx.WatchOwner(c.ctx, c.conn, service, func() {
		select {
		case appeared <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("wait for %s: %w", service, err)
	}
	defer func() {
		if err := stop(); err != nil {
			warnf("%v", err)
		}
	}()
	if _, err := dbusx.NameOwner(c.ctx, c.conn, service); err == nil {
		return nil
	}
	activated := c.conn.BusObject().GoWithContext(c.ctx, "org.freedesktop.DBus.StartServiceByName", 0, nil, service, uint32(0)).Done
	bound := time.NewTimer(shellAppearBound)
	defer bound.Stop()
	for {
		select {
		case <-appeared:
			return nil
		case call := <-activated:
			activated = nil
			if err := call.Err; err != nil && !isServiceUnknown(err) {
				warnf("activating %s: %v", service, err)
			}
		case <-c.ctx.Done():
			return fmt.Errorf("wait for %s: %w", service, c.ctx.Err())
		case <-bound.C:
			return fmt.Errorf("the shell does not own %s after %v", service, shellAppearBound)
		}
	}
}

// isServiceUnknown reports whether err is the bus saying it cannot
// activate a name: nothing installs an activation file for it.
func isServiceUnknown(err error) bool {
	dbusErr, ok := errors.AsType[dbus.Error](err)
	return ok && dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown"
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
	return callOr(begin(conn, handle, appID, parentWindow, nil, dismiss))
}

// beginOnSession is beginOr for a call on session s: the session ending
// mid-call closes the call's Request, and so dismisses its dialog.
func beginOnSession(conn *dbus.Conn, handle dbus.ObjectPath, s *session, parentWindow string, dismiss func(context.Context, string) error) (*call, bool) {
	return callOr(begin(conn, handle, s.appID, parentWindow, s.ended, dismiss))
}

func callOr(c *call, err error) (*call, bool) {
	if err != nil {
		warnf("%v", err)
		return nil, false
	}
	return c, true
}

// response maps a dialog's outcome onto a reply code: the app having
// closed the request is a cancel whatever the shell answered; then a
// failed dialog (no shell, or one that left) is other, a no a cancel, a yes success.
func (c *call) response(what string, ok bool, err error) uint32 {
	switch {
	case c.closed():
		return ResponseCancelled
	case c.missing != nil:
		return ResponseOther
	case err != nil:
		warnf("%s: dialog failed: %v", what, err)
		return ResponseOther
	case !ok:
		return ResponseCancelled
	}
	return ResponseSuccess
}
