package mpris

import (
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
)

func TestCloseHandsASharedConnectionBack(t *testing.T) {
	bus := dbustest.Start(t)
	startFakePlayer(t, bus, BusNamePrefix+"vlc", "First", "Paused")
	conn := bus.Conn(t)
	svc, err := New(conn, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("Close closed the shared connection: %v", err)
	}
	again, err := New(conn, nil, nil)
	if err != nil {
		t.Fatalf("a second service on the same connection: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if got := again.Players(); len(got) != 1 {
		t.Errorf("the second service sees %d players, want 1", len(got))
	}
}
