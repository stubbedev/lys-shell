package notifications

import (
	"time"

	"github.com/stubbedev/lys-desktop/service/notifications/notifypolicy"
)

// allows reports whether n pops up through do-not-disturb.
func allows(r notifypolicy.FocusRules, n *Notification) bool {
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
	rules      notifypolicy.FocusRules
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

var focusReasonNames = [...]string{"off", "manual", "until", "override", "scheduled", "sharing", "fullscreen"}

func (r FocusReason) String() string { return focusReasonNames[r] }
