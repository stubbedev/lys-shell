package idleinhibit

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func waitChange(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no change tick within a second")
	}
}

func TestStateEnableTimed(t *testing.T) {
	s := NewState(minutes(t, 2))
	changes, stop := s.Changes()
	defer stop()
	if s.Active() || s.Indefinite() {
		t.Fatal("a fresh state starts active")
	}
	s.Enable(false)
	if !s.Active() || s.Indefinite() {
		t.Fatalf("Enable(false) = active=%v indefinite=%v", s.Active(), s.Indefinite())
	}
	if got := s.Remaining(); got != 120 {
		t.Errorf("remaining = %d, want 120", got)
	}
	waitChange(t, changes)

	// The countdown runs and fires at zero, disabling the run: jump the
	// remaining time to its last second (white-box: the field lives in
	// this package) and let it tick out.
	s.mu.Lock()
	s.remainingSecs = 1
	s.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for s.Active() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Active() {
		t.Fatal("the timer never fired")
	}
	if got := s.Duration().Minutes(); got != 2 {
		t.Errorf("duration survived the run: %d", got)
	}
}

func TestStateEnableIndefinite(t *testing.T) {
	s := NewState(minutes(t, 0))
	s.Enable(true)
	if !s.Active() || !s.Indefinite() {
		t.Fatalf("Enable(true) = active=%v indefinite=%v", s.Active(), s.Indefinite())
	}
	// Indefinite mode ignores timer adjustments.
	s.AdjustRemaining(5)
	if got := s.Remaining(); got != 0 {
		t.Errorf("AdjustRemaining in indefinite mode set remaining to %d", got)
	}
	s.SetRemaining(5)
	if got := s.Remaining(); got != 0 {
		t.Errorf("SetRemaining in indefinite mode set remaining to %d", got)
	}
	s.Disable()
	if s.Active() {
		t.Error("Disable did not stop the run")
	}
}

func TestStateDurationAdjustments(t *testing.T) {
	s := NewState(minutes(t, 60))
	if err := s.AdjustDuration(-90); err != nil {
		t.Fatal(err)
	}
	if got := s.Duration().Minutes(); got != 0 {
		t.Errorf("negative clamp = %d, want 0", got)
	}
	s.SetDuration(minutes(t, 30))
	if err := s.AdjustDuration(15); err != nil {
		t.Fatal(err)
	}
	if got := s.Duration().Minutes(); got != 45 {
		t.Errorf("duration = %d, want 45", got)
	}
}

func TestStateRemainingGuards(t *testing.T) {
	s := NewState(minutes(t, 5))
	s.AdjustRemaining(1)
	if s.Active() || s.Remaining() != 0 {
		t.Errorf("inactive AdjustRemaining changed state: active=%v remaining=%d", s.Active(), s.Remaining())
	}
	s.Enable(false)
	if got := s.Remaining(); got != 300 {
		t.Fatalf("remaining = %d, want 300", got)
	}
	// Negative deltas subtract; saturating at zero disables the run.
	s.AdjustRemaining(-4)
	if got := s.Remaining(); got != 60 {
		t.Errorf("remaining after -4min = %d, want 60", got)
	}
	// Positive deltas cap at the full duration.
	s.AdjustRemaining(100)
	if got := s.Remaining(); got != 300 {
		t.Errorf("remaining after +100min = %d, want the 300 cap", got)
	}
	// Draining the remaining time disables the run.
	s.AdjustRemaining(-5)
	if s.Active() {
		t.Error("draining the remaining time did not disable the run")
	}
	// SetRemaining caps at the duration and zero disables.
	s.Enable(false)
	s.SetRemaining(99)
	if got := s.Remaining(); got != 300 {
		t.Errorf("SetRemaining above the duration = %d, want the cap", got)
	}
	s.SetRemaining(2)
	if got := s.Remaining(); got != 120 {
		t.Errorf("SetRemaining(2) = %d, want 120", got)
	}
	s.SetRemaining(0)
	if s.Active() {
		t.Error("SetRemaining(0) did not disable the run")
	}
	// SetDuration on a live run restarts it.
	s.Enable(false)
	s.SetDuration(minutes(t, 3))
	if got := s.Remaining(); got != 180 {
		t.Errorf("remaining after SetDuration = %d, want 180", got)
	}
	// And to indefinite stops the timer outright.
	s.SetDuration(minutes(t, 0))
	if got := s.Remaining(); got != 0 {
		t.Errorf("remaining after SetDuration(0) = %d, want 0", got)
	}
	s.Disable()
}

func TestStateTimerFiresToZero(t *testing.T) {
	s := NewState(minutes(t, 1))
	s.Enable(false)
	// Jump to the last second; the tick takes it to zero and disables.
	s.mu.Lock()
	s.remainingSecs = 1
	s.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for s.Active() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Active() {
		t.Fatal("the timer never fired")
	}
	if got := s.Duration().Minutes(); got != 1 {
		t.Errorf("duration survived the run: %d", got)
	}
}

func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		secs uint32
		want string
	}{
		{0, "0:00"},
		{59, "0:59"},
		{60, "1:00"},
		{3661, "1:01:01"},
		{3600, "1:00:00"},
		{7325, "2:02:05"},
	} {
		if got := FormatDuration(tc.secs); got != tc.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}

// An app's hold inhibits alongside the user's toggle without changing
// it; idle stays inhibited until both are gone.
func TestStateHoldsInhibitAlongsideTheToggle(t *testing.T) {
	s := NewState(minutes(t, 0))
	if s.Inhibiting() {
		t.Fatal("a fresh state inhibits")
	}
	a := s.AddHold(Hold{Owner: ":1.5", App: "org.app", Reason: "playing"})
	b := s.AddHold(Hold{Owner: ":1.6", App: "org.other"})
	if !s.Inhibiting() || s.Active() {
		t.Errorf("held: inhibiting %v, user active %v", s.Inhibiting(), s.Active())
	}
	s.Enable(true)
	s.Disable()
	if !s.Inhibiting() {
		t.Error("the user's toggle off dropped the apps' holds")
	}
	if !s.ReleaseHold(a) || s.ReleaseHold(a) {
		t.Error("a hold released twice, or not at all")
	}
	s.ReleaseOwner(":1.6")
	if s.Inhibiting() || len(s.Holds()) != 0 || s.ReleaseHold(b) {
		t.Errorf("after releasing everything: inhibiting %v, holds %v", s.Inhibiting(), s.Holds())
	}
}

// Over the bus a hold belongs to its caller: only it releases the hold,
// and its holds go when it leaves the bus.
func TestDaemonHoldsFollowTheirCaller(t *testing.T) {
	bus := dbustest.Start(t)
	state := NewState(minutes(t, 0))
	release, err := NewDaemon(state).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	portal := bus.Conn(t)
	other := bus.Conn(t)
	call := func(c *dbus.Conn, method string, args ...any) *dbus.Call {
		return c.Object(ServiceName, ServicePath).Call(ServiceName+"."+method, 0, args...)
	}
	var id uint32
	if err := call(portal, "Hold", "org.app", "playing").Store(&id); err != nil || id == 0 {
		t.Fatalf("Hold = %d, %v", id, err)
	}
	if h := state.Holds()[id]; h.App != "org.app" || h.Reason != "playing" || h.Owner != portal.Names()[0] {
		t.Errorf("hold = %+v", h)
	}
	if err := call(other, "Release", id).Err; err == nil {
		t.Error("another caller released the hold")
	}
	if err := call(portal, "Release", id).Err; err != nil || state.Inhibiting() {
		t.Errorf("Release = %v, inhibiting %v", err, state.Inhibiting())
	}
	if err := call(portal, "Hold", "org.app", "again").Store(&id); err != nil {
		t.Fatal(err)
	}
	_ = portal.Close()
	deadline := time.Now().Add(2 * time.Second)
	for state.Inhibiting() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if state.Inhibiting() {
		t.Error("a departed caller's hold kept idle inhibited")
	}
}

func minutes(t *testing.T, n int64) Duration {
	t.Helper()
	d, err := NewDuration(n)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func rangeMessage(minutes int64) string {
	return fmt.Sprintf("idle inhibit duration out of range: %d minutes, valid 0-71582788", minutes)
}

// The longest duration counts down its exact seconds, which still fit
// the uint32 the timer and the Remaining property hold.
func TestLongestDurationCountsDownItsExactSeconds(t *testing.T) {
	const wantSecs = 4294967280
	if got := minutes(t, MaxMinutes).Seconds(); got != wantSecs {
		t.Fatalf("Seconds() = %d, want %d, the last whole minute below 2^32", got, wantSecs)
	}
	s := NewState(minutes(t, MaxMinutes))
	s.Enable(false)
	defer s.Disable()
	if got := s.Remaining(); got != wantSecs {
		t.Errorf("remaining = %d, want %d", got, wantSecs)
	}
	s.AdjustRemaining(math.MaxInt32)
	if got := s.Remaining(); got != wantSecs {
		t.Errorf("remaining after +MaxInt32 min = %d, want the %d cap", got, wantSecs)
	}
	s.SetRemaining(math.MaxUint32)
	if got := s.Remaining(); got != wantSecs {
		t.Errorf("remaining after SetRemaining(MaxUint32) = %d, want the %d cap", got, wantSecs)
	}
	s.AdjustRemaining(-1)
	if got := s.Remaining(); got != wantSecs-60 {
		t.Errorf("remaining after -1 min = %d, want %d", got, wantSecs-60)
	}
}

// A duration past MaxMinutes, whose seconds would wrap a uint32, is
// refused with ErrDurationRange, as is a negative one.
func TestDurationPastTheLimitIsRefused(t *testing.T) {
	for _, n := range []int64{MaxMinutes + 1, math.MaxUint32, math.MaxUint32 + 1, -1} {
		d, err := NewDuration(n)
		if !errors.Is(err, ErrDurationRange) || d != (Duration{}) {
			t.Errorf("NewDuration(%d) = %+v, %v; want ErrDurationRange", n, d, err)
			continue
		}
		if err.Error() != rangeMessage(n) {
			t.Errorf("NewDuration(%d) error = %q, want %q", n, err, rangeMessage(n))
		}
	}
}

// AdjustDuration saturates at zero below, and refuses a shift past
// MaxMinutes, keeping the run, instead of saturating or wrapping.
func TestAdjustDurationRefusesPastTheLimit(t *testing.T) {
	for _, c := range []struct {
		start int64
		delta int32
		want  uint32
	}{
		{30, 15, 45},
		{30, -10, 20},
		{5, -10, 0},
		{0, math.MinInt32, 0},
		{MaxMinutes - 1, 1, MaxMinutes},
		{MaxMinutes, -1, MaxMinutes - 1},
	} {
		s := NewState(minutes(t, c.start))
		if err := s.AdjustDuration(c.delta); err != nil {
			t.Errorf("%d%+d: %v", c.start, c.delta, err)
		}
		if got := s.Duration().Minutes(); got != c.want {
			t.Errorf("%d%+d: got %d, want %d", c.start, c.delta, got, c.want)
		}
	}
	for _, delta := range []int32{1, math.MaxInt32} {
		s := NewState(minutes(t, MaxMinutes))
		s.Enable(false)
		err := s.AdjustDuration(delta)
		want := rangeMessage(MaxMinutes + int64(delta))
		if !errors.Is(err, ErrDurationRange) || err.Error() != want {
			t.Errorf("%d%+d: err = %v, want %q", MaxMinutes, delta, err, want)
		}
		if got, left := s.Duration().Minutes(), s.Remaining(); got != MaxMinutes || left != MaxMinutes*60 {
			t.Errorf("%d%+d: the refused shift changed the run to %d min, %d s", MaxMinutes, delta, got, left)
		}
		s.Disable()
	}
}

// Over the bus the longest duration is taken and reported exactly, and
// one past it is refused with InvalidArgs, leaving the run.
func TestDaemonRefusesDurationsPastTheLimit(t *testing.T) {
	bus := dbustest.Start(t)
	state := NewState(Duration{})
	release, err := NewDaemon(state).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	obj := bus.Conn(t).Object(ServiceName, ServicePath)
	call := func(method string, args ...any) error {
		return obj.Call(ServiceName+"."+method, 0, args...).Err
	}
	prop := func(name string) uint32 {
		t.Helper()
		var v uint32
		if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, ServiceName, name).Store(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := call("SetDuration", uint32(MaxMinutes)); err != nil {
		t.Fatalf("SetDuration(MaxMinutes) = %v", err)
	}
	if err := call("Enable", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(state.Disable)
	if d, r := prop("Duration"), prop("Remaining"); d != MaxMinutes || r != MaxMinutes*60 {
		t.Errorf("Duration %d, Remaining %d; want %d, %d", d, r, MaxMinutes, MaxMinutes*60)
	}
	for _, c := range []struct {
		method string
		arg    any
		value  int64
	}{
		{"SetDuration", uint32(MaxMinutes + 1), MaxMinutes + 1},
		{"SetDuration", uint32(math.MaxUint32), math.MaxUint32},
		{"AdjustDuration", int32(1), MaxMinutes + 1},
	} {
		err := call(c.method, c.arg)
		var de dbus.Error
		if !errors.As(err, &de) || de.Name != "org.freedesktop.DBus.Error.InvalidArgs" || len(de.Body) != 1 || de.Body[0] != rangeMessage(c.value) {
			t.Errorf("%s(%v) = %v, want InvalidArgs %q", c.method, c.arg, err, rangeMessage(c.value))
		}
		if d, r := prop("Duration"), prop("Remaining"); d != MaxMinutes || r != MaxMinutes*60 {
			t.Errorf("%s(%v) changed the run to Duration %d, Remaining %d", c.method, c.arg, d, r)
		}
	}
}
