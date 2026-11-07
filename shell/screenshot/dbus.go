package screenshot

import (
	"context"
	"errors"
	"fmt"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
)

// D-Bus identity (wayle-ipc's screenshot proxy).
const (
	ServiceName = "dev.stubbe.lys.Screenshot1"
	ServicePath = "/dev/stubbe/lys/Screenshot"
	iface       = "dev.stubbe.lys.Screenshot1"
	// busyError is the error reply carrying ErrBusy, distinct from a
	// failed capture.
	busyError = iface + ".Error.Busy"
)

// errorReply is err on the wire.
func errorReply(err error) *dbus.Error {
	if errors.Is(err, ErrBusy) {
		return dbus.NewError(busyError, []any{err.Error()})
	}
	return dbus.MakeFailedError(err)
}

// replyErr reads an error reply back: busyError is ErrBusy.
func replyErr(err error) error {
	if e, ok := errors.AsType[dbus.Error](err); ok && e.Name == busyError {
		return ErrBusy
	}
	return err
}

// capturer is what the daemon serves: the Host, or a fake in tests.
type capturer interface {
	Capture(c portaldialogs.Caller, mode, target string) (string, error)
	CapturePrivate(c portaldialogs.Caller, mode, target string, pointer bool) (string, error)
	PickColor(c portaldialogs.Caller) (color [3]float64, picked bool, err error)
	PickColorText(c portaldialogs.Caller, opts colorpicker.Options, copyText bool) (text string, picked bool, err error)
	Dismiss(handle string)
}

// Daemon exports a Host as dev.stubbe.lys.Screenshot1.
type Daemon struct {
	host capturer
}

// NewDaemon wraps the host for export.
func NewDaemon(h *Host) *Daemon { return &Daemon{host: h} }

// Export exports the object and claims the well-known name; the
// returned release drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	if err := conn.Export(d, ServicePath, iface); err != nil {
		return nil, fmt.Errorf("screenshot: export: %w", err)
	}
	reply, err := conn.RequestName(ServiceName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("screenshot: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("screenshot: %s is already owned", ServiceName)
	}
	return func() { _, _ = conn.ReleaseName(ServiceName) }, nil
}

// Capture captures a screenshot and blocks until it is saved or
// cancelled. mode is region, output, screen, or window; target is an
// output connector name (output mode), a window's ext toplevel
// identifier (window mode; the active window when empty), or empty. Replies the saved PNG
// path, empty when a region selection was cancelled or dismissed; every
// mode fails with the Busy error while a capture overlay is open.
func (d *Daemon) Capture(c portaldialogs.Caller, mode, target string) (string, *dbus.Error) {
	path, err := d.host.Capture(c, mode, target)
	if err != nil {
		return "", errorReply(err)
	}
	return path, nil
}

// CapturePrivate captures a screenshot for an app as Capture does, to
// a private file and with none of the user's save, clipboard or
// notification settings; pointer composites the pointer in.
func (d *Daemon) CapturePrivate(c portaldialogs.Caller, mode, target string, pointer bool) (string, *dbus.Error) {
	path, err := d.host.CapturePrivate(c, mode, target, pointer)
	if err != nil {
		return "", errorReply(err)
	}
	return path, nil
}

// PickColor picks one screen color interactively, replying whether the
// user picked one and its sRGB (r, g, b) in [0, 1]; a failure is an
// error, the Busy error while a capture overlay is open.
func (d *Daemon) PickColor(c portaldialogs.Caller) (bool, float64, float64, float64, *dbus.Error) {
	color, picked, err := d.host.PickColor(c)
	if err != nil {
		return false, 0, 0, 0, errorReply(err)
	}
	return picked, color[0], color[1], color[2], nil
}

// PickColorText picks one screen color interactively as PickColor does,
// replying whether the user picked one and the color written in the
// format the readout showed at the click. format names the readout's
// first format, empty for the last one used; noLoupe shows a crosshair
// instead of the magnifier; copyText puts the text on the clipboard.
func (d *Daemon) PickColorText(c portaldialogs.Caller, format string, noLoupe, copyText bool) (bool, string, *dbus.Error) {
	opts := colorpicker.Options{NoLoupe: noLoupe}
	if format != "" {
		f, err := config.ParseColorFormat(format)
		if err != nil {
			return false, "", dbus.MakeFailedError(err)
		}
		opts.Format = f
	}
	text, picked, err := d.host.PickColorText(c, opts, copyText)
	if err != nil {
		return false, "", errorReply(err)
	}
	return picked, text, nil
}

// Dismiss cancels the region selection or color pick showing for the
// request at handle.
func (d *Daemon) Dismiss(handle string) *dbus.Error {
	d.host.Dismiss(handle)
	return nil
}

// Client calls the daemon: the `lys-shell screenshot` CLI, and the portal
// backend's Screenshot and PickColor.
type Client struct {
	obj dbus.BusObject
}

// NewClient addresses the daemon over conn.
func NewClient(conn *dbus.Conn) *Client {
	return &Client{obj: conn.Object(ServiceName, ServicePath)}
}

// Capture asks the shell for a screenshot; see Daemon.Capture. A refused
// request is ErrBusy.
func (c *Client) Capture(ctx context.Context, caller portaldialogs.Caller, mode, target string) (string, error) {
	var path string
	err := c.obj.CallWithContext(ctx, iface+".Capture", 0, caller, mode, target).Store(&path)
	return path, replyErr(err)
}

// CapturePrivate asks the shell for an app's screenshot; see
// Daemon.CapturePrivate. A refused request is ErrBusy.
func (c *Client) CapturePrivate(ctx context.Context, caller portaldialogs.Caller, mode, target string, pointer bool) (string, error) {
	var path string
	err := c.obj.CallWithContext(ctx, iface+".CapturePrivate", 0, caller, mode, target, pointer).Store(&path)
	return path, replyErr(err)
}

// PickColor asks the shell for an interactive color pick; picked is
// false for the user's cancel, and a refused request is ErrBusy.
func (c *Client) PickColor(ctx context.Context, caller portaldialogs.Caller) (color [3]float64, picked bool, err error) {
	err = c.obj.CallWithContext(ctx, iface+".PickColor", 0, caller).Store(&picked, &color[0], &color[1], &color[2])
	return color, picked, replyErr(err)
}

// PickColorText asks the shell for an interactive color pick written in
// a format; see Daemon.PickColorText. An empty format starts in the one
// last used. picked is false for the user's cancel, and a refused
// request is ErrBusy.
func (c *Client) PickColorText(ctx context.Context, caller portaldialogs.Caller, format config.ColorFormat, noLoupe, copyText bool) (text string, picked bool, err error) {
	err = c.obj.CallWithContext(ctx, iface+".PickColorText", 0, caller, string(format), noLoupe, copyText).Store(&picked, &text)
	return text, picked, replyErr(err)
}

// Dismiss cancels the selection or pick showing for the request at
// handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
