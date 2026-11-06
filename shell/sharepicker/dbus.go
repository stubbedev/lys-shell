package sharepicker

import (
	"context"
	"errors"
	"fmt"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// D-Bus identity (wayle-ipc's share_picker proxy).
const (
	ServiceName = "dev.stubbe.lys.SharePicker1"
	ServicePath = "/dev/stubbe/lys/SharePicker"
	iface       = "dev.stubbe.lys.SharePicker1"
)

// Sources are the kinds of source a request may pick, a bit set.
type Sources uint32

// The kinds of source; a region is part of an output.
const (
	SourceOutputs Sources = 1 << iota
	SourceWindows
	SourceRegions
	AllSources = SourceOutputs | SourceWindows | SourceRegions
)

// Request is one pick, (subb): the XDPH window list ("" without one),
// the kinds of source offered, whether a restore token may be granted,
// and whether several sources may be picked.
type Request struct {
	WindowList string
	Sources    Sources
	AllowToken bool
	Multiple   bool
}

// WindowRequest is one click-to-pick, (a(ssiiii)s): the windows a click
// can pick, front to back on each output, and the output Enter shares,
// "" for none.
type WindowRequest struct {
	Windows []regionoverlay.Window
	Screen  string
}

// picker is what the daemon serves: the Picker, or a fake in tests.
type picker interface {
	Pick(portaldialogs.Caller, Request) string
	PickWindow(portaldialogs.Caller, WindowRequest) (string, error)
	Dismiss(handle string)
}

// Daemon exports a Picker as dev.stubbe.lys.SharePicker1.
type Daemon struct {
	picker picker
}

// NewDaemon wraps the picker for export.
func NewDaemon(p *Picker) *Daemon { return &Daemon{picker: p} }

// Export exports the object and claims the well-known name; the
// returned release drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	if err := conn.Export(d, ServicePath, iface); err != nil {
		return nil, fmt.Errorf("share picker: export: %w", err)
	}
	reply, err := conn.RequestName(ServiceName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("share picker: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("share picker: %s is already owned", ServiceName)
	}
	return func() { _, _ = conn.ReleaseName(ServiceName) }, nil
}

// Pick shows the picker and blocks until the user selects or cancels,
// replying the XDPH selection suffix (printed after `[SELECTION]`), or
// an empty string on cancel.
// A request offering no kind of source is refused.
func (d *Daemon) Pick(c portaldialogs.Caller, r Request) (string, *dbus.Error) {
	if r.Sources&AllSources == 0 {
		return "", dbus.MakeFailedError(fmt.Errorf("share picker: no kind of source to offer in %#x", uint32(r.Sources)))
	}
	return d.picker.Pick(c, r), nil
}

// PickWindow opens the click-to-pick overlay and blocks until a click
// picks a window, replying /window:<id>, or Enter shares the screen,
// replying /screen:<name>; Escape replies an empty string, which
// cancels, as Pick's does. A request offering nothing to pick is refused,
// and an overlay that cannot open fails, so the caller asks the picker.
func (d *Daemon) PickWindow(c portaldialogs.Caller, r WindowRequest) (string, *dbus.Error) {
	if len(r.Windows) == 0 {
		return "", dbus.MakeFailedError(errors.New("share picker: no window to pick from"))
	}
	selection, err := d.picker.PickWindow(c, r)
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return selection, nil
}

// Dismiss cancels the picker showing for the request at handle.
func (d *Daemon) Dismiss(handle string) *dbus.Error {
	d.picker.Dismiss(handle)
	return nil
}

// Client calls the daemon: the `lys-shell portal share-picker` stub XDPH
// execs, and the portal backend's ScreenCast source selection.
type Client struct {
	obj dbus.BusObject
}

// NewClient addresses the daemon over conn.
func NewClient(conn *dbus.Conn) *Client {
	return &Client{obj: conn.Object(ServiceName, ServicePath)}
}

// Pick asks the shell to show the picker; see Daemon.Pick.
func (c *Client) Pick(ctx context.Context, caller portaldialogs.Caller, r Request) (string, error) {
	var selection string
	err := c.obj.CallWithContext(ctx, iface+".Pick", 0, caller, r).Store(&selection)
	return selection, err
}

// PickWindow asks the shell for the click-to-pick overlay; see
// Daemon.PickWindow.
func (c *Client) PickWindow(ctx context.Context, caller portaldialogs.Caller, r WindowRequest) (string, error) {
	var selection string
	err := c.obj.CallWithContext(ctx, iface+".PickWindow", 0, caller, r).Store(&selection)
	return selection, err
}

// Dismiss cancels the picker showing for the request at handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
