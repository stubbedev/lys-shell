package network

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func TestOnlyTheManagersChangesTick(t *testing.T) {
	const nm, other = ":1.7", ":1.8"
	manager := dbus.ObjectPath(nmPath)
	noProps, noInvalidated := map[string]dbus.Variant{}, []string{}
	ticked := 0
	routes := managerRoutes(func() { ticked++ })
	for _, c := range []struct {
		sig  dbus.Signal
		want bool
	}{
		{dbus.Signal{Sender: nm, Path: manager, Name: nmIface + ".StateChanged", Body: []any{uint32(70)}}, true},
		{dbus.Signal{Sender: nm, Path: manager, Name: properties + ".PropertiesChanged", Body: []any{nmIface, noProps, noInvalidated}}, true},
		{dbus.Signal{Sender: nm, Path: manager, Name: properties + ".PropertiesChanged", Body: []any{"org.freedesktop.NetworkManager.Other", noProps, noInvalidated}}, false},
		{dbus.Signal{Sender: nm, Path: manager, Name: properties + ".PropertiesChanged"}, false},
		{dbus.Signal{Sender: nm, Path: manager, Name: nmIface + ".DeviceAdded", Body: []any{dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/1")}}, false},
		{dbus.Signal{Sender: nm, Path: "/org/bluez/hci0", Name: properties + ".PropertiesChanged", Body: []any{nmIface, noProps, noInvalidated}}, false},
		{dbus.Signal{Sender: nm, Path: "/org/freedesktop/UPower/devices/DisplayDevice", Name: nmIface + ".StateChanged", Body: []any{uint32(70)}}, false},
		{dbus.Signal{Sender: other, Path: manager, Name: nmIface + ".StateChanged", Body: []any{uint32(70)}}, false},
		{dbus.Signal{Sender: other, Path: manager, Name: properties + ".PropertiesChanged", Body: []any{nmIface, noProps, noInvalidated}}, false},
	} {
		before := ticked
		err := dbusx.Dispatch(&c.sig, nm, routes)
		if got := ticked > before; got != c.want {
			t.Errorf("%s %s %v from %s ticked %v (%v), want %v", c.sig.Path, c.sig.Name, c.sig.Body, c.sig.Sender, got, err, c.want)
		}
	}
}

// On the bus the feed ticks for the manager's changes, and stopping it
// closes it and leaves the shared connection without its match rules.
func TestTheManagersFeedTicksAndLeavesTheSharedConnectionAsItWas(t *testing.T) {
	f := startFakeNM(t)
	conn := f.client()
	ticks, stop, err := NewOn(conn).Subscribe(t.Context())
	must(t, err)
	select {
	case <-ticks:
		t.Fatal("a tick before NM changed")
	default:
	}
	f.setState(StateConnecting)
	select {
	case _, open := <-ticks:
		if !open {
			t.Fatal("the feed closed while it was followed")
		}
	case <-time.After(liveness):
		t.Fatal("no tick for the manager's State changing")
	}
	stop()
	for range ticks {
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the stopped feed left the match rules %q", rules)
	}
}
