package portal

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"

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

func dialogIfaces(conn *dbus.Conn) []dbusx.Interface {
	version := func(v uint32) dbusx.Getters { return dbusx.Getters{"version": func() any { return v }} }
	d := dialogs{conn}
	launcher := version(1)
	// Applications and web apps.
	launcher["SupportedLauncherTypes"] = func() any { return uint32(1 | 2) }
	return []dbusx.Interface{
		{Name: "org.freedesktop.impl.portal.Access", Methods: accessMethods{d}, Properties: version(1)},
		{Name: "org.freedesktop.impl.portal.Account", Methods: accountMethods{d}, Properties: version(1)},
		{Name: "org.freedesktop.impl.portal.AppChooser", Methods: appChooserMethods{d}, Properties: version(2)},
		{Name: "org.freedesktop.impl.portal.DynamicLauncher", Methods: launcherMethods{d}, Properties: launcher},
	}
}

// accessMethods is org.freedesktop.impl.portal.Access (access.rs).
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

// accountMethods is org.freedesktop.impl.portal.Account (account.rs).
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

// appChooserMethods is org.freedesktop.impl.portal.AppChooser
// (appchooser.rs).
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

// launcherMethods is org.freedesktop.impl.portal.DynamicLauncher
// (dynamiclauncher.rs).
type launcherMethods struct{ dialogs }

// PrepareInstall confirms installing a launcher and echoes its name and
// icon back; the frontend writes the desktop file.
func (l launcherMethods) PrepareInstall(handle dbus.ObjectPath, appID, parent, name string, icon dbus.Variant, _ Vardict) (uint32, Vardict, *dbus.Error) {
	client := l.client()
	c, ok := beginOr(l.conn, handle, appID, parent, client.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	yes, err := client.ConfirmInstall(c.ctx, c.caller, name, "")
	if code := c.response("dynamiclauncher", yes, err); code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"name": dbus.MakeVariant(name), "icon": icon}, nil
}

// RequestInstallToken grants the token the frontend mints.
func (launcherMethods) RequestInstallToken(_ string, _ Vardict) (uint32, *dbus.Error) {
	return ResponseSuccess, nil
}
