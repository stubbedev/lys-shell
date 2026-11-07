package sni

import (
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
)

func TestCloseHandsTheWatcherAndASharedConnectionBack(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	host, err := NewHostOn(conn, NewStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("Close closed the shared connection: %v", err)
	}
	var hasOwner bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, WatcherName).Store(&hasOwner); err != nil {
		t.Fatal(err)
	}
	if hasOwner {
		t.Error("the watcher name outlived Close")
	}
	other := bus.Conn(t)
	if err := other.Object(conn.Names()[0], WatcherPath).Call(PropsIface+".GetAll", 0, WatcherIface).Err; err == nil {
		t.Error("the watcher object is still exported after Close")
	}
	again, err := NewHostOn(conn, NewStore())
	if err != nil {
		t.Fatalf("a second host on the same connection: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if again.watcher == nil {
		t.Error("the second host did not become the watcher")
	}
}
