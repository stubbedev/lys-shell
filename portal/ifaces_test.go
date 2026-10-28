package portal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-secrets/client"
	"github.com/stubbedev/lys-secrets/secretstest"
)

// interactive calls a (u, a{sv}) method.
func (r *rig) interactive(t *testing.T, method string, args ...any) (uint32, map[string]dbus.Variant) {
	t.Helper()
	var code uint32
	var results map[string]dbus.Variant
	if err := r.obj.Call(method, 0, args...).Store(&code, &results); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return code, results
}

// spawned records the programs the backend hands off to and the
// environment they get; fail makes the named program fail to start.
type spawned struct {
	argv dbustest.Var[[][]string]
	env  dbustest.Var[[][]string]
	fail map[string]bool
}

func (s *spawned) spawn(argv, env []string) error {
	s.argv.Update(func(all [][]string) [][]string { return append(all, argv) })
	s.env.Update(func(all [][]string) [][]string { return append(all, env) })
	if s.fail[argv[0]] {
		return os.ErrNotExist
	}
	return nil
}

func spawnRig(t *testing.T, fail ...string) (*rig, *spawned) {
	s := &spawned{fail: map[string]bool{}}
	for _, f := range fail {
		s.fail[f] = true
	}
	return newRig(t, func(b *Backend) { b.spawn = s.spawn }), s
}

const handle = dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_2/t")

const acquireDevices = "org.freedesktop.impl.portal.Usb.AcquireDevices"

// usbRequest is a writable pad the database names and a read-only key
// known only by its id.
var usbRequest = []requestedDevice{
	{
		ID: "dev0",
		Info: Vardict{"properties": dbus.MakeVariant(map[string]dbus.Variant{
			"ID_VENDOR_FROM_DATABASE": dbus.MakeVariant("Acme"), "ID_MODEL_ENC": dbus.MakeVariant(`Game\x20Pad`),
			"ID_VENDOR_ID": dbus.MakeVariant("1234"), "ID_MODEL_ID": dbus.MakeVariant("abcd"),
		})},
		Access: Vardict{"writable": dbus.MakeVariant(true)},
	},
	{ID: "dev1", Info: Vardict{}, Access: Vardict{}},
}

// grants reads AcquireDevices' devices result as id=writable.
func grants(t *testing.T, res Vardict) []string {
	t.Helper()
	var granted []grantedDevice
	if err := dbus.Store([]any{res["devices"].Value()}, &granted); err != nil {
		t.Fatalf("devices result %v: %v", res, err)
	}
	out := []string{}
	for _, g := range granted {
		out = append(out, fmt.Sprintf("%s=%v", g.ID, g.Access["writable"].Value()))
	}
	return out
}

// Nothing is granted without the user's answer: no shell is other, a
// denial is cancelled, and only the devices picked are granted, with
// the access asked for. The dialog names the app (the third argument,
// after the parent window) and each device.
func TestUsbGrantsOnlyWhatTheUserPicks(t *testing.T) {
	r := newRig(t, nil)
	if code, res := r.interactive(t, acquireDevices, handle, "wayland:x", "org.app", usbRequest, Vardict{}); code != ResponseOther || len(res) != 0 {
		t.Errorf("without a shell = %d %v, want other and no grant", code, res)
	}
	d := serveDialogs(t, r, true)
	code, res := r.interactive(t, acquireDevices, handle, "wayland:x", "org.app", usbRequest, Vardict{})
	if got := grants(t, res); code != ResponseSuccess || !slices.Equal(got, []string{"dev0=true", "dev1=false"}) {
		t.Errorf("all picked = %d %v", code, got)
	}
	want := []portaldialogs.Device{{ID: "dev0", Name: "Acme Game Pad (1234:abcd)", Writable: true}, {ID: "dev1", Name: "dev1"}}
	if asked := d.asked.Load()[0]; !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %+v, want %+v", asked, want)
	}
	if callers := d.callers.Load(); callers[len(callers)-1].AppID != "org.app" || callers[len(callers)-1].ParentWindow != "wayland:x" {
		t.Errorf("caller %+v", callers[len(callers)-1])
	}
	d.picks.Store(&[]string{"dev1"})
	if _, res := r.interactive(t, acquireDevices, handle, "", "org.app", usbRequest, Vardict{}); !slices.Equal(grants(t, res), []string{"dev1=false"}) {
		t.Errorf("one picked = %v", grants(t, res))
	}
	d.picks.Store(&[]string{})
	if code, res := r.interactive(t, acquireDevices, handle, "", "org.app", usbRequest, Vardict{}); code != ResponseSuccess || len(grants(t, res)) != 0 {
		t.Errorf("none picked = %d %v, want success with no devices", code, grants(t, res))
	}
	d.picks.Store(&[]string{"dev9"})
	if code, res := r.interactive(t, acquireDevices, handle, "", "org.app", usbRequest, Vardict{}); code != ResponseOther || len(res) != 0 {
		t.Errorf("a device never requested = %d %v, want other", code, res)
	}
	d.yes.Store(false)
	if code, res := r.interactive(t, acquireDevices, handle, "", "org.app", usbRequest, Vardict{}); code != ResponseCancelled || len(res) != 0 {
		t.Errorf("denied = %d %v, want cancelled", code, res)
	}
}

func TestUsbDeviceName(t *testing.T) {
	props := func(kv ...string) Vardict {
		m := map[string]dbus.Variant{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = dbus.MakeVariant(kv[i+1])
		}
		return Vardict{"properties": dbus.MakeVariant(m)}
	}
	for _, tc := range []struct {
		info Vardict
		want string
	}{
		{props("ID_VENDOR_ENC", `Acme\x20Inc\x20\x20`, "ID_MODEL_FROM_DATABASE", "Pad", "ID_VENDOR_ID", "1234", "ID_MODEL_ID", "abcd"), "Acme Inc Pad (1234:abcd)"},
		{props("ID_VENDOR_ID", "1234", "ID_MODEL_ID", "abcd"), "USB device 1234:abcd"},
		{props("ID_MODEL_ENC", `a\xZZb\`), `a\xZZb\`},
		{Vardict{}, "dev7"},
	} {
		if got := usbDeviceName("dev7", tc.info); got != tc.want {
			t.Errorf("usbDeviceName(%v) = %q, want %q", tc.info, got, tc.want)
		}
	}
}

const emailIfaceName = "org.freedesktop.impl.portal.Email.ComposeEmail"

func TestEmailOpensAMailtoURI(t *testing.T) {
	r, s := spawnRig(t)
	opts := Vardict{
		"addresses": dbus.MakeVariant([]string{"x@y.com", "z@w.com"}),
		"address":   dbus.MakeVariant("a@b.com"),
		"cc":        dbus.MakeVariant([]string{"c@d.com"}),
		"subject":   dbus.MakeVariant("Hi there"),
		"body":      dbus.MakeVariant("Line & stuff~"),
	}
	if code, _ := r.interactive(t, emailIfaceName, handle, "org.app", "", opts); code != ResponseSuccess {
		t.Errorf("code = %d", code)
	}
	want := []string{"xdg-open", "mailto:x@y.com,z@w.com,a@b.com?cc=c%40d.com&subject=Hi%20there&body=Line%20%26%20stuff~"}
	if got := s.argv.Load(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("spawned %q, want %q", got, want)
	}
	if mailto(Vardict{}) != "mailto:" {
		t.Errorf("empty mailto = %q", mailto(Vardict{}))
	}
}

// The frontend hands the backend the attachments as a list of paths
// (it resolves the app's attachment_fds itself and filters them out),
// which reach xdg-email as --attach arguments.
func TestEmailAttachesThroughXdgEmail(t *testing.T) {
	opts := Vardict{
		"address":     dbus.MakeVariant("a@b.com"),
		"subject":     dbus.MakeVariant("S"),
		"bcc":         dbus.MakeVariant([]string{"h@i.com"}),
		"attachments": dbus.MakeVariant([]string{"/doc/a.txt", "file:///doc/b%20c.pdf"}),
	}
	r, s := spawnRig(t)
	if code, _ := r.interactive(t, emailIfaceName, handle, "org.app", "", opts); code != ResponseSuccess {
		t.Errorf("code = %d", code)
	}
	got := s.argv.Load()
	want := []string{"xdg-email", "--utf8", "--subject", "S", "--bcc", "h@i.com", "--attach", "/doc/a.txt", "--attach", "/doc/b c.pdf", "a@b.com"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("spawned %q, want %q", got, want)
	}

	// No xdg-email: a failure, not a compose window without the files.
	r, s = spawnRig(t, "xdg-email")
	if code, _ := r.interactive(t, emailIfaceName, handle, "org.app", "", opts); code != ResponseOther {
		t.Errorf("xdg-email missing: code %d, want other", code)
	}
	if got := s.argv.Load(); len(got) != 1 {
		t.Errorf("a fallback ran after xdg-email failed: %q", got)
	}
}

// attachment_fds never reaches a backend through the frontend; on its
// own it attaches nothing and writes no copies anywhere.
func TestEmailIgnoresAttachmentFds(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, s := spawnRig(t)
	opts := Vardict{
		"address":        dbus.MakeVariant("a@b.com"),
		"attachment_fds": dbus.MakeVariant([]dbus.UnixFD{dbus.UnixFD(f.Fd())}),
	}
	r.interactive(t, emailIfaceName, handle, "org.app", "", opts)
	if got := s.argv.Load(); len(got) != 1 || !slices.Equal(got[0], []string{"xdg-open", "mailto:a@b.com"}) {
		t.Errorf("spawned %q, want the plain mailto", got)
	}
}

// The activation token reaches the mail client as XDG_ACTIVATION_TOKEN,
// so it can raise its window; without one nothing is added.
func TestEmailPassesTheActivationToken(t *testing.T) {
	r, s := spawnRig(t)
	r.interactive(t, emailIfaceName, handle, "org.app", "", Vardict{"activation_token": dbus.MakeVariant("tok-1")})
	r.interactive(t, emailIfaceName, handle, "org.app", "", Vardict{})
	if got := s.env.Load(); len(got) != 2 || !slices.Equal(got[0], []string{"XDG_ACTIVATION_TOKEN=tok-1"}) || len(got[1]) != 0 {
		t.Errorf("env = %q", got)
	}
}

func TestEmailFailsWithoutAHandler(t *testing.T) {
	r, _ := spawnRig(t, "xdg-open")
	if code, _ := r.interactive(t, emailIfaceName, handle, "org.app", "", Vardict{}); code != ResponseOther {
		t.Errorf("code = %d, want other", code)
	}
}

// retrieveSecret calls RetrieveSecret and returns what it wrote.
func (r *rig) retrieveSecret(t *testing.T, appID string) (uint32, []byte) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	code, _ := r.interactive(t, "org.freedesktop.impl.portal.Secret.RetrieveSecret", handle, appID, dbus.UnixFD(pw.Fd()), Vardict{})
	_ = pw.Close()
	got, _ := io.ReadAll(pr)
	return code, got
}

// withKeyring serves a Secret Service on the rig's bus whose login
// collection is unlocked with "pw".
func withKeyring(t *testing.T, r *rig) *secretstest.Server {
	t.Helper()
	return secretstest.Start(t, r.bus.Conn(t), "pw")
}

// stored reads the app's master secret straight from the Secret Service.
func stored(t *testing.T, r *rig, appID string) ([]byte, bool) {
	t.Helper()
	c, err := client.Open(context.Background(), r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	v, found, err := c.Lookup(context.Background(), secretAttrs(appID))
	if err != nil {
		t.Fatal(err)
	}
	return v, found
}

func TestSecretIsStablePerAppAndKeptInTheKeyring(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	r := newRig(t, nil)
	withKeyring(t, r)
	code, first := r.retrieveSecret(t, "org.app")
	if code != ResponseSuccess || len(first) != secretLen {
		t.Fatalf("first = %d, %d bytes", code, len(first))
	}
	if _, again := r.retrieveSecret(t, "org.app"); !bytes.Equal(first, again) {
		t.Error("the secret changed between calls")
	}
	if _, other := r.retrieveSecret(t, "org.other"); bytes.Equal(first, other) {
		t.Error("two apps share a secret")
	}
	if v, found := stored(t, r, "org.app"); !found || !bytes.Equal(v, first) {
		t.Error("the secret is not in the keyring")
	}
	if _, err := os.Stat(filepath.Join(data, "lys-shell/portal/secrets")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a plain secret file was written: %v", err)
	}
}

// A plain secret file left from before moves into the keyring once, with
// its value, and is removed.
func TestSecretMigratesThePlainFile(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	path := filepath.Join(data, "lys-shell/portal/secrets/org.app")
	old := bytes.Repeat([]byte{7}, secretLen)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, nil)
	withKeyring(t, r)
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseSuccess || !bytes.Equal(got, old) {
		t.Fatalf("migrated = %d, %x", code, got)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the plain file survived the migration: %v", err)
	}
	if v, found := stored(t, r, "org.app"); !found || !bytes.Equal(v, old) {
		t.Error("the migrated secret is not in the keyring")
	}
	if _, got := r.retrieveSecret(t, "org.app"); !bytes.Equal(got, old) {
		t.Error("the secret changed after the migration")
	}
}

// A plain file that cannot be read, or reads short (a truncated file),
// is an error and is left as it is: replacing it would make the app's
// keyring undecryptable for good.
func TestSecretNeverReplacesABrokenPlainFile(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	r := newRig(t, nil)
	withKeyring(t, r)
	path := filepath.Join(data, "lys-shell/portal/secrets/org.app")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("a short secret = %d, %d bytes; want other and nothing", code, len(got))
	}
	if got, _ := os.ReadFile(path); string(got) != "short" {
		t.Errorf("the short secret was replaced with %d bytes", len(got))
	}
	if _, found := stored(t, r, "org.app"); found {
		t.Error("a secret was stored in place of the broken file")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, secretLen), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("an unreadable secret = %d, %d bytes; want other and nothing", code, len(got))
	}
}

// Concurrent first uses agree on one secret.
func TestSecretCreationAgreesUnderConcurrency(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := newRig(t, nil)
	withKeyring(t, r)
	got := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() { _, got[i] = r.retrieveSecret(t, "org.app") })
	}
	wg.Wait()
	for i, v := range got {
		if len(v) != secretLen || !bytes.Equal(v, got[0]) {
			t.Fatalf("call %d got a different secret", i)
		}
	}
}

// A locked keyring is unlocked through the provider's prompt; a
// dismissed prompt fails the request and hands out nothing.
func TestSecretUnlocksTheKeyringThroughItsPrompt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := newRig(t, nil)
	keyring := withKeyring(t, r)
	_, first := r.retrieveSecret(t, "org.app")
	keyring.Lock()
	keyring.Answer("")
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("dismissed unlock = %d, %d bytes", code, len(got))
	}
	keyring.Answer("pw")
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseSuccess || !bytes.Equal(got, first) {
		t.Errorf("unlocked = %d, same secret %v", code, bytes.Equal(got, first))
	}
}

// Without a Secret Service the request fails; nothing is written in the
// clear instead.
func TestSecretFailsWithoutAKeyring(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	r := newRig(t, nil)
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("no keyring = %d, %d bytes", code, len(got))
	}
	if _, err := os.Stat(filepath.Join(data, "lys-shell/portal/secrets")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a plain secret file was written: %v", err)
	}
}

func TestSecretRefusesAnAppWithoutAnID(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := newRig(t, nil)
	withKeyring(t, r)
	if code, got := r.retrieveSecret(t, ""); code != ResponseOther || len(got) != 0 {
		t.Errorf("no app id = %d, %d bytes", code, len(got))
	}
	if _, found := stored(t, r, ""); found {
		t.Error("a shared host secret was stored")
	}
}

func TestSecretFailsWithoutAHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative")
	t.Setenv("HOME", "")
	_ = os.Unsetenv("HOME")
	r := newRig(t, nil)
	withKeyring(t, r)
	if code, got := r.retrieveSecret(t, "org.app"); code != ResponseOther || len(got) != 0 {
		t.Errorf("no home = %d, %d bytes", code, len(got))
	}
}

func TestSanitizeAppID(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd": "_.._.._etc_passwd", "..": "_..", ".": "_.", "": "_", "/": "_",
		"org.gnome.Builder": "org.gnome.Builder", "com.example_app-1": "com.example_app-1", "é": "_",
	} {
		if got := sanitizeAppID(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
