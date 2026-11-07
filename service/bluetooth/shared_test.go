package bluetooth

import (
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func TestCloseHandsASharedConnectionBack(t *testing.T) {
	f := populated(t)
	conn := f.bus.Conn(t)
	s, err := New(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if at, _, isDefault := f.registration(); at != "" || isDefault {
		t.Errorf("after Close the agent is still registered at %q (default %v)", at, isDefault)
	}
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("Close closed the shared connection: %v", err)
	}
	again, err := New(conn)
	if err != nil {
		t.Fatalf("a second service on the same connection: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if st := again.State(); len(st.Devices) != 2 {
		t.Errorf("the second service sees %d devices, want 2", len(st.Devices))
	}
}

func TestOnlyBlueZsSignalsReachTheModel(t *testing.T) {
	for _, c := range []struct {
		sig  dbus.Signal
		want bool
	}{
		{dbus.Signal{Path: hci0, Name: propertiesChange}, true},
		{dbus.Signal{Path: dev1, Name: deviceIface + ".Disconnected"}, true},
		{dbus.Signal{Path: objectManagerAt, Name: objectManager + ".InterfacesAdded", Body: []any{dev2}}, true},
		{dbus.Signal{Path: "/org/freedesktop/NetworkManager", Name: propertiesChange}, false},
		{dbus.Signal{Path: "/org/bluezz", Name: propertiesChange}, false},
		{dbus.Signal{Path: objectManagerAt, Name: objectManager + ".InterfacesAdded", Body: []any{dbus.ObjectPath("/org/freedesktop/UDisks2/drives/a")}}, false},
		{dbus.Signal{Path: objectManagerAt, Name: objectManager + ".InterfacesAdded"}, false},
	} {
		if got := fromBlueZ(&c.sig); got != c.want {
			t.Errorf("fromBlueZ(%s %s %v) = %v, want %v", c.sig.Path, c.sig.Name, c.sig.Body, got, c.want)
		}
	}
}
