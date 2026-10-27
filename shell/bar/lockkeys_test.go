package bar

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/keytoggles"
	"github.com/stubbedev/lys-shell/service/lyswm"
)

func TestLockChangesReportsEachChangedKeyWithItsNewState(t *testing.T) {
	for _, c := range []struct {
		name       string
		prev, next lyswm.Locks
		want       []keytoggles.Event
	}{
		{"caps on", lyswm.Locks{}, lyswm.Locks{Caps: true}, []keytoggles.Event{{Key: keytoggles.CapsLock, Active: true}}},
		{"num off", lyswm.Locks{Num: true}, lyswm.Locks{}, []keytoggles.Event{{Key: keytoggles.NumLock}}},
		{"scroll on beside an unchanged num", lyswm.Locks{Num: true}, lyswm.Locks{Num: true, Scroll: true}, []keytoggles.Event{{Key: keytoggles.ScrollLock, Active: true}}},
		{"all three", lyswm.Locks{Caps: true}, lyswm.Locks{Num: true, Scroll: true}, []keytoggles.Event{
			{Key: keytoggles.CapsLock},
			{Key: keytoggles.NumLock, Active: true},
			{Key: keytoggles.ScrollLock, Active: true},
		}},
		{"unchanged", lyswm.Locks{Caps: true, Num: true}, lyswm.Locks{Caps: true, Num: true}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := lockChanges(c.prev, c.next); !slices.Equal(got, c.want) {
				t.Errorf("lockChanges(%+v, %+v) = %+v, want %+v", c.prev, c.next, got, c.want)
			}
		})
	}
}

func TestLockKeySourceIsLysWMsOnLysWM(t *testing.T) {
	newFakeLysWM(t)
	if got := reflect.ValueOf(lockKeySource()).Pointer(); got != reflect.ValueOf(lysWMLockKeys).Pointer() {
		t.Error("on lys-wm the lock keys do not come from lys-wm")
	}
}

func TestLockKeySourceIsEvdevElsewhere(t *testing.T) {
	advertise(t, "SWAYSOCK")
	if got := reflect.ValueOf(lockKeySource()).Pointer(); got != reflect.ValueOf(evdevLockKeys).Pointer() {
		t.Error("off lys-wm the lock keys do not come from evdev")
	}
}

func startLysWMLockKeys(t *testing.T) <-chan keytoggles.Event {
	t.Helper()
	events := make(chan keytoggles.Event, 8)
	stop, err := lysWMLockKeys(func(e keytoggles.Event) { events <- e })
	if err != nil {
		t.Fatalf("lysWMLockKeys: %v", err)
	}
	t.Cleanup(stop)
	return events
}

func expectLockEvents(t *testing.T, events <-chan keytoggles.Event, want ...keytoggles.Event) {
	t.Helper()
	for _, w := range want {
		select {
		case got := <-events:
			if got != w {
				t.Errorf("lock key %+v, want %+v", got, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no lock key, want %+v", w)
		}
	}
	select {
	case got := <-events:
		t.Errorf("lock key %+v, want nothing more", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestLysWMLockKeysShowChangesNotTheSeed(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("locks", `{"caps":false,"num":true,"scroll":false}`)
	events := startLysWMLockKeys(t)
	f.AwaitWatch(t)
	expectLockEvents(t, events)
	f.Events <- `{"event":"locks","locks":{"caps":true,"num":true,"scroll":false}}`
	expectLockEvents(t, events, keytoggles.Event{Key: keytoggles.CapsLock, Active: true})
	f.Events <- `{"event":"locks","locks":{"caps":true,"num":false,"scroll":true}}`
	expectLockEvents(t, events,
		keytoggles.Event{Key: keytoggles.NumLock},
		keytoggles.Event{Key: keytoggles.ScrollLock, Active: true})
	if got := f.Requests(); !slices.Contains(got, `{"request":"locks"}`) {
		t.Errorf("requests = %q, want the locks seed", got)
	}
}

func TestLysWMLockKeysIgnoreOtherEvents(t *testing.T) {
	f := newFakeLysWM(t)
	events := startLysWMLockKeys(t)
	f.AwaitWatch(t)
	f.Events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	f.Events <- `{"event":"locks","locks":{"caps":false,"num":false,"scroll":false}}`
	expectLockEvents(t, events)
}

func TestLysWMLockKeysFailWithoutALocksSeed(t *testing.T) {
	for _, c := range []struct{ name, answer, want string }{
		{"refused", `{"version":1,"error":"unknown request"}`, "seed the locks: lys-wm rejected locks: unknown request"},
		{"empty", `{"version":1}`, "seed the locks: lys-wm: locks answered no result"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeLysWM(t)
			f.Answer("locks", c.answer)
			stop, err := lysWMLockKeys(func(keytoggles.Event) { t.Error("a failed source reported a lock key") })
			if err == nil {
				stop()
				t.Fatal("lysWMLockKeys succeeded without a locks seed")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}
