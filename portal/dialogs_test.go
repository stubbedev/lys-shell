package portal

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

const (
	accessDialog = "org.freedesktop.impl.portal.Access.AccessDialog"
	userInfoCall = "org.freedesktop.impl.portal.Account.GetUserInformation"
	chooseApp    = "org.freedesktop.impl.portal.AppChooser.ChooseApplication"
	prepInstall  = "org.freedesktop.impl.portal.DynamicLauncher.PrepareInstall"
)

// eachDialog calls every dialog-backed method once.
func (r *rig) eachDialog(t *testing.T) map[string]uint32 {
	t.Helper()
	codes := map[string]uint32{}
	codes[accessDialog], _ = r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{})
	codes[userInfoCall], _ = r.interactive(t, userInfoCall, handle, "org.app", "", Vardict{})
	codes[chooseApp], _ = r.interactive(t, chooseApp, handle, "org.app", "", []string{"a.desktop"}, Vardict{})
	codes[prepInstall], _ = r.interactive(t, prepInstall, handle, "org.app", "", "App", dbus.MakeVariant(gicon{"bytes", dbus.MakeVariant([]byte{1})}), Vardict{})
	return codes
}

func TestDialogsAnswerFromTheShell(t *testing.T) {
	r := newRig(t, nil)
	// No shell: every dialog fails as other, never a silent grant.
	for method, code := range r.eachDialog(t) {
		if code != ResponseOther {
			t.Errorf("%s without a shell = %d", method, code)
		}
	}
	d := serveDialogs(t, r, false)
	for method, code := range r.eachDialog(t) {
		if code != ResponseCancelled {
			t.Errorf("%s declined = %d", method, code)
		}
	}
	d.yes.Store(true)
	for method, code := range r.eachDialog(t) {
		if code != ResponseSuccess {
			t.Errorf("%s accepted = %d", method, code)
		}
	}
}

func TestAccessDialogLabels(t *testing.T) {
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{})
	if got := d.asked.Load()[0]; !reflect.DeepEqual(got, portaldialogs.AccessRequest{Title: "T", Subtitle: "S", Body: "B", GrantLabel: "Allow", DenyLabel: "Deny", Choices: []portalreq.Choice{}}) {
		t.Errorf("defaults = %+v", got)
	}
	r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{
		"grant_label": dbus.MakeVariant("Share"), "deny_label": dbus.MakeVariant("Keep"), "icon": dbus.MakeVariant("camera-web"),
	})
	if got := d.asked.Load()[0].(portaldialogs.AccessRequest); got.GrantLabel != "Share" || got.DenyLabel != "Keep" || got.Icon != "camera-web" {
		t.Errorf("labels = %+v", got)
	}
}

// Choices reach the dialog and come back with the grant; a deny returns
// none, and malformed choices fail the call.
func TestAccessDialogChoicesRoundTrip(t *testing.T) {
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	choices := []portalreq.Choice{
		{ID: "remember", Label: "Remember", Options: []portalreq.Option{}, Initial: "true"},
		{ID: "scope", Label: "Scope", Options: []portalreq.Option{{ID: "once", Label: "Once"}, {ID: "always", Label: "Always"}}, Initial: "always"},
	}
	code, res := r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{"choices": dbus.MakeVariant(choices)})
	if got := d.asked.Load()[0].(portaldialogs.AccessRequest); !reflect.DeepEqual(got.Choices, choices) {
		t.Errorf("the dialog got choices %+v", got.Choices)
	}
	var picked []portalreq.Selection
	if err := dbus.Store([]any{res["choices"].Value()}, &picked); err != nil {
		t.Fatalf("choices result %v: %v", res, err)
	}
	want := []portalreq.Selection{{ID: "remember", Value: "true"}, {ID: "scope", Value: "always"}}
	if code != ResponseSuccess || !reflect.DeepEqual(picked, want) {
		t.Errorf("granted %d with %+v, want %+v", code, picked, want)
	}
	d.yes.Store(false)
	if code, res := r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{"choices": dbus.MakeVariant(choices)}); code != ResponseCancelled || len(res) != 0 {
		t.Errorf("denied = %d %v, want no choices", code, res)
	}
	bad := []portalreq.Choice{{ID: "", Label: "No id"}}
	if code, _ := r.interactive(t, accessDialog, handle, "org.app", "", "T", "S", "B", Vardict{"choices": dbus.MakeVariant(bad)}); code != ResponseOther {
		t.Errorf("a choice without an id = %d, want other", code)
	}
}

// The account offered is the passwd entry's, whatever $USER says; what
// is shared is what the user left after editing, with the picture as a
// file:// URI, and none when they removed it.
func TestAccountSharesTheUser(t *testing.T) {
	want, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Skipf("uid not in the passwd file: %v", err)
	}
	t.Setenv("USER", "mallory")
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	_, res := r.interactive(t, userInfoCall, handle, "org.app", "", Vardict{"reason": dbus.MakeVariant("to greet you")})
	asked := d.asked.Load()
	if asked[0] != "to greet you" {
		t.Errorf("reason = %v", asked)
	}
	if offered := asked[1].(portaldialogs.AccountInfo); offered.Name == "" {
		t.Errorf("offered %+v, want the real name", offered)
	}
	if res["id"].Value() != want.Username {
		t.Errorf("id = %v, want the passwd login %s", res["id"], want.Username)
	}
	d.edit.Store(&portaldialogs.AccountInfo{Name: "Ada", Image: "/pics/a b.png"})
	_, res = r.interactive(t, userInfoCall, handle, "org.app", "", Vardict{})
	if res["name"].Value() != "Ada" || res["image"].Value() != "file:///pics/a%20b.png" {
		t.Errorf("edited results = %v", res)
	}
	d.edit.Store(&portaldialogs.AccountInfo{Name: "Ada"})
	if _, res = r.interactive(t, userInfoCall, handle, "org.app", "", Vardict{}); res["image"] != (dbus.Variant{}) {
		t.Errorf("a removed picture was shared: %v", res["image"])
	}
}

func TestAppChooserAndLauncherResults(t *testing.T) {
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	_, res := r.interactive(t, chooseApp, handle, "org.app", "", []string{"org.a.App", "org.b.App"}, Vardict{
		"content_type": dbus.MakeVariant("image/png"), "uri": dbus.MakeVariant("file:///x.png"),
		"last_choice": dbus.MakeVariant("org.b.App"),
	})
	want := portaldialogs.ChooserRequest{Choices: []string{"org.a.App", "org.b.App"}, LastChoice: "org.b.App", ContentType: "image/png", Target: "file:///x.png"}
	if asked := d.asked.Load(); res["choice"].Value() != "org.chosen.App" || !reflect.DeepEqual(asked[0], want) {
		t.Errorf("choice = %v, asked %+v, want %+v", res, asked, want)
	}
	if _, ok := res["activation_token"]; ok {
		t.Errorf("an activation token with none given: %v", res)
	}
	// The basename names the file over its URI; the token given comes
	// back with the choice.
	_, res = r.interactive(t, chooseApp, handle, "org.app", "", []string{}, Vardict{
		"uri": dbus.MakeVariant("file:///d/x.png"), "filename": dbus.MakeVariant("x.png"),
		"activation_token": dbus.MakeVariant("tok"),
	})
	if got := d.asked.Load()[0].(portaldialogs.ChooserRequest); got.Target != "x.png" || res["activation_token"].Value() != "tok" {
		t.Errorf("target %q, results %v", got.Target, res)
	}
	d.yes.Store(false)
	if _, res = r.interactive(t, chooseApp, handle, "org.app", "", []string{}, Vardict{"activation_token": dbus.MakeVariant("tok")}); len(res) != 0 {
		t.Errorf("a cancel returned %v", res)
	}
	d.yes.Store(true)

	update := "org.freedesktop.impl.portal.AppChooser.UpdateChoices"
	if err := r.obj.Call(update, 0, handle, []string{"org.c.App"}).Err; err != nil || !reflect.DeepEqual(d.updated.Load(), []any{string(handle), []string{"org.c.App"}}) {
		t.Errorf("UpdateChoices = %v, the shell got %v", err, d.updated.Load())
	}
	var dErr dbus.Error
	if err := r.obj.Call(update, 0, dbus.ObjectPath("/no/such/request"), []string{"org.c.App"}).Err; !errors.As(err, &dErr) || dErr.Name != errNotFound {
		t.Errorf("UpdateChoices without a chooser = %v, want %s", err, errNotFound)
	}

	v, err := r.obj.GetProperty("org.freedesktop.impl.portal.DynamicLauncher.SupportedLauncherTypes")
	if err != nil || v.Value() != uint32(3) {
		t.Errorf("SupportedLauncherTypes = %v, %v", v, err)
	}
}

// The dialog gets the icon's bytes, the web app's URL and whether the
// name is editable; the name chosen comes back (the default when it
// was not editable) with the icon as it came.
func TestPrepareInstall(t *testing.T) {
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	icon := dbus.MakeVariant(gicon{"bytes", dbus.MakeVariant([]byte{1, 2})})
	_, res := r.interactive(t, prepInstall, handle, "org.app", "", "My App", icon, Vardict{})
	// The icon goes back as it came, a (sv) GIcon: the call surviving at
	// all is the wire check (a struct re-encoded as an array is a
	// malformed reply the bus disconnects the backend for).
	if got, ok := res["icon"].Value().([]any); res["name"].Value() != "Renamed" || !ok || len(got) != 2 || got[0] != "bytes" || !slices.Equal(got[1].(dbus.Variant).Value().([]byte), []byte{1, 2}) {
		t.Errorf("launcher results = %v", res)
	}
	if got := d.asked.Load()[0]; !reflect.DeepEqual(got, portaldialogs.LauncherRequest{Name: "My App", Icon: []byte{1, 2}, EditableName: true}) {
		t.Errorf("ConfirmInstall asked %+v", got)
	}
	web := Vardict{"launcher_type": dbus.MakeVariant(launcherWebApp), "target": dbus.MakeVariant("https://example.org"), "editable_name": dbus.MakeVariant(false)}
	if _, res = r.interactive(t, prepInstall, handle, "org.app", "", "My App", icon, web); res["name"].Value() != "My App" {
		t.Errorf("a fixed name came back as %v", res["name"])
	}
	if got := d.asked.Load()[0].(portaldialogs.LauncherRequest); got.Target != "https://example.org" || got.EditableName {
		t.Errorf("web app asked %+v", got)
	}
	app := Vardict{"launcher_type": dbus.MakeVariant(launcherApplication), "target": dbus.MakeVariant("https://example.org")}
	if r.interactive(t, prepInstall, handle, "org.app", "", "My App", icon, app); d.asked.Load()[0].(portaldialogs.LauncherRequest).Target != "" {
		t.Errorf("an application showed a target: %+v", d.asked.Load()[0])
	}
	d.asked.Store(nil)
	themed := dbus.MakeVariant(gicon{"themed", dbus.MakeVariant([]string{"app"})})
	if code, _ := r.interactive(t, prepInstall, handle, "org.app", "", "My App", themed, Vardict{}); code != ResponseOther || d.asked.Load() != nil {
		t.Errorf("a themed icon = %d, asked %v; want other, unasked", code, d.asked.Load())
	}
}

// Only the configured apps get an install token; every other app asks.
func TestRequestInstallTokenFollowsTheAllowlist(t *testing.T) {
	r := newRig(t, nil)
	token := func(appID string) uint32 {
		t.Helper()
		var code uint32
		if err := r.obj.Call("org.freedesktop.impl.portal.DynamicLauncher.RequestInstallToken", 0, appID, Vardict{}).Store(&code); err != nil {
			t.Fatal(err)
		}
		return code
	}
	if code := token("org.app"); code == ResponseSuccess {
		t.Error("an app got an install token by default")
	}
	if err := r.cfg.SetByPath("portal.dynamic-launcher.install-token-apps", []any{"org.gnome.Software"}); err != nil {
		t.Fatal(err)
	}
	if code := token("org.gnome.Software"); code != ResponseSuccess {
		t.Errorf("an allowlisted app = %d", code)
	}
	for _, id := range []string{"org.app", ""} {
		if code := token(id); code == ResponseSuccess {
			t.Errorf("%q got an install token", id)
		}
	}
}

// The consent dialogs' icons are glyphs the shell installs, so they
// draw on any icon theme.
func TestConsentIconsAreBundled(t *testing.T) {
	for _, name := range []string{screenshotIcon, remoteDesktopIcon, shortcutsIcon} {
		if _, err := os.Stat(filepath.Join("..", "resources", "icons", "hicolor", "scalable", "actions", name+".svg")); err != nil {
			t.Errorf("consent icon %q is not bundled: %v", name, err)
		}
	}
}
