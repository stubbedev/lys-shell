package portal

import (
	"context"
	"slices"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/screenshare"
)

// followShares follows the rig's shares as the shell's bar does.
func followShares(t *testing.T, r *rig) *screenshare.Shares {
	t.Helper()
	s, err := screenshare.Follow(r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func awaitShares(t *testing.T, s *screenshare.Shares, want []screenshare.Share) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.EqualFunc(s.List(), want, func(a, b screenshare.Share) bool {
		return a.Session == b.Session && a.App == b.App && a.Kind == b.Kind && slices.Equal(a.Targets, b.Targets)
	}) {
		if time.Now().After(deadline) {
			t.Fatalf("shares %+v, want %+v", s.List(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAStartedShareIsListedAndStopEndsItsSession(t *testing.T) {
	r, f := castRig(t)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(SessionIface)); err != nil {
		t.Fatal(err)
	}
	serveSharePicker(t, r, "/screen:DP-1")
	shares := followShares(t, r)
	ticks, stop := shares.Subscribe()
	t.Cleanup(stop)
	if got := shares.List(); len(got) != 0 {
		t.Fatalf("shares before any session: %+v", got)
	}
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	awaitShares(t, shares, []screenshare.Share{{
		Session: sessA, App: "org.app", Kind: screenshare.KindScreenCast,
		Targets: []screenshare.Target{{Kind: screenshare.TargetOutput, Name: "DP-1"}},
	}})
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Error("no change was told")
	}
	if err := shares.Stop(context.Background(), sessA); err != nil {
		t.Fatal(err)
	}
	r.waitClosed(t, sessA)
	awaitShares(t, shares, nil)
	if !f.streams.Load()[0].closed.Load() {
		t.Error("the stream still runs")
	}
}

func TestAWindowShareNamesItsWindow(t *testing.T) {
	f := &fakeStarts{fail: map[string]bool{}}
	r := newRig(t, func(b *Backend) {
		b.screenCast = f.screenCast(b)
		b.shares.titles = func() map[string]string { return map[string]string{"lys-wm-7": "Notes - Editor"} }
	})
	serveSharePicker(t, r, "/window:lys-wm-7")
	shares := followShares(t, r)
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceWindow)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	awaitShares(t, shares, []screenshare.Share{{
		Session: sessA, App: "org.app", Kind: screenshare.KindScreenCast,
		Targets: []screenshare.Target{{Kind: screenshare.TargetWindow, Name: "lys-wm-7", Title: "Notes - Editor"}},
	}})
}

func TestStopRefusesWhatIsNotAShare(t *testing.T) {
	r, _ := castRig(t)
	shares := followShares(t, r)
	r.castSession(t, Vardict{})
	if err := shares.Stop(context.Background(), sessA); err == nil {
		t.Error("a session that streams nothing stopped as a share")
	}
	if _, live := r.backend.sessions.get(sessA); !live {
		t.Error("the refused Stop closed the session")
	}
	if err := shares.Stop(context.Background(), "/no/such/session"); err == nil {
		t.Error("an unknown session stopped")
	}
}

func TestAShareWhoseSourceVanishesLeavesTheList(t *testing.T) {
	r, f := castRig(t)
	serveSharePicker(t, r, "/screen:DP-1")
	shares := followShares(t, r)
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	awaitShares(t, shares, []screenshare.Share{{
		Session: sessA, App: "org.app", Kind: screenshare.KindScreenCast,
		Targets: []screenshare.Target{{Kind: screenshare.TargetOutput, Name: "DP-1"}},
	}})
	f.streams.Load()[0].vanish()
	awaitShares(t, shares, nil)
}
