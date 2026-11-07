package mpris

import (
	"context"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/fakeclock"
)

func TestStoreReportsTrackChangesOfTheActivePlayer(t *testing.T) {
	clock := fakeclock.New()
	vlc := Player{BusName: BusNamePrefix + "vlc", Title: "One", TrackID: "/t/1"}
	mpv := Player{BusName: BusNamePrefix + "mpv", Title: "Other", TrackID: "/t/1"}
	s := &Service{players: []Player{vlc, mpv}, active: vlc.BusName, now: clock.Now, requestedAt: map[string]time.Time{}}
	run := s.requested(func(_ context.Context, _ string) error { return nil })

	paused := vlc
	paused.State = StatePaused
	if _, _, moved := s.store(paused); moved {
		t.Error("a state change on the same track counted as a track change")
	}
	two := paused
	two.Title, two.TrackID = "Two", "/t/2"
	if _, change, moved := s.store(two); !moved || change.Requested || change.Player.Title != "Two" {
		t.Errorf("an unrequested track change = %+v, %v; want Two, not requested", change, moved)
	}

	if err := run(context.Background(), vlc.BusName); err != nil {
		t.Fatal(err)
	}
	three := two
	three.Title, three.TrackID = "Three", "/t/3"
	if _, change, moved := s.store(three); !moved || !change.Requested {
		t.Errorf("a track change right after Next = %+v, %v; want requested", change, moved)
	}
	four := three
	four.Title, four.TrackID = "Four", "/t/4"
	if _, change, _ := s.store(four); change.Requested {
		t.Error("one request marked two track changes")
	}

	if err := run(context.Background(), vlc.BusName); err != nil {
		t.Fatal(err)
	}
	clock.Advance(requestWindow + time.Millisecond)
	five := four
	five.Title, five.TrackID = "Five", "/t/5"
	if _, change, _ := s.store(five); change.Requested {
		t.Error("a request older than its window marked a later automatic change")
	}

	blank := five
	blank.Title, blank.TrackID = "", ""
	if _, _, moved := s.store(blank); moved {
		t.Error("a player clearing its metadata counted as a track change")
	}
	other := mpv
	other.Title = "Elsewhere"
	if _, _, moved := s.store(other); moved {
		t.Error("an inactive player's track change was reported")
	}
}

func TestRequestedCallThatFailsMarksNothing(t *testing.T) {
	clock := fakeclock.New()
	vlc := Player{BusName: BusNamePrefix + "vlc", Title: "One"}
	s := &Service{players: []Player{vlc}, active: vlc.BusName, now: clock.Now, requestedAt: map[string]time.Time{}}
	failing := s.requested(func(_ context.Context, _ string) error { return ErrNoPlayer })
	if err := failing(context.Background(), vlc.BusName); err == nil {
		t.Fatal("the wrapped error was swallowed")
	}
	next := vlc
	next.Title = "Two"
	if _, change, _ := s.store(next); change.Requested {
		t.Error("a failed Next marked the next track change as requested")
	}
}

func TestDaemonNextFlagsTheTrackChangeItCauses(t *testing.T) {
	bus := dbustest.Start(t)
	name := BusNamePrefix + "vlc"
	player := startFakePlayer(t, bus, name, "First", "Playing")
	svc, err := New(bus.Conn(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	changes, stop := svc.TrackChanges()
	defer stop()

	setTrack := func(title, id string) {
		player.props.SetMust(PlayerIface, "Metadata", map[string]dbus.Variant{
			"xesam:title":   dbus.MakeVariant(title),
			"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath(id)),
		})
	}
	next := func() TrackChange {
		t.Helper()
		select {
		case c := <-changes:
			return c
		case <-time.After(2 * time.Second):
			t.Fatal("no track change")
		}
		return TrackChange{}
	}

	d := &Daemon{s: svc}
	if derr := d.Next(""); derr != nil {
		t.Fatal(derr)
	}
	setTrack("Second", "/t/2")
	if c := next(); !c.Requested || c.Player.Title != "Second" {
		t.Errorf("after the daemon's Next: %+v, want Second, requested", c)
	}
	setTrack("Third", "/t/3")
	if c := next(); c.Requested || c.Player.Title != "Third" {
		t.Errorf("a playlist advance: %+v, want Third, not requested", c)
	}
	if derr := d.Previous(""); derr != nil {
		t.Fatal(derr)
	}
	setTrack("Second", "/t/2")
	if c := next(); !c.Requested {
		t.Errorf("after the daemon's Previous: %+v, want requested", c)
	}
}
