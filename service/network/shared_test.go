package network

import (
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func TestOnlyTheManagersChangesTick(t *testing.T) {
	manager := dbus.ObjectPath(nmPath)
	for _, c := range []struct {
		sig  dbus.Signal
		want bool
	}{
		{dbus.Signal{Path: manager, Name: nmIface + ".StateChanged", Body: []any{uint32(70)}}, true},
		{dbus.Signal{Path: manager, Name: properties + ".PropertiesChanged", Body: []any{nmIface}}, true},
		{dbus.Signal{Path: manager, Name: properties + ".PropertiesChanged", Body: []any{"org.freedesktop.NetworkManager.Other"}}, false},
		{dbus.Signal{Path: manager, Name: properties + ".PropertiesChanged"}, false},
		{dbus.Signal{Path: "/org/bluez/hci0", Name: properties + ".PropertiesChanged", Body: []any{nmIface}}, false},
		{dbus.Signal{Path: "/org/freedesktop/UPower/devices/DisplayDevice", Name: nmIface + ".StateChanged"}, false},
	} {
		if got := managerChanged(&c.sig); got != c.want {
			t.Errorf("managerChanged(%s %s %v) = %v, want %v", c.sig.Path, c.sig.Name, c.sig.Body, got, c.want)
		}
	}
}
