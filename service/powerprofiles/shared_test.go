package powerprofiles

import (
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func TestOnlyTheDaemonsPropertyChangesTick(t *testing.T) {
	for _, c := range []struct {
		sig  dbus.Signal
		want bool
	}{
		{dbus.Signal{Path: daemonPath, Name: properties + ".PropertiesChanged", Body: []any{daemonIface}}, true},
		{dbus.Signal{Path: daemonPath, Name: properties + ".PropertiesChanged", Body: []any{"org.freedesktop.UPower"}}, false},
		{dbus.Signal{Path: daemonPath, Name: properties + ".PropertiesChanged"}, false},
		{dbus.Signal{Path: "/org/freedesktop/NetworkManager", Name: properties + ".PropertiesChanged", Body: []any{daemonIface}}, false},
		{dbus.Signal{Path: daemonPath, Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []any{daemonIface}}, false},
	} {
		if got := daemonChanged(&c.sig); got != c.want {
			t.Errorf("daemonChanged(%s %s %v) = %v, want %v", c.sig.Path, c.sig.Name, c.sig.Body, got, c.want)
		}
	}
}
