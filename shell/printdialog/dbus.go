// Package printdialog is the shell's dev.stubbe.lys.Print1 host (lys-shell
// services/print and shell/print): the print dialog and spooler behind
// the portal backend's Print.
package printdialog

import (
	"context"
	"os"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// D-Bus identity (wayle-ipc print.rs).
const (
	ServiceName = "dev.stubbe.lys.Print1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/Print")
	iface       = "dev.stubbe.lys.Print1"
)

// Setting is one flat print-setting key/value pair.
type Setting struct{ Key, Value string }

// Host shows the dialog and spools the jobs.
type Host interface {
	// Prepare lets the user pick a printer and settings for title and
	// stashes them under the returned token; granted is false on a
	// cancel or when the request is dismissed.
	Prepare(c portalreq.Caller, title string) (granted bool, settings []Setting, token uint32)
	// Print spools document (a PDF the host takes ownership of) to the
	// printer prepared under token, and reports whether it was sent.
	Print(c portalreq.Caller, title string, document *os.File, token uint32) bool
	// Dismiss takes down the dialog showing for the request at handle.
	Dismiss(handle string)
}

// Daemon serves a Host as dev.stubbe.lys.Print1.
type Daemon struct{ host Host }

// NewDaemon wraps the host for export.
func NewDaemon(host Host) *Daemon { return &Daemon{host: host} }

// Export serves the interface under its name; the returned release
// drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{Name: ServiceName, Path: ServicePath, Interface: iface, Methods: d})
}

// Prepare shows the dialog.
func (d *Daemon) Prepare(c portalreq.Caller, title string) (bool, []Setting, uint32, *dbus.Error) {
	granted, settings, token := d.host.Prepare(c, title)
	if settings == nil {
		settings = []Setting{}
	}
	return granted, settings, token, nil
}

// Print spools document.
func (d *Daemon) Print(c portalreq.Caller, title string, document dbus.UnixFD, token uint32) (bool, *dbus.Error) {
	return d.host.Print(c, title, os.NewFile(uintptr(document), "document"), token), nil
}

// Dismiss takes down the dialog showing for the request at handle.
func (d *Daemon) Dismiss(handle string) *dbus.Error {
	d.host.Dismiss(handle)
	return nil
}

// Client calls the shell's print host; Prepare waits on the user.
type Client struct{ obj dbus.BusObject }

// NewClient addresses the host over conn.
func NewClient(conn *dbus.Conn) *Client { return &Client{obj: conn.Object(ServiceName, ServicePath)} }

// Prepare shows the dialog.
func (c *Client) Prepare(ctx context.Context, caller portalreq.Caller, title string) (granted bool, settings []Setting, token uint32, err error) {
	err = c.obj.CallWithContext(ctx, iface+".Prepare", 0, caller, title).Store(&granted, &settings, &token)
	return granted, settings, token, err
}

// Print spools document, which stays the caller's to close.
func (c *Client) Print(ctx context.Context, caller portalreq.Caller, title string, document dbus.UnixFD, token uint32) (sent bool, err error) {
	err = c.obj.CallWithContext(ctx, iface+".Print", 0, caller, title, document, token).Store(&sent)
	return sent, err
}

// Dismiss takes down the dialog showing for the request at handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
