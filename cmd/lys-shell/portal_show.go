package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/dbuscli"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/printdialog"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// The `lys-shell portal show <dialog>` previews (cli/portal/show.rs): each
// calls the shell service the real backend delegates to, with
// placeholder arguments, and prints the answer — `cancelled` for an
// empty one. The calls wait on the user, so they carry no timeout.

// showDialog connects to the session bus around one preview; show
// returns the lines to print, or the service's label and operation
// for the error text.
func showDialog(show func(ctx context.Context, conn *dbus.Conn) (lines []string, service, op string, err error)) func(*cli.Matches) error {
	return func(m *cli.Matches) error {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return cliMessage("Failed to connect to D-Bus session bus: " + err.Error())
		}
		defer func() { _ = conn.Close() }()
		lines, service, op, err := show(context.Background(), conn)
		if err != nil {
			return dbuscli.FormatError(service, op, err)
		}
		for _, l := range lines {
			_, _ = fmt.Fprintln(m.Stdout(), l)
		}
		return nil
	}
}

// orCancelled prints a result, or `cancelled` for none.
func orCancelled(results ...string) []string {
	var out []string
	for _, r := range results {
		if r != "" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return []string{"cancelled"}
	}
	return out
}

// verdict prints one of two words.
func verdict(ok bool, yes, no string) []string {
	if ok {
		return []string{yes}
	}
	return []string{no}
}

func showFileChooser(m *cli.Matches) error {
	return showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
		c := filechooser.NewClient(conn)
		var res filechooser.Result
		var err error
		if m.Flag("save") {
			res, err = c.Save(ctx, portaldialogs.Caller{}, filechooser.SaveRequest{Title: "Preview: Save File", CurrentName: "untitled.txt"})
		} else {
			res, err = c.Open(ctx, portaldialogs.Caller{}, filechooser.OpenRequest{Title: "Preview: Open File", Multiple: m.Flag("multiple"), Directory: m.Flag("directory")})
		}
		return orCancelled(res.URIs...), "FileChooser", "show file chooser", err
	})(m)
}

func showScreenshot(m *cli.Matches) error {
	mode, _ := cli.Value[string](m, "mode")
	target, _ := cli.Value[string](m, "target")
	return showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
		path, err := screenshot.NewClient(conn).CapturePrivate(ctx, portaldialogs.Caller{}, mode, target, false)
		return orCancelled(path), "Screenshot", "capture screenshot", err
	})(m)
}

var showColor = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	c, picked, err := screenshot.NewClient(conn).PickColor(ctx, portaldialogs.Caller{})
	if !picked {
		return []string{"cancelled"}, "Screenshot", "pick color", err
	}
	return []string{fmt.Sprintf("rgb(%.3f, %.3f, %.3f)", c[0], c[1], c[2])}, "Screenshot", "pick color", err
})

func showScreenCast(m *cli.Matches) error {
	return showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
		// No window list: the picker with no pre-seeded sources.
		selection, err := sharepicker.NewClient(conn).Pick(ctx, portaldialogs.Caller{}, sharepicker.Request{Sources: sharepicker.AllSources, AllowToken: m.Flag("allow_token"), Multiple: m.Flag("multiple")})
		return orCancelled(selection), "SharePicker", "show share picker", err
	})(m)
}

var showPrint = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	granted, settings, _, token, err := printdialog.NewClient(conn).Prepare(ctx, portaldialogs.Caller{}, printdialog.PrepareRequest{Title: "Preview: Print"})
	if !granted {
		return []string{"cancelled"}, "Print", "show print dialog", err
	}
	return []string{fmt.Sprintf("prepared (token %d, %d settings)", token, len(settings))}, "Print", "show print dialog", err
})

var showAccess = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	granted, _, err := portaldialogs.NewClient(conn).Access(ctx, portaldialogs.Caller{}, portaldialogs.AccessRequest{
		Title:      "Preview: Access",
		Subtitle:   "An application is requesting access",
		Body:       "This is a preview of the generic access prompt.",
		GrantLabel: "Allow",
		DenyLabel:  "Deny",
		Icon:       "ld-key-round-symbolic",
	})
	return verdict(granted, "granted", "denied"), "Access", "show access prompt", err
})

var showAccount = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	shared, _, err := portaldialogs.NewClient(conn).Account(ctx, portaldialogs.Caller{}, "This is a preview of the account-sharing consent prompt.",
		portaldialogs.AccountInfo{Name: "Preview User"})
	return verdict(shared, "shared", "declined"), "Account", "show account prompt", err
})

var showAppChooser = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	id, err := portaldialogs.NewClient(conn).ChooseApplication(ctx, portaldialogs.Caller{}, portaldialogs.ChooserRequest{ContentType: "text/plain"})
	return orCancelled(id), "AppChooser", "show app chooser", err
})

var showDynamicLauncher = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	ok, _, err := portaldialogs.NewClient(conn).ConfirmInstall(ctx, portaldialogs.Caller{}, portaldialogs.LauncherRequest{Name: "Preview Launcher", EditableName: true})
	return verdict(ok, "approved", "rejected"), "DynamicLauncher", "show install confirmation", err
})

func showWallpaper(m *cli.Matches) error {
	uri, _ := cli.Value[string](m, "uri")
	return showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
		ok, err := portaldialogs.NewClient(conn).ConfirmWallpaper(ctx, portaldialogs.Caller{}, uri)
		return verdict(ok, "accepted", "declined"), "Wallpaper", "show wallpaper preview", err
	})(m)
}

// previewAuthHandle is the request the authentication preview runs
// under, dismissed once answered.
const previewAuthHandle = "/dev/stubbe/lys/preview/authenticate"

// previewAuth is one prompt of an authentication as lys-polkit asks it,
// with two identities to pick from. The answer is never printed.
var previewAuth = portaldialogs.AuthRequest{
	Message:    "Preview: authentication is needed to run a program as the super user",
	Icon:       "ld-shield-check-symbolic",
	Details:    []portaldialogs.AuthDetail{{Label: "Program", Value: "/usr/bin/true"}},
	Identities: []portaldialogs.AuthIdentity{{UID: 0, Name: "root"}, {UID: 1000, Name: "Preview User"}},
	Selected:   0,
	Notes:      []portaldialogs.AuthNote{{Text: "This is a preview; nothing is authenticated."}},
	Prompt:     "Password:",
}

var showAuthenticate = showDialog(func(ctx context.Context, conn *dbus.Conn) ([]string, string, string, error) {
	c := portaldialogs.NewClient(conn)
	reply, err := c.Authenticate(ctx, portaldialogs.Caller{Handle: previewAuthHandle}, previewAuth)
	err = errors.Join(err, c.Dismiss(ctx, previewAuthHandle))
	var line string
	switch reply := reply.(type) {
	case portaldialogs.AuthAnswer:
		line = "answered"
	case portaldialogs.AuthSwitch:
		line = fmt.Sprintf("switched to uid %d", reply.UID)
	default:
		line = "cancelled"
	}
	return []string{line}, "Authenticate", "show authentication dialog", err
})
