package screenshot

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// D-Bus identity (wayle-ipc's screenshot proxy).
const (
	ServiceName = "dev.stubbe.lys.Screenshot1"
	ServicePath = "/dev/stubbe/lys/Screenshot"
	iface       = "dev.stubbe.lys.Screenshot1"
)

// capturer is what the daemon serves: the Host, or a fake in tests.
type capturer interface {
	Capture(c portalreq.Caller, mode, target string) (string, error)
	CapturePrivate(c portalreq.Caller, mode, target string, pointer bool) (string, error)
	PickColor(c portalreq.Caller) (color [3]float64, picked bool, err error)
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
// path, empty when a region selection was cancelled or dismissed.
func (d *Daemon) Capture(c portalreq.Caller, mode, target string) (string, *dbus.Error) {
	path, err := d.host.Capture(c, mode, target)
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return path, nil
}

// CapturePrivate captures a screenshot for an app as Capture does, to
// a private file and with none of the user's save, clipboard or
// notification settings; pointer composites the pointer in.
func (d *Daemon) CapturePrivate(c portalreq.Caller, mode, target string, pointer bool) (string, *dbus.Error) {
	path, err := d.host.CapturePrivate(c, mode, target, pointer)
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return path, nil
}

// PickColor picks one screen color interactively, replying whether the
// user picked one and its sRGB (r, g, b) in [0, 1]; a failure is an
// error.
func (d *Daemon) PickColor(c portalreq.Caller) (bool, float64, float64, float64, *dbus.Error) {
	color, picked, err := d.host.PickColor(c)
	if err != nil {
		return false, 0, 0, 0, dbus.MakeFailedError(err)
	}
	return picked, color[0], color[1], color[2], nil
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

// Capture asks the shell for a screenshot; see Daemon.Capture.
func (c *Client) Capture(ctx context.Context, caller portalreq.Caller, mode, target string) (string, error) {
	var path string
	err := c.obj.CallWithContext(ctx, iface+".Capture", 0, caller, mode, target).Store(&path)
	return path, err
}

// CapturePrivate asks the shell for an app's screenshot; see
// Daemon.CapturePrivate.
func (c *Client) CapturePrivate(ctx context.Context, caller portalreq.Caller, mode, target string, pointer bool) (string, error) {
	var path string
	err := c.obj.CallWithContext(ctx, iface+".CapturePrivate", 0, caller, mode, target, pointer).Store(&path)
	return path, err
}

// PickColor asks the shell for an interactive color pick; picked is
// false for the user's cancel.
func (c *Client) PickColor(ctx context.Context, caller portalreq.Caller) (color [3]float64, picked bool, err error) {
	err = c.obj.CallWithContext(ctx, iface+".PickColor", 0, caller).Store(&picked, &color[0], &color[1], &color[2])
	return color, picked, err
}

// Dismiss cancels the selection or pick showing for the request at
// handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
