package notifications

import (
	"errors"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/fakeclock"
)

func critical() map[string]dbus.Variant {
	return map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(2))}
}

func popped(s *Service, id uint32) bool {
	for _, n := range s.Popups() {
		if n.ID == id {
			return true
		}
	}
	return false
}

func TestCriticalPassesDoNotDisturbOnlyWhenAllowed(t *testing.T) {
	for _, allow := range []bool{true, false} {
		s := newTestService(t)
		s.SetFocusRules(FocusRules{AllowCritical: allow})
		s.SetDND(true)
		alarm := s.NotifyHints("battery", 0, "", "Battery at 3%", "", nil, critical(), 0)
		chat := s.Notify("chat", 0, "", "hi", "", nil, 0)
		if popped(s, alarm) != allow {
			t.Errorf("allow-critical %v: critical popped up %v", allow, popped(s, alarm))
		}
		if popped(s, chat) {
			t.Errorf("allow-critical %v: a normal notification popped up in do-not-disturb", allow)
		}
		if s.Count() != 2 {
			t.Errorf("allow-critical %v: history holds %d, want both", allow, s.Count())
		}
	}
}

func TestAnAllowedAppPopsUpThroughDoNotDisturb(t *testing.T) {
	s := newTestService(t)
	s.SetFocusRules(FocusRules{AllowApps: []string{"org.gnome.Calendar", "*alarm*"}})
	s.SetDND(true)
	cal := s.Notify("org.gnome.Calendar", 0, "", "Standup", "", nil, 0)
	clock := s.NotifyHints("Clocks", 0, "", "Wake up", "", nil, map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant("org.gnome.alarm-clock")}, 0)
	other := s.Notify("chat", 0, "", "hi", "", nil, 0)
	if !popped(s, cal) || !popped(s, clock) {
		t.Errorf("allowed apps: calendar %v, alarm desktop entry %v", popped(s, cal), popped(s, clock))
	}
	if popped(s, other) {
		t.Error("an app no glob matches popped up")
	}
	s.SetFocusRules(FocusRules{AllowApps: []string{"*nothing-matches*"}})
	if popped(s, s.Notify("chat", 0, "", "again", "", nil, 0)) || !s.DND() {
		t.Error("a glob matching no app changed do-not-disturb")
	}
}

type countingClock struct {
	*fakeclock.Clock
	mu   sync.Mutex
	live int
	made int
}

func (c *countingClock) AfterFunc(d time.Duration, fn func()) func() bool {
	c.mu.Lock()
	c.live++
	c.made++
	c.mu.Unlock()
	var once sync.Once
	done := func() {
		once.Do(func() {
			c.mu.Lock()
			c.live--
			c.mu.Unlock()
		})
	}
	stop := c.Clock.AfterFunc(d, func() { done(); fn() })
	return func() bool { done(); return stop() }
}

func (c *countingClock) counts() (live, made int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live, c.made
}

func scheduledService(t *testing.T, start time.Time, loc *time.Location, windows ...Window) (*Service, *countingClock) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fc := fakeclock.New()
	fc.Advance(start.Sub(fc.Now()))
	clock := &countingClock{Clock: fc}
	s := NewService()
	s.UseClock(clock)
	s.UseLocation(loc)
	s.SetFocusRules(FocusRules{Schedule: windows})
	return s, clock
}

func everyDay() DaySet {
	var d DaySet
	for day := time.Sunday; day <= time.Saturday; day++ {
		d = d.With(day)
	}
	return d
}

func mustWindow(t *testing.T, days DaySet, from, to string) Window {
	t.Helper()
	f, err := ParseClockTime(from)
	if err != nil {
		t.Fatal(err)
	}
	tt, err := ParseClockTime(to)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWindow(days, f, tt)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestAScheduleCrossesMidnightAndADaylightSavingChange(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 28, 21, 0, 0, 0, berlin)
	s, clock := scheduledService(t, start, berlin, mustWindow(t, everyDay(), "22:00", "07:00"))
	step := func(to time.Time, want bool) {
		t.Helper()
		clock.Advance(to.Add(-time.Minute).Sub(clock.Now()))
		if s.DND() == want {
			t.Fatalf("a minute before %s do-not-disturb is already %v", to, want)
		}
		clock.Advance(time.Minute)
		f := s.Focus()
		if f.On != want {
			t.Fatalf("at %s do-not-disturb is %v, want %v", to, f.On, want)
		}
		if live, _ := clock.counts(); live != 1 {
			t.Fatalf("at %s %d timers armed, want exactly one", to, live)
		}
	}
	on := time.Date(2026, 3, 28, 22, 0, 0, 0, berlin)
	step(on, true)
	if f := s.Focus(); f.Reason != FocusScheduled || !f.Until.Equal(time.Date(2026, 3, 29, 7, 0, 0, 0, berlin)) {
		t.Fatalf("scheduled focus = %+v", f)
	}
	off := time.Date(2026, 3, 29, 7, 0, 0, 0, berlin)
	if got := off.Sub(on); got != 8*time.Hour {
		t.Fatalf("the night the clocks go forward lasts %s, want 8h", got)
	}
	step(off, false)
	step(time.Date(2026, 3, 29, 22, 0, 0, 0, berlin), true)
	step(time.Date(2026, 3, 30, 7, 0, 0, 0, berlin), false)
	if _, made := clock.counts(); made > 6 {
		t.Errorf("%d timers armed over four boundaries: polling", made)
	}
}

func TestADayOffScheduleStaysOff(t *testing.T) {
	var weekdays DaySet
	for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday} {
		weekdays = weekdays.With(d)
	}
	saturday := time.Date(2026, 3, 28, 21, 0, 0, 0, time.UTC)
	s, clock := scheduledService(t, saturday, time.UTC, mustWindow(t, weekdays, "22:00", "07:00"))
	clock.Advance(2 * time.Hour)
	if s.DND() {
		t.Fatal("a weekday schedule turned on on a Saturday night")
	}
	clock.Advance(time.Date(2026, 3, 30, 22, 0, 0, 0, time.UTC).Sub(clock.Now()))
	if !s.DND() {
		t.Fatal("the Monday window never started")
	}
}

func TestATimerIsArmedOnlyForTheNextBoundary(t *testing.T) {
	s, clock := scheduledService(t, time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC), time.UTC, mustWindow(t, everyDay(), "22:00", "07:00"))
	if live, made := clock.counts(); live != 1 || made > 2 {
		t.Fatalf("after setup %d live, %d made", live, made)
	}
	_, before := clock.counts()
	clock.Advance(9*time.Hour + 59*time.Minute)
	if _, made := clock.counts(); made != before {
		t.Errorf("%d timers armed between boundaries", made-before)
	}
	s.SetFocusRules(FocusRules{})
	if live, _ := clock.counts(); live != 0 {
		t.Errorf("no schedule left %d timers armed", live)
	}
}

func TestSharingTurnsDoNotDisturbOnAndKeepsTheManualState(t *testing.T) {
	for _, manual := range []bool{false, true} {
		s := newTestService(t)
		s.SetFocusRules(FocusRules{WhileSharing: true})
		s.SetDND(manual)
		s.SetSharing(true)
		if f := s.Focus(); !f.On || f.Reason != FocusSharing {
			t.Fatalf("manual %v: sharing focus = %+v", manual, f)
		}
		s.SetSharing(false)
		if s.DND() != manual {
			t.Errorf("manual %v: after the share do-not-disturb is %v", manual, s.DND())
		}
	}
	s := newTestService(t)
	s.SetFocusRules(FocusRules{})
	s.SetSharing(true)
	if s.DND() {
		t.Error("sharing turned do-not-disturb on without while-sharing")
	}
}

func TestTheManualToggleOverridesARuleUntilItsNextBoundary(t *testing.T) {
	s, clock := scheduledService(t, time.Date(2026, 1, 5, 23, 0, 0, 0, time.UTC), time.UTC, mustWindow(t, everyDay(), "22:00", "07:00"))
	if !s.DND() {
		t.Fatal("not on inside the window")
	}
	s.SetDND(false)
	if f := s.Focus(); f.On || f.Reason != FocusOverride {
		t.Fatalf("overridden focus = %+v", f)
	}
	clock.Advance(8 * time.Hour)
	if s.DND() {
		t.Fatal("the override outlived the boundary")
	}
	clock.Advance(15 * time.Hour)
	if !s.DND() {
		t.Fatal("the next window did not turn on after an override")
	}
}

func TestDoNotDisturbUntilATime(t *testing.T) {
	s := newTestService(t)
	s.SetFocusRules(FocusRules{})
	until := testClock(s).Now().Add(time.Hour)
	s.SetDNDUntil(until)
	if f := s.Focus(); !f.On || f.Reason != FocusUntil || !f.Until.Equal(until) {
		t.Fatalf("until focus = %+v", f)
	}
	advance(s, time.Hour)
	if s.DND() {
		t.Fatal("still on after its time")
	}
}

func TestScheduleWindowsAndTimesAreValidated(t *testing.T) {
	for _, raw := range []string{"24:00", "7:00", "07:60", "0700", "ab:cd"} {
		if _, err := ParseClockTime(raw); !errors.Is(err, ErrClockTime) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	at, err := ParseClockTime("07:00")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWindow(everyDay(), at, at); !errors.Is(err, ErrEmptyWindow) {
		t.Errorf("empty window: %v", err)
	}
}

func TestParseUntilTakesATimeOfDayOrADuration(t *testing.T) {
	now := time.Date(2026, 3, 2, 10, 30, 0, 0, time.UTC)
	for raw, want := range map[string]time.Time{
		"11:00": time.Date(2026, 3, 2, 11, 0, 0, 0, time.UTC),
		"09:00": time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		"10:30": time.Date(2026, 3, 3, 10, 30, 0, 0, time.UTC),
		"1h":    now.Add(time.Hour),
		"45m":   now.Add(45 * time.Minute),
	} {
		got, err := ParseUntil(raw, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q = %s, %v; want %s", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "soon", "-1h", "0s", "25:00"} {
		if _, err := ParseUntil(raw, now); !errors.Is(err, ErrUntil) {
			t.Errorf("%q: %v, want ErrUntil", raw, err)
		}
	}
}
