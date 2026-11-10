package portal

import (
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-lib/logind/logindtest"
)

func TestLogindLockTakesABlockInhibitor(t *testing.T) {
	fake := logindtest.StartUnit(t, logindtest.Config{Display: "2"})
	l := &logind{}
	t.Cleanup(l.close)
	f, err := l.lock(inhibitWhat(inhibitIdle|inhibitSuspend|inhibitLogout), "Playing music")
	if err != nil {
		t.Fatal(err)
	}
	calls := fake.CallsTo("Manager.Inhibit")
	want := []any{"shutdown:sleep:idle", identity.Name + " portal", "Playing music", "block"}
	if len(calls) != 1 || !slices.Equal(calls[0].Args, want) {
		t.Fatalf("Inhibit calls %v, want one with %v", calls, want)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for fake.Inhibitors.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("closing the lock kept the inhibitor")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLogindScreensaverFollowsLockedHint(t *testing.T) {
	fake := logindtest.StartUnit(t, logindtest.Config{Display: "2"})
	l := &logind{}
	t.Cleanup(l.close)
	changes := make(chan bool, 4)
	stop, err := l.watchScreensaver(func(active bool) { changes <- active })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if active, err := l.screensaverActive(); err != nil || active {
		t.Fatalf("screensaverActive before the lock: %t, %v", active, err)
	}
	fake.SetLockedHint(t, "2", true)
	select {
	case active := <-changes:
		if !active {
			t.Error("LockedHint true reported the screensaver inactive")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch never saw LockedHint change")
	}
	if active, err := l.screensaverActive(); err != nil || !active {
		t.Errorf("screensaverActive after the lock: %t, %v", active, err)
	}
}
