package bar

import (
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/upower"
)

// A service appearing after the shell rebuilds the bars; a client that
// could not start is started then, retried after a failed start, and
// never started twice; the late client stops with the shell.
func TestLateServicesStartTheirClientAndRebuild(t *testing.T) {
	bus := dbustest.Start(t)
	starts, stops, applied := 0, 0, 0
	late := &lateServices{}
	rebuilt := make(chan bool, 8)
	err := late.watch(t.Context(), bus.Conn(t), map[string]lateStart{
		network.BusName: func() (func(*ModuleContext), func(), error) {
			starts++
			if starts == 1 {
				return nil, nil, errors.New("not yet")
			}
			return func(*ModuleContext) { applied++ }, func() { stops++ }, nil
		},
	}, func(fn func()) { fn() }, func(apply func(*ModuleContext)) {
		if apply != nil {
			apply(&ModuleContext{})
		}
		rebuilt <- apply != nil
	})
	if err != nil {
		t.Fatal(err)
	}
	expect := func(what string, withClient bool) {
		t.Helper()
		select {
		case got := <-rebuilt:
			if got != withClient {
				t.Errorf("%s: rebuilt with a client %v, want %v", what, got, withClient)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no rebuild", what)
		}
	}
	appear := func(name string) *dbus.Conn {
		t.Helper()
		c := bus.Conn(t)
		if reply, err := c.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("own %s: %v %v", name, reply, err)
		}
		return c
	}

	nm := appear(network.BusName)
	expect("NetworkManager appearing with its client failing", false)
	_ = nm.Close()
	nm = appear(network.BusName)
	expect("NetworkManager appearing again", true)
	_ = nm.Close()
	appear(network.BusName)
	expect("NetworkManager restarting", false)
	if starts != 2 {
		t.Errorf("the client started %d times, want 2 (once failing, once for good)", starts)
	}
	if applied != 1 {
		t.Errorf("the started client was applied %d times, want 1", applied)
	}
	appear(upower.BusName)
	expect("UPower appearing", false)

	late.stop()
	if stops != 1 {
		t.Errorf("the late client stopped %d times, want 1", stops)
	}
}
