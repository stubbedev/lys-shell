// Package filechooser is the shell's dev.stubbe.lys.FileChooser1 host
// (lys-shell services/file_chooser and shell/file_chooser): the file
// dialog behind the portal backend's FileChooser.
package filechooser

import (
	"context"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// D-Bus identity (wayle-ipc file_chooser.rs).
const (
	ServiceName = "dev.stubbe.lys.FileChooser1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/FileChooser")
	iface       = "dev.stubbe.lys.FileChooser1"
)

// Rule kinds of a Filter.
const (
	RuleGlob uint32 = 0
	RuleMIME uint32 = 1
)

// Rule is one (kind, value) of a filter: a glob or a MIME type.
type Rule struct {
	Kind  uint32
	Value string
}

// Filter is a named list of rules, the portal's (sa(us)).
type Filter struct {
	Name  string
	Rules []Rule
}

// OpenRequest is one open dialog.
type OpenRequest struct {
	Title               string
	Multiple, Directory bool
	Filters             []Filter
	// CurrentFolder seeds the starting directory ("" is the default).
	CurrentFolder string
}

// SaveRequest is one save dialog.
type SaveRequest struct {
	Title         string
	CurrentName   string
	Filters       []Filter
	CurrentFolder string
}

// Host shows the dialogs; each blocks until the user answers and
// replies the chosen file:// URIs, none on a cancel or when the request
// is dismissed.
type Host interface {
	Open(portalreq.Caller, OpenRequest) []string
	Save(portalreq.Caller, SaveRequest) []string
	// Dismiss takes down the dialog showing for the request at handle.
	Dismiss(handle string)
}

// Daemon serves a Host as dev.stubbe.lys.FileChooser1.
type Daemon struct{ host Host }

// NewDaemon wraps the host for export.
func NewDaemon(host Host) *Daemon { return &Daemon{host: host} }

// Export serves the interface under its name; the returned release
// drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{Name: ServiceName, Path: ServicePath, Interface: iface, Methods: d})
}

// OpenFile opens existing files or a directory.
func (d *Daemon) OpenFile(c portalreq.Caller, title string, multiple, directory bool, filters []Filter, currentFolder string) ([]string, *dbus.Error) {
	return nonNil(d.host.Open(c, OpenRequest{title, multiple, directory, filters, currentFolder})), nil
}

// SaveFile chooses a save destination.
func (d *Daemon) SaveFile(c portalreq.Caller, title, currentName string, filters []Filter, currentFolder string) ([]string, *dbus.Error) {
	return nonNil(d.host.Save(c, SaveRequest{title, currentName, filters, currentFolder})), nil
}

// Dismiss takes down the dialog showing for the request at handle.
func (d *Daemon) Dismiss(handle string) *dbus.Error {
	d.host.Dismiss(handle)
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Client calls the shell's dialog; it waits on the user, so it is
// bounded only by ctx.
type Client struct{ obj dbus.BusObject }

// NewClient addresses the host over conn.
func NewClient(conn *dbus.Conn) *Client { return &Client{obj: conn.Object(ServiceName, ServicePath)} }

// Open shows the open dialog.
func (c *Client) Open(ctx context.Context, caller portalreq.Caller, r OpenRequest) (uris []string, err error) {
	err = c.obj.CallWithContext(ctx, iface+".OpenFile", 0, caller, r.Title, r.Multiple, r.Directory, nonNil(r.Filters), r.CurrentFolder).Store(&uris)
	return uris, err
}

// Save shows the save dialog.
func (c *Client) Save(ctx context.Context, caller portalreq.Caller, r SaveRequest) (uris []string, err error) {
	err = c.obj.CallWithContext(ctx, iface+".SaveFile", 0, caller, r.Title, r.CurrentName, nonNil(r.Filters), r.CurrentFolder).Store(&uris)
	return uris, err
}

// Dismiss takes down the dialog showing for the request at handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
