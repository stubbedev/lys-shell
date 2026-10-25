package dbusx_test

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// A watched name that gains an owner after the watch began, and again
// after a restart, runs appeared each time; another name, and the
// owner leaving, run nothing.
func TestWatchOwnerSeesALateService(t *testing.T) {
	bus := dbustest.Start(t)
	client := bus.Conn(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	appeared := make(chan struct{}, 8)
	if err := dbusx.WatchOwner(ctx, client, "dev.stubbe.lys.Late", func() { appeared <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	expect := func(want bool, what string) {
		t.Helper()
		select {
		case <-appeared:
			if !want {
				t.Errorf("%s ran appeared", what)
			}
		case <-time.After(300 * time.Millisecond):
			if want {
				t.Errorf("%s did not run appeared", what)
			}
		}
	}
	own := func(name string) *dbus.Conn {
		t.Helper()
		c := bus.Conn(t)
		if reply, err := c.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("own %s: %v %v", name, reply, err)
		}
		return c
	}
	other := own("dev.stubbe.lys.Other")
	expect(false, "another name")
	_ = other.Close()

	service := own("dev.stubbe.lys.Late")
	expect(true, "the service starting")
	_ = service.Close()
	expect(false, "the service stopping")
	restarted := own("dev.stubbe.lys.Late")
	expect(true, "the service restarting")
	_ = restarted.Close()
	expect(false, "the service stopping again")

	cancel()
	time.Sleep(50 * time.Millisecond)
	own("dev.stubbe.lys.Late")
	expect(false, "the service starting after the watch ended")
}
