package bluetooth

import "testing"

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
