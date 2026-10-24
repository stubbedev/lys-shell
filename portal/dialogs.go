package portal

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/internal/useraccount"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// The interfaces whose whole job is one of the shell's native dialogs
// (dev.stubbe.lys.PortalDialogs1): Access, Account, AppChooser and
// DynamicLauncher. No xdg-desktop-portal-gtk.

// dialogs carries the D-Bus methods of all four; their names do not
// collide, and each interface exports only its own.
type dialogs struct{ conn *dbus.Conn }

func (d dialogs) client() *portaldialogs.Client { return portaldialogs.NewClient(d.conn) }

func dialogIfaces(conn *dbus.Conn, cfg *config.Service) []dbusx.Interface {
	d := dialogs{conn}
	// Of the four, only DynamicLauncher's spec has a version.
	launcher := dbusx.Getters{
		"version":                func() any { return uint32(1) },
		"SupportedLauncherTypes": func() any { return launcherApplication | launcherWebApp },
	}
	return []dbusx.Interface{
		{Name: "org.freedesktop.impl.portal.Access", Methods: accessMethods{d}},
		{Name: "org.freedesktop.impl.portal.Account", Methods: accountMethods{d}},
		{Name: "org.freedesktop.impl.portal.AppChooser", Methods: appChooserMethods{d}},
		{Name: "org.freedesktop.impl.portal.DynamicLauncher", Methods: launcherMethods{d, cfg}, Properties: launcher},
	}
}

// accessMethods is org.freedesktop.impl.portal.Access.
type accessMethods struct{ dialogs }

// AccessDialog shows a grant/deny prompt.
func (a accessMethods) AccessDialog(handle dbus.ObjectPath, appID, parent, title, subtitle, body string, options Vardict) (uint32, Vardict, *dbus.Error) {
	client := a.client()
	c, ok := beginOr(a.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	choices, err := optChoices(options)
	if err != nil {
		warnf("access: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	granted, picked, err := client.Access(c.ctx, c.caller, portaldialogs.AccessRequest{
		Title: title, Subtitle: subtitle, Body: body,
		GrantLabel: stringOr(options, "grant_label", "Allow"),
		DenyLabel:  stringOr(options, "deny_label", "Deny"),
		Icon:       stringOr(options, "icon", ""),
		Choices:    choices,
	})
	if code := c.response("access", granted, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	results := Vardict{}
	if len(choices) > 0 {
		results["choices"] = dbus.MakeVariant(picked)
	}
	return ResponseSuccess, results, nil
}

// accountMethods is org.freedesktop.impl.portal.Account.
type accountMethods struct{ dialogs }

// GetUserInformation offers the user's login name, real name and
// avatar for sharing; the user may edit the name and the picture
// first. The id is always the passwd login name.
func (a accountMethods) GetUserInformation(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	acct, err := useraccount.Current()
	if err != nil {
		warnf("account: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	client := a.client()
	c, ok := beginOr(a.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	shared, info, err := client.Account(c.ctx, c.caller, stringOr(options, "reason", ""), portaldialogs.AccountInfo{Name: acct.RealName, Image: acct.Avatar})
	if code := c.response("account", shared, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	results := Vardict{"id": dbus.MakeVariant(acct.Name), "name": dbus.MakeVariant(info.Name)}
	if info.Image != "" {
		results["image"] = dbus.MakeVariant(fileuri.FromPath(info.Image))
	}
	return ResponseSuccess, results, nil
}

// appChooserMethods is org.freedesktop.impl.portal.AppChooser.
type appChooserMethods struct{ dialogs }

// ChooseApplication lets the user pick the handler: the frontend's
// choices, preselecting its last choice, naming the file or URI opened.
// The shell cannot mint an activation token, so the one given is
// handed back for the chosen app.
func (a appChooserMethods) ChooseApplication(handle dbus.ObjectPath, appID, parent string, choices []string, options Vardict) (uint32, Vardict, *dbus.Error) {
	client := a.client()
	c, ok := beginOr(a.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	choice, err := client.ChooseApplication(c.ctx, c.caller, portaldialogs.ChooserRequest{
		Choices:     choices,
		LastChoice:  stringOr(options, "last_choice", ""),
		ContentType: stringOr(options, "content_type", ""),
		Target:      stringOr(options, "filename", stringOr(options, "uri", "")),
	})
	if code := c.response("appchooser", choice != "", err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	results := Vardict{"choice": dbus.MakeVariant(choice)}
	if token := stringOr(options, "activation_token", ""); token != "" {
		results["activation_token"] = dbus.MakeVariant(token)
	}
	return ResponseSuccess, results, nil
}

// UpdateChoices replaces the choices of the chooser open for handle;
// none open is NotFound.
func (a appChooserMethods) UpdateChoices(handle dbus.ObjectPath, choices []string) *dbus.Error {
	ctx, cancel := context.WithTimeout(context.Background(), shellCallTimeout)
	defer cancel()
	found, err := a.client().UpdateChoices(ctx, string(handle), choices)
	if err != nil {
		return dbus.MakeFailedError(fmt.Errorf("appchooser: update choices for %s: %w", handle, err))
	}
	if !found {
		return dbus.NewError(errNotFound, []any{"no app chooser open for " + string(handle)})
	}
	return nil
}

// launcherMethods is org.freedesktop.impl.portal.DynamicLauncher.
type launcherMethods struct {
	dialogs
	cfg *config.Service
}

// The launcher types, SupportedLauncherTypes' bits.
const (
	launcherApplication uint32 = 1
	launcherWebApp      uint32 = 2
)

// PrepareInstall confirms installing a launcher, showing its icon and,
// for a web app, the URL it opens; the user may edit the name unless
// editable_name is false. The icon goes back as it came (editing it is
// not supported); the frontend writes the desktop file.
func (l launcherMethods) PrepareInstall(handle dbus.ObjectPath, appID, parent, name string, icon dbus.Variant, options Vardict) (uint32, Vardict, *dbus.Error) {
	data, err := bytesIcon(icon)
	if err != nil {
		warnf("dynamiclauncher: %s's launcher %q: %v", appID, name, err)
		return ResponseOther, Vardict{}, nil
	}
	req := portaldialogs.LauncherRequest{Name: name, Icon: data, EditableName: true}
	if editable, ok := optBool(options, "editable_name"); ok {
		req.EditableName = editable
	}
	if kind, ok := optU32(options, "launcher_type"); ok && kind == launcherWebApp {
		req.Target = stringOr(options, "target", "")
	}
	client := l.client()
	c, ok := beginOr(l.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	c.modal(options)
	yes, chosen, err := client.ConfirmInstall(c.ctx, c.caller, req)
	if code := c.response("dynamiclauncher", yes, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	if !req.EditableName {
		chosen = name
	}
	return ResponseSuccess, Vardict{"name": dbus.MakeVariant(chosen), "icon": icon}, nil
}

// bytesIcon is the image of a ('bytes', <ay>) icon, the only kind the
// frontend passes.
func bytesIcon(icon dbus.Variant) ([]byte, error) {
	kind, inner, ok := serializedIcon(icon)
	if !ok || kind != "bytes" {
		return nil, fmt.Errorf("icon %v is not a serialized bytes icon", icon)
	}
	data, ok := inner.Value().([]byte)
	if !ok {
		return nil, errors.New("a bytes icon without bytes")
	}
	if len(data) > maxIconBytes {
		return nil, fmt.Errorf("icon larger than %d bytes", maxIconBytes)
	}
	return data, nil
}

// RequestInstallToken grants an install token, installing with no
// dialog, only to the apps portal.dynamic-launcher.install-token-apps
// names.
func (l launcherMethods) RequestInstallToken(appID string, _ Vardict) (uint32, *dbus.Error) {
	if appID != "" && slices.Contains(l.cfg.Config().Portal.DynamicLauncher.InstallTokenApps, appID) {
		return ResponseSuccess, nil
	}
	return ResponseOther, nil
}
