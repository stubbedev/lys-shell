package portal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/portal/permissions"
	"github.com/stubbedev/lys-desktop/portal/permissions/permstoretest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal/bussignaltest"
	"github.com/stubbedev/lys-shell/service/notifications"
)

const hostRegistry = "org.freedesktop.host.portal.Registry"

// persistRig is the real frontend in front of the backend, with the
// real xdg-permission-store keeping its restore tokens, a share picker
// replying screen DP-1 with a token granted, a notification daemon and
// the app registered as org.app.
type persistRig struct {
	*rig
	starts *fakeStarts
	picker *fakeSharePicker
	notify *fakeNotifyd
	front  *frontend
	store  permissions.Store
}

func newPersistRig(t *testing.T) persistRig {
	t.Helper()
	r, f := castRig(t)
	permstoretest.Start(t, r.bus, t.TempDir())
	p := persistRig{rig: r, starts: f, picker: serveSharePicker(t, r, "r/screen:DP-1"), notify: serveNotifyd(t, r)}
	dataHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataHome, "applications"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := "[Desktop Entry]\nType=Application\nName=App\nExec=true\n"
	if err := os.WriteFile(filepath.Join(dataHome, "applications", "org.app.desktop"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	p.front = startFrontend(t, r, dataHome)
	if err := p.front.obj.Call(hostRegistry+".Register", 0, "org.app", Vardict{}).Err; err != nil {
		t.Fatalf("register org.app: %v", err)
	}
	p.store = permissions.Store{Conn: r.bus.Conn(t)}
	return p
}

// share opens a persistent ScreenCast session through the frontend,
// replaying token when set, and starts it in the background.
func (p persistRig) share(t *testing.T, token string) (dbus.ObjectPath, <-chan []any) {
	t.Helper()
	session := p.front.session(t, publicScreenCast)
	options := Vardict{"types": dbus.MakeVariant(sourceMonitor), "persist_mode": dbus.MakeVariant(persistPermanent)}
	if token != "" {
		options["restore_token"] = dbus.MakeVariant(token)
	}
	if code, _ := p.front.request(t, publicScreenCast+".SelectSources", session, options); code != ResponseSuccess {
		t.Fatalf("SelectSources = %d", code)
	}
	done := make(chan []any, 1)
	go func() {
		code, res := p.front.request(t, publicScreenCast+".Start", session, "", Vardict{})
		done <- []any{code, res}
	}()
	return session, done
}

func started(t *testing.T, done <-chan []any) (uint32, string) {
	t.Helper()
	select {
	case got := <-done:
		res, _ := got[1].(map[string]dbus.Variant)
		token, _ := res["restore_token"].Value().(string)
		return got[0].(uint32), token
	case <-time.After(10 * time.Second):
		t.Fatal("Start never replied")
	}
	return 0, ""
}

// close closes a session through the frontend, which stores the token
// then; it returns once the store holds the change.
func (p persistRig) close(t *testing.T, session dbus.ObjectPath) {
	t.Helper()
	if err := p.front.conn.Object(frontendName, session).Call("org.freedesktop.portal.Session.Close", 0).Err; err != nil {
		t.Fatalf("close %s: %v", session, err)
	}
}

// tokens are the screen-share tokens the store keeps for org.app.
func (p persistRig) tokens(t *testing.T) []string {
	t.Helper()
	apps, err := p.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range apps {
		for _, g := range a.Grants {
			if a.ID == "org.app" && g.Table == permissions.ScreenCast {
				ids = append(ids, g.ID)
			}
		}
	}
	return ids
}

func (p persistRig) waitTokens(t *testing.T, want []string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !slices.Equal(p.tokens(t), want); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("stored tokens %v, want %v", p.tokens(t), want)
		}
	}
}

// A persistent token the frontend stored for org.app is listed, replays
// without the picker through the store's GVariant round trip, and once
// revoked the next Start shows the picker.
func TestFrontendRevokedTokenAsksAgain(t *testing.T) {
	p := newPersistRig(t)
	session, done := p.share(t, "")
	code, token := started(t, done)
	if code != ResponseSuccess || token == "" {
		t.Fatalf("first Start = %d, token %q", code, token)
	}
	p.close(t, session)
	p.waitTokens(t, []string{token})

	session, done = p.share(t, token)
	if code, again := started(t, done); code != ResponseSuccess || again != token || len(p.picker.asked.Load()) != 1 {
		t.Fatalf("replay = %d, token %q, picker asked %d times; want no picker", code, again, len(p.picker.asked.Load()))
	}
	p.close(t, session)
	p.waitTokens(t, []string{token})

	if n, err := p.store.Revoke(context.Background(), "org.app", permissions.ScreenCast, ""); err != nil || n != 1 {
		t.Fatalf("revoke = %d, %v", n, err)
	}
	p.waitTokens(t, nil)
	_, done = p.share(t, token)
	if code, _ := started(t, done); code != ResponseSuccess || len(p.picker.asked.Load()) != 2 {
		t.Errorf("after revoke = %d, picker asked %d times; want it to ask", code, len(p.picker.asked.Load()))
	}
}

// A 31-day-old persistent token reminds on restore; Revoke ends Start
// cancelled, closes the session and leaves no token in the store.
func TestFrontendReminderRevokeDeletesTheToken(t *testing.T) {
	p := newPersistRig(t)
	session, done := p.share(t, "")
	_, token := started(t, done)
	p.close(t, session)
	p.waitTokens(t, []string{token})

	p.starts.clock.Advance(31 * day)
	session, done = p.share(t, token)
	closed, _ := bussignaltest.Signals(t, p.front.conn, 4, dbus.WithMatchObjectPath(session), dbus.WithMatchMember("Closed"))
	waitNotify(t, p.notify, 1)
	_ = p.notify.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", p.notify.next.Load(), reminderRevoke)
	if code, again := started(t, done); code != ResponseCancelled || again != "" {
		t.Fatalf("Revoke: Start = %d, token %q; want cancelled with none", code, again)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Error("the frontend never closed the session")
	}
	p.waitTokens(t, nil)
	if n := len(p.notify.calls.Load()); n != 1 {
		t.Errorf("%d reminders, want one", n)
	}
}
