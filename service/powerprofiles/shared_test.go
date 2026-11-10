package powerprofiles

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
)

func TestOnlyTheDaemonsPropertyChangesTick(t *testing.T) {
	const daemon, other = ":1.7", ":1.8"
	noProps, noInvalidated := map[string]dbus.Variant{}, []string{}
	ticked := 0
	routes := daemonRoutes(func() { ticked++ })
	for _, c := range []struct {
		sig  dbus.Signal
		want bool
	}{
		{dbus.Signal{Sender: daemon, Path: daemonPath, Name: properties + ".PropertiesChanged", Body: []any{daemonIface, noProps, noInvalidated}}, true},
		{dbus.Signal{Sender: daemon, Path: daemonPath, Name: properties + ".PropertiesChanged", Body: []any{"org.freedesktop.UPower", noProps, noInvalidated}}, false},
		{dbus.Signal{Sender: daemon, Path: daemonPath, Name: properties + ".PropertiesChanged"}, false},
		{dbus.Signal{Sender: daemon, Path: "/org/freedesktop/NetworkManager", Name: properties + ".PropertiesChanged", Body: []any{daemonIface, noProps, noInvalidated}}, false},
		{dbus.Signal{Sender: daemon, Path: daemonPath, Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []any{daemonIface}}, false},
		{dbus.Signal{Sender: other, Path: daemonPath, Name: properties + ".PropertiesChanged", Body: []any{daemonIface, noProps, noInvalidated}}, false},
	} {
		before := ticked
		err := dbusx.Dispatch(&c.sig, daemon, routes)
		if got := ticked > before; got != c.want {
			t.Errorf("%s %s %v from %s ticked %v (%v), want %v", c.sig.Path, c.sig.Name, c.sig.Body, c.sig.Sender, got, err, c.want)
		}
	}
}

// On the bus the feed ticks when the daemon's active profile changes,
// and stopping it closes it and leaves the shared connection without
// its match rules.
func TestTheDaemonsFeedTicksAndLeavesTheSharedConnectionAsItWas(t *testing.T) {
	bus := dbustest.Start(t)
	daemon := bus.Conn(t)
	props, err := prop.Export(daemon, daemonPath, prop.Map{daemonIface: {
		"ActiveProfile": {Value: ProfileBalanced, Writable: true, Emit: prop.EmitTrue},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if reply, err := daemon.RequestName(BusName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", BusName, reply, err)
	}
	conn := bus.Conn(t)
	ticks, stop, err := NewOn(conn).Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticks:
		t.Fatal("a tick before the daemon changed")
	default:
	}
	props.SetMust(daemonIface, "ActiveProfile", ProfilePerformance)
	select {
	case _, open := <-ticks:
		if !open {
			t.Fatal("the feed closed while it was followed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no tick for the active profile changing")
	}
	stop()
	for range ticks {
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the stopped feed left the match rules %q", rules)
	}
}
