package notifications

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// DaySet is a set of weekdays.
type DaySet uint8

// Has reports whether d is in the set.
func (s DaySet) Has(d time.Weekday) bool { return s&(1<<d) != 0 }

// With is the set with d added.
func (s DaySet) With(d time.Weekday) DaySet { return s | 1<<d }

// ClockTime is a time of day to the minute.
type ClockTime struct{ minute int }

// ErrClockTime is a time of day that is not HH:MM within a day.
var ErrClockTime = errors.New("not a time of day as HH:MM")

// ParseClockTime reads HH:MM, 00:00 to 23:59.
func ParseClockTime(raw string) (ClockTime, error) {
	if len(raw) != 5 || raw[2] != ':' {
		return ClockTime{}, fmt.Errorf("%q: %w", raw, ErrClockTime)
	}
	h, herr := strconv.Atoi(raw[:2])
	m, merr := strconv.Atoi(raw[3:])
	if herr != nil || merr != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return ClockTime{}, fmt.Errorf("%q: %w", raw, ErrClockTime)
	}
	return ClockTime{minute: h*60 + m}, nil
}

func (c ClockTime) String() string {
	return fmt.Sprintf("%02d:%02d", c.minute/60, c.minute%60)
}

func (c ClockTime) on(day time.Time) time.Time {
	y, mo, d := day.Date()
	return time.Date(y, mo, d, c.minute/60, c.minute%60, 0, 0, day.Location())
}

// ErrEmptyWindow is a schedule window that starts when it ends.
var ErrEmptyWindow = errors.New("a do-not-disturb window must end at another time than it starts")

// Window is one scheduled stretch of do-not-disturb: from From to To on
// each of Days, running past midnight into the next day when To is not
// after From.
type Window struct {
	days     DaySet
	from, to ClockTime
}

// NewWindow validates a window on days from from to to.
func NewWindow(days DaySet, from, to ClockTime) (Window, error) {
	if from == to {
		return Window{}, fmt.Errorf("%s-%s: %w", from, to, ErrEmptyWindow)
	}
	return Window{days: days, from: from, to: to}, nil
}

// Days are the days the window starts on.
func (w Window) Days() DaySet { return w.days }

// From is when the window starts.
func (w Window) From() ClockTime { return w.from }

// To is when the window ends.
func (w Window) To() ClockTime { return w.to }

func (w Window) overnight() bool { return w.to.minute <= w.from.minute }

func (w Window) activeAt(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	day := t.Weekday()
	if !w.overnight() {
		return w.days.Has(day) && m >= w.from.minute && m < w.to.minute
	}
	return (w.days.Has(day) && m >= w.from.minute) || (w.days.Has((day+6)%7) && m < w.to.minute)
}

// boundaries calls fn with each start and end of the window on the day
// of base, day days later.
func (w Window) boundaries(base time.Time, day int, fn func(time.Time)) {
	date := base.AddDate(0, 0, day)
	if !w.days.Has(date.Weekday()) {
		return
	}
	fn(w.from.on(date))
	if w.overnight() {
		fn(w.to.on(date.AddDate(0, 0, 1)))
		return
	}
	fn(w.to.on(date))
}

// FocusRules are the do-not-disturb rules: what still pops up while it is
// on, and what turns it on by itself.
type FocusRules struct {
	// AllowCritical lets critical notifications pop up through it.
	AllowCritical bool
	// AllowApps are app-name or desktop-entry globs that pop up through it.
	AllowApps []string
	// Schedule turns it on within each window, in the local time zone.
	Schedule []Window
	// WhileSharing turns it on while the screen is shared or recorded.
	WhileSharing bool
	// WhileFullscreen turns it on while a window is fullscreen.
	WhileFullscreen bool
}

// Scheduled reports whether t falls in a schedule window.
func (r FocusRules) Scheduled(t time.Time) bool {
	for _, w := range r.Schedule {
		if w.activeAt(t) {
			return true
		}
	}
	return false
}

// NextBoundary is the first start or end of a schedule window after t;
// false without a schedule.
func (r FocusRules) NextBoundary(t time.Time) (time.Time, bool) {
	var next time.Time
	for day := -1; day <= 7; day++ {
		for _, w := range r.Schedule {
			w.boundaries(t, day, func(b time.Time) {
				if b.After(t) && (next.IsZero() || b.Before(next)) {
					next = b
				}
			})
		}
	}
	return next, !next.IsZero()
}

// allows reports whether n pops up through do-not-disturb.
func (r FocusRules) allows(n *Notification) bool {
	if r.AllowCritical && n.Urgency == UrgencyCritical {
		return true
	}
	for _, pattern := range r.AllowApps {
		if globMatch(pattern, n.AppName) || (n.DesktopEntry != "" && globMatch(pattern, n.DesktopEntry)) {
			return true
		}
	}
	return false
}

// FocusReason is why do-not-disturb is on or off.
type FocusReason uint8

// The reasons.
const (
	FocusOff FocusReason = iota
	FocusManual
	FocusUntil
	FocusOverride
	FocusScheduled
	FocusSharing
	FocusFullscreen
)

// Focus is the do-not-disturb state as the shell shows it: on or off, why,
// and until when (zero when it lasts until something else changes).
type Focus struct {
	On     bool
	Reason FocusReason
	Until  time.Time
}

type focusOverride struct {
	on    bool
	until time.Time
}

// focusInputs is what the do-not-disturb state follows.
type focusInputs struct {
	rules      FocusRules
	manual     bool
	override   *focusOverride
	sharing    bool
	fullscreen bool
}

func (in focusInputs) automatic(now time.Time) bool {
	return (in.rules.WhileSharing && in.sharing) || (in.rules.WhileFullscreen && in.fullscreen) || in.rules.Scheduled(now)
}

func (in focusInputs) overrideAt(now time.Time) (focusOverride, bool) {
	if in.override == nil || (!in.override.until.IsZero() && !now.Before(in.override.until)) {
		return focusOverride{}, false
	}
	return *in.override, true
}

// evaluate is the do-not-disturb state at now.
func (in focusInputs) evaluate(now time.Time) Focus {
	if o, ok := in.overrideAt(now); ok {
		reason := FocusOverride
		if o.on {
			reason = FocusUntil
		}
		return Focus{On: o.on, Reason: reason, Until: o.until}
	}
	switch {
	case in.rules.WhileSharing && in.sharing:
		return Focus{On: true, Reason: FocusSharing}
	case in.rules.WhileFullscreen && in.fullscreen:
		return Focus{On: true, Reason: FocusFullscreen}
	case in.rules.Scheduled(now):
		until, _ := in.rules.NextBoundary(now)
		return Focus{On: true, Reason: FocusScheduled, Until: until}
	case in.manual:
		return Focus{On: true, Reason: FocusManual}
	}
	return Focus{}
}

// wake is when the state must be evaluated again: the next schedule
// boundary or the end of an override, whichever comes first.
func (in focusInputs) wake(now time.Time) (time.Time, bool) {
	next, ok := in.rules.NextBoundary(now)
	if o, live := in.overrideAt(now); live && !o.until.IsZero() && (!ok || o.until.Before(next)) {
		return o.until, true
	}
	return next, ok
}

// ErrUntil is a --until that is neither a time of day nor a duration.
var ErrUntil = errors.New("want a time of day as HH:MM or a duration such as 30m or 1h")

// ParseUntil reads when a do-not-disturb ends, from now: a time of day
// (HH:MM, its next occurrence in now's zone) or a positive duration.
func ParseUntil(raw string, now time.Time) (time.Time, error) {
	if at, err := ParseClockTime(raw); err == nil {
		until := at.on(now)
		if !until.After(now) {
			until = at.on(now.AddDate(0, 0, 1))
		}
		return until, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("until %q: %w", raw, ErrUntil)
	}
	return now.Add(d), nil
}

var focusReasonNames = [...]string{"off", "manual", "until", "override", "scheduled", "sharing", "fullscreen"}

func (r FocusReason) String() string { return focusReasonNames[r] }
