package idle

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"
)

const display logind.SessionID = "c1"

func startLogin(t *testing.T) (*logindtest.Logind, Login) {
	t.Helper()
	fake := logindtest.Start(t, logindtest.Config{Display: display})
	m := logind.NewManager(fake.Bus.Conn(t))
	session, err := m.GetSession(t.Context(), logind.AutoSession)
	if err != nil {
		t.Fatal(err)
	}
	return fake, Login{Manager: m, Session: session}
}

func startSleepLock(t *testing.T, lockWait time.Duration) *logindtest.Logind {
	t.Helper()
	fake, login := startLogin(t)
	stop, err := LockBeforeSleep(login, lockWait)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if n := fake.Inhibitors.Load(); n != 1 {
		t.Fatalf("%d sleep inhibitors held after start, want 1", n)
	}
	return fake
}

func awaitEvents(t *testing.T, fake *logindtest.Logind, want []logindtest.Event) {
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
		fake.SetLockedHint(t, display, true)
	})
	fake.PrepareForSleep(t, true)
	awaitEvents(t, fake, []logindtest.Event{
		logindtest.EventInhibit, logindtest.EventLock, logindtest.EventLockedHint, logindtest.EventInhibitorReleased,
	})
	if n := fake.Inhibitors.Load(); n != 0 {
		t.Errorf("%d inhibitors held while asleep", n)
	}
	fake.SetLockedHint(t, display, false)
	fake.PrepareForSleep(t, false)
	awaitEvents(t, fake, []logindtest.Event{
		logindtest.EventInhibit, logindtest.EventLock, logindtest.EventLockedHint, logindtest.EventInhibitorReleased,
		logindtest.EventUnlockedHint, logindtest.EventInhibit,
	})
}

func TestALockScreenThatNeverConfirmsDoesNotHoldSleepForever(t *testing.T) {
	fake := startSleepLock(t, 300*time.Millisecond)
	fake.PrepareForSleep(t, true)
	awaitEvents(t, fake, []logindtest.Event{logindtest.EventInhibit, logindtest.EventLock, logindtest.EventInhibitorReleased})
	if slices.Contains(fake.Events.Load(), logindtest.EventLockedHint) {
		t.Error("LockedHint set without a lock screen")
	}
}

func TestStopLetsGoOfTheInhibitor(t *testing.T) {
	fake, login := startLogin(t)
	stop, err := LockBeforeSleep(login, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	awaitEvents(t, fake, []logindtest.Event{logindtest.EventInhibit, logindtest.EventInhibitorReleased})
	fake.PrepareForSleep(t, true)
	time.Sleep(100 * time.Millisecond)
	if got := fake.Events.Load(); slices.Contains(got, logindtest.EventLock) {
		t.Errorf("a stopped lock-before-sleep still locked: %v", got)
	}
}

func TestSystemLocksAndSuspendsThroughLogind(t *testing.T) {
	fake, login := startLogin(t)
	s := System{Login: &login}
	if err := s.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	want := []logindtest.Event{logindtest.EventLock, logindtest.EventSuspend}
	if got := fake.Events.Load(); !slices.Equal(got, want) {
		t.Errorf("logind saw %v, want %v", got, want)
	}
	if got := fake.CallsTo("Session.Lock"); len(got) != 1 || got[0].Path != logind.SessionPath(display) {
		t.Errorf("Session.Lock calls %v, want one on %s", got, logind.SessionPath(display))
	}
	if err := (System{}).Lock(); !errors.Is(err, ErrNoLogind) {
		t.Errorf("Lock without logind: %v, want ErrNoLogind", err)
	}
	if err := (System{}).Suspend(); !errors.Is(err, ErrNoLogind) {
		t.Errorf("Suspend without logind: %v, want ErrNoLogind", err)
	}
}
