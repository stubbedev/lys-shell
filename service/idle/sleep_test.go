package idle

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/logind"
	"github.com/stubbedev/lys-shell/service/logind/logindtest"
)

const display = "/org/freedesktop/login1/session/c1"

func startSleepLock(t *testing.T, lockWait time.Duration) *logindtest.Logind {
	t.Helper()
	fake := logindtest.Start(t, display)
	conn := fake.Bus.Conn(t)
	ctx, cancel := context.WithTimeout(context.Background(), logind.CallTimeout)
	defer cancel()
	session, err := logind.NewClient(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := LockBeforeSleep(conn, session, lockWait)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if n := fake.Inhibitors.Load(); n != 1 {
		t.Fatalf("%d sleep inhibitors held after start, want 1", n)
	}
	return fake
}

func awaitEvents(t *testing.T, fake *logindtest.Logind, want []string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		got := fake.Events.Load()
		if len(got) >= len(want) {
			if !slices.Equal(got, want) {
				t.Fatalf("logind saw %v, want %v", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("logind saw %v, want %v", got, want)
		}
	}
}

func TestSleepWaitsForTheLockScreenBeforeLettingGo(t *testing.T) {
	fake := startSleepLock(t, 5*time.Second)
	fake.OnLock.Store(func() {
		time.Sleep(200 * time.Millisecond)
		if err := fake.SetLockedHint(true); err != nil {
			t.Error(err)
		}
	})
	if err := fake.PrepareForSleep(true); err != nil {
		t.Fatal(err)
	}
	awaitEvents(t, fake, []string{
		logindtest.EventInhibit, logindtest.EventLock, logindtest.EventLockedHint, logindtest.EventInhibitorReleased,
	})
	if n := fake.Inhibitors.Load(); n != 0 {
		t.Errorf("%d inhibitors held while asleep", n)
	}
	if err := fake.SetLockedHint(false); err != nil {
		t.Fatal(err)
	}
	if err := fake.PrepareForSleep(false); err != nil {
		t.Fatal(err)
	}
	awaitEvents(t, fake, []string{
		logindtest.EventInhibit, logindtest.EventLock, logindtest.EventLockedHint, logindtest.EventInhibitorReleased,
		logindtest.EventUnlockedHint, logindtest.EventInhibit,
	})
}

func TestALockScreenThatNeverConfirmsDoesNotHoldSleepForever(t *testing.T) {
	fake := startSleepLock(t, 300*time.Millisecond)
	if err := fake.PrepareForSleep(true); err != nil {
		t.Fatal(err)
	}
	awaitEvents(t, fake, []string{logindtest.EventInhibit, logindtest.EventLock, logindtest.EventInhibitorReleased})
	if fake.LockedHint.Load() {
		t.Error("LockedHint set without a lock screen")
	}
}

func TestStopLetsGoOfTheInhibitor(t *testing.T) {
	fake := logindtest.Start(t, display)
	conn := fake.Bus.Conn(t)
	ctx, cancel := context.WithTimeout(context.Background(), logind.CallTimeout)
	defer cancel()
	session, err := logind.NewClient(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := LockBeforeSleep(conn, session, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	awaitEvents(t, fake, []string{logindtest.EventInhibit, logindtest.EventInhibitorReleased})
	if err := fake.PrepareForSleep(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := fake.Events.Load(); slices.Contains(got, logindtest.EventLock) {
		t.Errorf("a stopped lock-before-sleep still locked: %v", got)
	}
}
