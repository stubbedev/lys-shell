package main

import (
	"os"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
	"github.com/stubbedev/lys-shell/shell/printdialog"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakeChooser answers every dialog with uris and records the request.
type fakeChooser struct {
	uris *dbustest.Var[[]string]
	got  *dbustest.Var[any]
}

func (f fakeChooser) Open(_ portalreq.Caller, r filechooser.OpenRequest) []string {
	f.got.Store(r)
	return f.uris.Load()
}

func (f fakeChooser) Save(_ portalreq.Caller, r filechooser.SaveRequest) []string {
	f.got.Store(r)
	return f.uris.Load()
}

func (fakeChooser) Dismiss(string) {}

// fakeDialogs answers yes (or with id) and records the last call.
type fakeDialogs struct {
	yes *dbustest.Var[bool]
	id  *dbustest.Var[string]
	got *dbustest.Var[[]any]
}

func (f fakeDialogs) Access(_ portalreq.Caller, r portaldialogs.AccessRequest) (bool, []portalreq.Selection) {
	f.got.Store([]any{r})
	return f.yes.Load(), nil
}

func (f fakeDialogs) Account(_ portalreq.Caller, reason string, info portaldialogs.AccountInfo) (bool, portaldialogs.AccountInfo) {
	f.got.Store([]any{reason})
	return f.yes.Load(), info
}

func (f fakeDialogs) ConfirmWallpaper(_ portalreq.Caller, uri string) bool {
	f.got.Store([]any{uri})
	return f.yes.Load()
}

func (f fakeDialogs) ChooseApplication(_ portalreq.Caller, r portaldialogs.ChooserRequest) string {
	f.got.Store([]any{r})
	return f.id.Load()
}

func (fakeDialogs) UpdateChoices(string, []string) bool { return false }

func (fakeDialogs) ChooseDevices(portalreq.Caller, []portaldialogs.Device) (bool, []string) {
	return false, nil
}

func (f fakeDialogs) ConfirmInstall(_ portalreq.Caller, r portaldialogs.LauncherRequest) (bool, string) {
	f.got.Store([]any{r})
	return f.yes.Load(), r.Name
}

func (fakeDialogs) Dismiss(string) {}

type fakePrinter struct{ granted *dbustest.Var[bool] }

func (f fakePrinter) Prepare(portalreq.Caller, printdialog.PrepareRequest) (bool, []printdialog.Setting, printdialog.PageSetup, uint32) {
	return f.granted.Load(), []printdialog.Setting{{Key: "copies", Value: "1"}}, printdialog.PageSetup{}, 7
}

func (fakePrinter) Print(portalreq.Caller, string, *os.File, uint32) bool { return false }

func (fakePrinter) Dismiss(string) {}

// fakeShot is dev.stubbe.lys.Screenshot1 answering fixed values; a
// preview takes the app's private capture, never the user's.
type fakeShot struct {
	path   string
	picked *dbustest.Var[bool]
}

func (f fakeShot) Capture(_ portalreq.Caller, mode, target string) (string, *dbus.Error) {
	return "user:" + mode, nil
}

func (f fakeShot) CapturePrivate(_ portalreq.Caller, mode, target string) (string, *dbus.Error) {
	return f.path + ":" + mode + ":" + target, nil
}

func (f fakeShot) PickColor(portalreq.Caller) (bool, float64, float64, float64, *dbus.Error) {
	return f.picked.Load(), 0.25, 0.5, 1, nil
}

func export(t *testing.T, export func(*dbus.Conn) (func(), error)) {
	t.Helper()
	if _, err := export(dbustest.SessionConn(t)); err != nil {
		t.Fatal(err)
	}
}

func show(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCaptured(t, false, append([]string{"portal", "show"}, args...)...)
}

func TestPortalShowFileChooser(t *testing.T) {
	dbustest.Session(t)
	if _, stderr, code := show(t, "file-chooser"); code != 1 || stderr != "Error: FileChooser service not running. Start lys-shell shell first.\n" {
		t.Errorf("no shell: code %d stderr %q", code, stderr)
	}
	got := &dbustest.Var[any]{}
	uris := &dbustest.Var[[]string]{}
	uris.Store([]string{"file:///a", "file:///b"})
	host := fakeChooser{uris: uris, got: got}
	export(t, filechooser.NewDaemon(host).Export)
	stdout, stderr, code := show(t, "file-chooser", "--multiple", "--directory")
	if code != 0 || stdout != "file:///a\nfile:///b\n" || stderr != "" {
		t.Errorf("open: code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if r, ok := got.Load().(filechooser.OpenRequest); !ok || r.Title != "Preview: Open File" || !r.Multiple || !r.Directory {
		t.Errorf("open request = %+v", got.Load())
	}
	uris.Store(nil)
	stdout, _, code = show(t, "file-chooser", "--save")
	if code != 0 || stdout != "cancelled\n" {
		t.Errorf("save cancel: code %d stdout %q", code, stdout)
	}
	if r, ok := got.Load().(filechooser.SaveRequest); !ok || r.CurrentName != "untitled.txt" {
		t.Errorf("save request = %+v", got.Load())
	}
}

func TestPortalShowDialogs(t *testing.T) {
	dbustest.Session(t)
	got := &dbustest.Var[[]any]{}
	yes, id := &dbustest.Var[bool]{}, &dbustest.Var[string]{}
	host := fakeDialogs{yes: yes, id: id, got: got}
	export(t, portaldialogs.NewDaemon(host).Export)
	for _, c := range []struct {
		args       []string
		yes, no    string
		checkInput func([]any) bool
	}{
		{[]string{"access"}, "granted\n", "denied\n", func(a []any) bool {
			r, _ := a[0].(portaldialogs.AccessRequest)
			return r.Title == "Preview: Access" && r.GrantLabel == "Allow" && r.Icon == "dialog-password-symbolic"
		}},
		{[]string{"account"}, "shared\n", "declined\n", func(a []any) bool {
			return strings.Contains(a[0].(string), "account-sharing")
		}},
		{[]string{"dynamic-launcher"}, "approved\n", "rejected\n", func(a []any) bool {
			return a[0].(portaldialogs.LauncherRequest).Name == "Preview Launcher"
		}},
		{[]string{"wallpaper", "--uri", "file:///w.png"}, "accepted\n", "declined\n", func(a []any) bool {
			return a[0] == "file:///w.png"
		}},
		{[]string{"app-chooser"}, "org.example.Editor\n", "cancelled\n", func(a []any) bool {
			return a[0].(portaldialogs.ChooserRequest).ContentType == "text/plain"
		}},
	} {
		yes.Store(true)
		id.Store("org.example.Editor")
		stdout, stderr, code := show(t, c.args...)
		if code != 0 || stdout != c.yes || stderr != "" {
			t.Errorf("%v yes: code %d stdout %q stderr %q", c.args, code, stdout, stderr)
		}
		if !c.checkInput(got.Load()) {
			t.Errorf("%v sent %v", c.args, got.Load())
		}
		yes.Store(false)
		id.Store("")
		if stdout, _, _ := show(t, c.args...); stdout != c.no {
			t.Errorf("%v no: stdout %q, want %q", c.args, stdout, c.no)
		}
	}
}

func TestPortalShowPrintScreenCastAndScreenshot(t *testing.T) {
	dbustest.Session(t)
	granted := &dbustest.Var[bool]{}
	granted.Store(true)
	printer := fakePrinter{granted: granted}
	export(t, printdialog.NewDaemon(printer).Export)
	if stdout, _, code := show(t, "print"); code != 0 || stdout != "prepared (token 7, 1 settings)\n" {
		t.Errorf("print: code %d stdout %q", code, stdout)
	}
	granted.Store(false)
	if stdout, _, _ := show(t, "print"); stdout != "cancelled\n" {
		t.Errorf("print cancel: stdout %q", stdout)
	}

	got := servePicker(t, "monitor:DP-1")
	if stdout, _, code := show(t, "screen-cast", "--allow-token", "--multiple"); code != 0 || stdout != "monitor:DP-1\n" {
		t.Errorf("screen-cast: code %d stdout %q", code, stdout)
	}
	if args := got.Load(); len(args) != 1 || args[0] != (sharepicker.Request{Sources: sharepicker.AllSources, AllowToken: true, Multiple: true}) {
		t.Errorf("Pick args = %v, want no window list, every source and both flags", args)
	}

	conn := dbustest.SessionConn(t)
	picked := &dbustest.Var[bool]{}
	picked.Store(true)
	if err := conn.Export(fakeShot{"/tmp/shot.png", picked}, screenshot.ServicePath, screenshot.ServiceName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(screenshot.ServiceName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if stdout, _, code := show(t, "screenshot", "--mode", "output", "--target", "DP-2"); code != 0 || stdout != "/tmp/shot.png:output:DP-2\n" {
		t.Errorf("screenshot: code %d stdout %q", code, stdout)
	}
	if stdout, _, code := show(t, "color"); code != 0 || stdout != "rgb(0.250, 0.500, 1.000)\n" {
		t.Errorf("color: code %d stdout %q", code, stdout)
	}
	picked.Store(false)
	if stdout, _, code := show(t, "color"); code != 0 || stdout != "cancelled\n" {
		t.Errorf("color cancel: code %d stdout %q", code, stdout)
	}
}
