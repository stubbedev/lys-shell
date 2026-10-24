// Package portaldialogs is the shell's dev.stubbe.lys.PortalDialogs1 host
// (lys-shell services/portal_dialogs and shell/portal_dialogs): the
// native dialogs behind the portal backend's Access, Account,
// AppChooser, DynamicLauncher and Wallpaper preview, in place of
// xdg-desktop-portal-gtk.
package portaldialogs

import (
	"context"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// D-Bus identity (wayle-ipc portal_dialogs.rs).
const (
	ServiceName = "dev.stubbe.lys.PortalDialogs1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/PortalDialogs")
	iface       = "dev.stubbe.lys.PortalDialogs1"
)

// AccessRequest is one grant/deny prompt.
type AccessRequest struct {
	Title, Subtitle, Body string
	GrantLabel, DenyLabel string
	// Icon is a themed icon name for the permission, "" for none.
	Icon string
	// Choices are extra questions shown with the prompt.
	Choices []portalreq.Choice
}

// AccountInfo is what the Account dialog shares, (ss): the name and an
// avatar image path ("" for none).
type AccountInfo struct{ Name, Image string }

// Host shows the dialogs; each call blocks until the user answers or
// the request is dismissed, which answers no ("" for a chooser).
type Host interface {
	// Access replies whether the user granted and, when granted, what
	// they picked for each choice.
	Access(portalreq.Caller, AccessRequest) (bool, []portalreq.Selection)
	// Account offers info for sharing and replies whether the user
	// shares it, and what they left of it after editing.
	Account(c portalreq.Caller, reason string, info AccountInfo) (bool, AccountInfo)
	// ConfirmWallpaper previews a file:// image and replies whether to
	// set it.
	ConfirmWallpaper(c portalreq.Caller, uri string) bool
	// ChooseApplication replies the chosen desktop-file id, "" on
	// cancel; choices are candidate ids (none offers every app).
	ChooseApplication(c portalreq.Caller, choices []string, contentType, uri string) string
	// ConfirmInstall replies whether to install a dynamic launcher.
	ConfirmInstall(c portalreq.Caller, name, iconName string) bool
	// Dismiss takes down the dialog showing for the request at handle,
	// when one is; the app closed the request.
	Dismiss(handle string)
}

// Daemon serves a Host as dev.stubbe.lys.PortalDialogs1.
type Daemon struct{ host Host }

// NewDaemon wraps the host for export.
func NewDaemon(host Host) *Daemon { return &Daemon{host: host} }

// Export serves the interface under its name; the returned release
// drops the name.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{Name: ServiceName, Path: ServicePath, Interface: iface, Methods: d})
}

// Access is the grant/deny prompt.
func (d *Daemon) Access(c portalreq.Caller, title, subtitle, body, grantLabel, denyLabel, icon string, choices []portalreq.Choice) (bool, []portalreq.Selection, *dbus.Error) {
	granted, picked := d.host.Access(c, AccessRequest{title, subtitle, body, grantLabel, denyLabel, icon, choices})
	if picked == nil {
		picked = []portalreq.Selection{}
	}
	return granted, picked, nil
}

// Account is the account-info consent.
func (d *Daemon) Account(c portalreq.Caller, reason string, info AccountInfo) (bool, AccountInfo, *dbus.Error) {
	shared, edited := d.host.Account(c, reason, info)
	return shared, edited, nil
}

// ConfirmWallpaper is the wallpaper preview confirmation.
func (d *Daemon) ConfirmWallpaper(c portalreq.Caller, uri string) (bool, *dbus.Error) {
	return d.host.ConfirmWallpaper(c, uri), nil
}

// ChooseApplication is the app chooser.
func (d *Daemon) ChooseApplication(c portalreq.Caller, choices []string, contentType, uri string) (string, *dbus.Error) {
	return d.host.ChooseApplication(c, choices, contentType, uri), nil
}

// ConfirmInstall is the dynamic-launcher install confirmation.
func (d *Daemon) ConfirmInstall(c portalreq.Caller, name, iconName string) (bool, *dbus.Error) {
	return d.host.ConfirmInstall(c, name, iconName), nil
}

// Dismiss takes down the dialog showing for the request at handle.
func (d *Daemon) Dismiss(handle string) *dbus.Error {
	d.host.Dismiss(handle)
	return nil
}

// Client calls the shell's dialogs; a dialog waits on the user, so the
// calls are bounded only by ctx.
type Client struct{ obj dbus.BusObject }

// NewClient addresses the host over conn.
func NewClient(conn *dbus.Conn) *Client { return &Client{obj: conn.Object(ServiceName, ServicePath)} }

func (c *Client) call(ctx context.Context, method string, out any, args ...any) error {
	return c.obj.CallWithContext(ctx, iface+"."+method, 0, args...).Store(out)
}

// Access shows the grant/deny prompt.
func (c *Client) Access(ctx context.Context, caller portalreq.Caller, r AccessRequest) (granted bool, picked []portalreq.Selection, err error) {
	choices := r.Choices
	if choices == nil {
		choices = []portalreq.Choice{}
	}
	err = c.obj.CallWithContext(ctx, iface+".Access", 0, caller, r.Title, r.Subtitle, r.Body, r.GrantLabel, r.DenyLabel, r.Icon, choices).Store(&granted, &picked)
	return granted, picked, err
}

// Account asks to share info, which the user may edit first.
func (c *Client) Account(ctx context.Context, caller portalreq.Caller, reason string, info AccountInfo) (shared bool, edited AccountInfo, err error) {
	err = c.obj.CallWithContext(ctx, iface+".Account", 0, caller, reason, info).Store(&shared, &edited)
	return shared, edited, err
}

// ConfirmWallpaper previews uri and asks to set it.
func (c *Client) ConfirmWallpaper(ctx context.Context, caller portalreq.Caller, uri string) (ok bool, err error) {
	err = c.call(ctx, "ConfirmWallpaper", &ok, caller, uri)
	return ok, err
}

// ChooseApplication picks a handler; "" is a cancel.
func (c *Client) ChooseApplication(ctx context.Context, caller portalreq.Caller, choices []string, contentType, uri string) (id string, err error) {
	if choices == nil {
		choices = []string{}
	}
	err = c.call(ctx, "ChooseApplication", &id, caller, choices, contentType, uri)
	return id, err
}

// ConfirmInstall asks to install a dynamic launcher.
func (c *Client) ConfirmInstall(ctx context.Context, caller portalreq.Caller, name, iconName string) (ok bool, err error) {
	err = c.call(ctx, "ConfirmInstall", &ok, caller, name, iconName)
	return ok, err
}

// Dismiss takes down the dialog showing for the request at handle.
func (c *Client) Dismiss(ctx context.Context, handle string) error {
	return c.obj.CallWithContext(ctx, iface+".Dismiss", 0, handle).Err
}
