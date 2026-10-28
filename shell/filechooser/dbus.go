// Package filechooser is the shell's dev.stubbe.lys.FileChooser1 host
// (lys-shell services/file_chooser and shell/file_chooser): the file
// dialog behind the portal backend's FileChooser.
package filechooser

import (
	"context"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
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
	// AcceptLabel names the confirm button ("" for the mode's own).
	AcceptLabel string
	// CurrentFilter is the index of the filter to start on (the
	// first by default).
	CurrentFilter int32
	// Choices are extra questions shown with the dialog.
	Choices []portaldialogs.Choice
}

// SaveRequest is one save dialog.
type SaveRequest struct {
	Title         string
	CurrentName   string
	Filters       []Filter
	CurrentFolder string
	AcceptLabel   string
	CurrentFilter int32
	Choices       []portaldialogs.Choice
}

// Result is what a dialog answers, (asa(sa(us))a(ss)) on the wire as
// three values: the chosen file:// URIs (none on a cancel or a
// dismissal), the filter active at the end (a nameless empty one for
// none), and what was picked for each choice.
type Result struct {
	URIs    []string
	Filter  Filter
	Choices []portaldialogs.Selection
}

// Host shows the dialogs; each blocks until the user answers.
type Host interface {
	Open(portaldialogs.Caller, OpenRequest) Result
	Save(portaldialogs.Caller, SaveRequest) Result
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

// OpenFile opens existing files or directories.
func (d *Daemon) OpenFile(c portaldialogs.Caller, r OpenRequest) ([]string, Filter, []portaldialogs.Selection, *dbus.Error) {
	res := d.host.Open(c, r)
	return nonNil(res.URIs), withRules(res.Filter), nonNil(res.Choices), nil
}

// SaveFile chooses a save destination.
func (d *Daemon) SaveFile(c portaldialogs.Caller, r SaveRequest) ([]string, Filter, []portaldialogs.Selection, *dbus.Error) {
	res := d.host.Save(c, r)
	return nonNil(res.URIs), withRules(res.Filter), nonNil(res.Choices), nil
}

// withRules gives a filter a non-nil rule list for the wire.
func withRules(f Filter) Filter {
	f.Rules = nonNil(f.Rules)
	return f
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
func (c *Client) Open(ctx context.Context, caller portaldialogs.Caller, r OpenRequest) (res Result, err error) {
	r.Filters, r.Choices = wireFilters(r.Filters), wireChoices(r.Choices)
	err = c.obj.CallWithContext(ctx, iface+".OpenFile", 0, caller, r).Store(&res.URIs, &res.Filter, &res.Choices)
	return res, err
}

// Save shows the save dialog.
func (c *Client) Save(ctx context.Context, caller portaldialogs.Caller, r SaveRequest) (res Result, err error) {
	r.Filters, r.Choices = wireFilters(r.Filters), wireChoices(r.Choices)
	err = c.obj.CallWithContext(ctx, iface+".SaveFile", 0, caller, r).Store(&res.URIs, &res.Filter, &res.Choices)
	return res, err
}

// wireFilters gives every list a filter list holds a non-nil value, as
// the wire types an empty one.
func wireFilters(filters []Filter) []Filter {
	out := make([]Filter, len(filters))
	for i, f := range filters {
		out[i] = withRules(f)
	}
	return out
}

// wireChoices does the same for choices.
func wireChoices(choices []portaldialogs.Choice) []portaldialogs.Choice {
	out := make([]portaldialogs.Choice, len(choices))
	for i, c := range choices {
		c.Options = nonNil(c.Options)
		out[i] = c
	}
	return out
}

// Dismiss takes down the dialog showing for the request at handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
