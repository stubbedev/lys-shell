package portal

import (
	"slices"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/notifications"
)

// reminderRig issues a persistent token for screen DP-1 at the fake
// clock's start, with a notification daemon to remind through. The
// token is re-encoded as the frontend hands it back: a struct received
// as []any would go out again as av.
func reminderRig(t *testing.T) (*rig, *fakeStarts, *fakeNotifyd, dbus.Variant) {
	t.Helper()
	r, f := castRig(t)
	d := serveNotifyd(t, r)
	serveSharePicker(t, r, "r/screen:DP-1")
	r.castSession(t, Vardict{"persist_mode": dbus.MakeVariant(persistPermanent)})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("first Start = %d", code)
	}
	g, ok := decodeRestore(res["restore_data"])
	if !ok || !g.issued.Equal(f.clock.Now()) {
		t.Fatalf("token issued %v, want %v", g.issued, f.clock.Now())
	}
	r.closeSession(t, sessA)
	return r, f, d, dbus.MakeVariant(encodeRestore(g))
}

// replay starts a session replaying token, in the background: a
// reminder holds Start until it is answered.
func (r *rig) replay(t *testing.T, token dbus.Variant) <-chan []any {
	t.Helper()
	r.castSession(t, Vardict{"restore_data": token, "persist_mode": dbus.MakeVariant(persistPermanent)})
	done := make(chan []any, 1)
	go func() {
		var code uint32
		var res map[string]dbus.Variant
		err := r.obj.Call(ScreenCastIface+".Start", 0, handle, sessA, "org.app", "", Vardict{}).Store(&code, &res)
		done <- []any{code, res, err}
	}()
	return done
}

func replied(t *testing.T, done <-chan []any) (uint32, restoreGrant) {
	t.Helper()
	select {
	case got := <-done:
		if got[2] != nil {
			t.Fatalf("Start: %v", got[2])
		}
		res, _ := got[1].(map[string]dbus.Variant)
		g, _ := decodeRestore(res["restore_data"])
		return got[0].(uint32), g
	case <-time.After(5 * time.Second):
		t.Fatal("Start never replied")
	}
	return 0, restoreGrant{}
}

// A persistent token 29 days old replays without a reminder and keeps
// its age; at 31 days one reminder asks, Keep shares and restarts the
// count, and the next replay does not ask again.
func TestAnOldPersistentShareRemindsOnce(t *testing.T) {
	r, f, d, token := reminderRig(t)
	issued := f.clock.Now()
	f.clock.Advance(29 * day)
	code, g := replied(t, r.replay(t, token))
	if code != ResponseSuccess || len(d.calls.Load()) != 0 || !g.issued.Equal(issued) {
		t.Fatalf("29 days: Start %d, %d reminders, issued %v; want no reminder and the age kept", code, len(d.calls.Load()), g.issued)
	}
	r.closeSession(t, sessA)

	f.clock.Advance(2 * day)
	done := r.replay(t, token)
	call := waitNotify(t, d, 1)
	if call.summary != "org.app can share your screen without asking" || call.body != "You allowed it 31 days ago. Keep allowing it?" ||
		!slices.Equal(call.actions, reminderActions) || call.appName != "org.app" {
		t.Errorf("reminder = %+v", call)
	}
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", d.next.Load(), reminderKeep)
	code, kept := replied(t, done)
	if code != ResponseSuccess || !kept.issued.Equal(f.clock.Now()) || !slices.Equal(f.started.Load(), []string{"screen:DP-1", "screen:DP-1", "screen:DP-1"}) {
		t.Fatalf("Keep: Start %d, issued %v, started %v", code, kept.issued, f.started.Load())
	}
	r.closeSession(t, sessA)

	code, _ = replied(t, r.replay(t, dbus.MakeVariant(encodeRestore(kept))))
	if code != ResponseSuccess || len(d.calls.Load()) != 1 {
		t.Errorf("after Keep: Start %d, %d reminders; want no second one", code, len(d.calls.Load()))
	}
}

// Revoke ends the request cancelled, starts nothing and closes the
// session; a reminder closed unanswered shares and keeps the age, so
// the next replay asks again; 0 days never asks.
func TestRevokingTheReminderClosesTheSession(t *testing.T) {
	r, f, d, token := reminderRig(t)
	issued := f.clock.Now()
	f.clock.Advance(31 * day)
	done := r.replay(t, token)
	waitNotify(t, d, 1)
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", d.next.Load(), reminderRevoke)
	if code, _ := replied(t, done); code != ResponseCancelled {
		t.Errorf("Revoke: Start %d, want cancelled", code)
	}
	if _, live := r.backend.sessions.get(sessA); live {
		t.Error("the session outlived the revoke")
	}
	if got := f.started.Load(); len(got) != 1 {
		t.Errorf("started %v: the revoked share streamed", got)
	}

	done = r.replay(t, token)
	waitNotify(t, d, 2)
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".NotificationClosed", d.next.Load(), uint32(2))
	if code, g := replied(t, done); code != ResponseSuccess || !g.issued.Equal(issued) {
		t.Errorf("dismissed: Start %d, issued %v; want the share and the age kept", code, g.issued)
	}
	r.closeSession(t, sessA)

	if err := r.cfg.SetByPath("portal.screen-cast.reminder-days", int64(0)); err != nil {
		t.Fatal(err)
	}
	if code, _ := replied(t, r.replay(t, token)); code != ResponseSuccess || len(d.calls.Load()) != 2 {
		t.Errorf("0 days: Start %d, %d reminders; want none", code, len(d.calls.Load()))
	}
}

// A transient token never reminds, however old.
func TestATransientShareNeverReminds(t *testing.T) {
	r, f, d, token := reminderRig(t)
	f.clock.Advance(400 * day)
	r.castSession(t, Vardict{"restore_data": token, "persist_mode": dbus.MakeVariant(uint32(1))})
	if code, _ := r.castStart(t); code != ResponseSuccess || len(d.calls.Load()) != 0 {
		t.Errorf("transient: Start %d, %d reminders", code, len(d.calls.Load()))
	}
}

// Open Settings opens lys-settings on the Privacy page and shares,
// keeping the grant's age so the next replay asks again; a settings app
// that cannot start still shares.
func TestTheReminderOpensThePrivacySettings(t *testing.T) {
	r, f, d, token := reminderRig(t)
	s := &spawned{}
	r.backend.spawn = s.spawn
	issued := f.clock.Now()
	f.clock.Advance(40 * day)
	done := r.replay(t, token)
	waitNotify(t, d, 1)
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", d.next.Load(), reminderSettings)
	if code, g := replied(t, done); code != ResponseSuccess || !g.issued.Equal(issued) {
		t.Errorf("Open Settings: Start %d, issued %v; want the share and the age kept", code, g.issued)
	}
	if got := s.argv.Load(); len(got) != 1 || !slices.Equal(got[0], []string{"lys-settings", "--page", "privacy"}) {
		t.Errorf("spawned %q, want lys-settings on the Privacy page", got)
	}
	r.closeSession(t, sessA)

	s.failOn("lys-settings")
	done = r.replay(t, token)
	waitNotify(t, d, 2)
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", d.next.Load(), reminderSettings)
	if code, _ := replied(t, done); code != ResponseSuccess {
		t.Errorf("no settings app: Start %d, want the share", code)
	}
}
