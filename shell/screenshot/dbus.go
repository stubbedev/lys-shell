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
	PickColor(c portalreq.Caller) (r, g, b float64, err error)
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
// output connector name (output mode) or empty. Replies the saved PNG
// path, empty when a region selection was cancelled or dismissed.
func (d *Daemon) Capture(c portalreq.Caller, mode, target string) (string, *dbus.Error) {
	path, err := d.host.Capture(c, mode, target)
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return path, nil
}

// PickColor picks one screen color interactively, replying sRGB
// (r, g, b) in [0, 1]; cancelling is an error.
func (d *Daemon) PickColor(c portalreq.Caller) (float64, float64, float64, *dbus.Error) {
	r, g, b, err := d.host.PickColor(c)
	if err != nil {
		return 0, 0, 0, dbus.MakeFailedError(err)
	}
	return r, g, b, nil
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

// PickColor asks the shell for an interactive color pick.
func (c *Client) PickColor(ctx context.Context, caller portalreq.Caller) (r, g, b float64, err error) {
	err = c.obj.CallWithContext(ctx, iface+".PickColor", 0, caller).Store(&r, &g, &b)
	return r, g, b, err
}

// Dismiss cancels the selection or pick showing for the request at
// handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
