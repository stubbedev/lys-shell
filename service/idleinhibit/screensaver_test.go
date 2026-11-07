package idleinhibit

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func TestScreenSaverInhibitHoldsUntilUnInhibitOrTheCallerLeaves(t *testing.T) {
	bus := dbustest.Start(t)
	state := NewState(minutes(t, 0))
	release, err := NewDaemon(state).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	browser, other := bus.Conn(t), bus.Conn(t)
	call := func(c *dbus.Conn, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		return c.Object(ScreenSaverName, path).Call(ScreenSaverName+"."+method, 0, args...)
	}
	for _, path := range []dbus.ObjectPath{ScreenSaverPath, "/ScreenSaver"} {
		var cookie uint32
		if err := call(browser, path, "Inhibit", "chromium", "Playing video").Store(&cookie); err != nil || cookie == 0 {
			t.Fatalf("%s Inhibit = %d, %v", path, cookie, err)
		}
		if h := state.Holds()[cookie]; h.App != "chromium" || h.Reason != "Playing video" || !state.Inhibiting() {
			t.Fatalf("%s: hold %+v, inhibiting %v", path, h, state.Inhibiting())
		}
		if err := call(other, path, "UnInhibit", cookie).Err; err == nil {
			t.Errorf("%s: another caller ended the hold", path)
		}
		if err := call(browser, path, "UnInhibit", cookie).Err; err != nil || state.Inhibiting() {
			t.Fatalf("%s UnInhibit = %v, inhibiting %v", path, err, state.Inhibiting())
		}
		if err := call(browser, path, "UnInhibit", cookie).Err; err == nil {
			t.Errorf("%s: a cookie ended twice", path)
		}
	}
	if err := call(browser, ScreenSaverPath, "Inhibit", "chromium", "again").Err; err != nil {
		t.Fatal(err)
	}
	_ = browser.Close()
	for deadline := time.Now().Add(2 * time.Second); state.Inhibiting(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a departed browser's ScreenSaver hold kept idle inhibited")
		}
	}
}

func TestAScreenSaverOwnedElsewhereLeavesTheDaemonServing(t *testing.T) {
	bus := dbustest.Start(t)
	owner := bus.Conn(t)
	if reply, err := owner.RequestName(ScreenSaverName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim %s: %v %v", ScreenSaverName, reply, err)
	}
	state := NewState(minutes(t, 0))
	release, err := NewDaemon(state).Export(bus.Conn(t))
	if err != nil {
		t.Fatalf("a ScreenSaver owned by another program failed the daemon: %v", err)
	}
	t.Cleanup(release)
	var id uint32
	if err := bus.Conn(t).Object(ServiceName, ServicePath).Call(ServiceName+".Hold", 0, "org.app", "x").Store(&id); err != nil {
		t.Fatal(err)
	}
}
