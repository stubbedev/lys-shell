package notifications

import (
	"time"

	"github.com/stubbedev/lys-desktop/service/notifications/notifypolicy"
)

// Verdict is what do-not-disturb lets a notification do.
type Verdict uint8

// The verdicts.
const (
	// Popup shows the notification (and plays its sound).
	Popup Verdict = iota
	// HistoryOnly keeps it in the history, silent and unseen.
	HistoryOnly
)

// Verdict is what do-not-disturb lets n do now.
func (s *Service) Verdict(n *Notification) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdictLocked(n)
}

func (s *Service) verdictLocked(n *Notification) Verdict {
	if !s.focusLocked().On || allows(s.focus.rules, n) {
		return Popup
	}
	return HistoryOnly
}

func (s *Service) nowLocal() time.Time { return s.clock.Now().In(s.focusLoc) }

func (s *Service) focusLocked() Focus { return s.focus.evaluate(s.nowLocal()) }

// DND reports whether do-not-disturb is on, by hand or by a rule.
func (s *Service) DND() bool { return s.Focus().On }

// Focus is the do-not-disturb state with why it is on and until when.
func (s *Service) Focus() Focus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focusLocked()
}

// SetDND turns do-not-disturb on or off by hand. While a rule holds it on
// (a schedule, a share, a fullscreen window), the choice overrides the
// rules until their state next changes; otherwise it is the sticky manual
// state, persisted across restarts.
func (s *Service) SetDND(on bool) {
	s.mu.Lock()
	now := s.nowLocal()
	if s.focus.automatic(now) {
		until, _ := s.focus.rules.NextBoundary(now)
		s.focus.override = &focusOverride{on: on, until: until}
	} else {
		s.focus.override = nil
		s.focus.manual = on
		s.saveDNDLocked(on)
	}
	s.refocusUnlock(true)
}

// ToggleDND flips do-not-disturb as SetDND does, returning the new state.
func (s *Service) ToggleDND() bool {
	s.mu.Lock()
	on := !s.focusLocked().On
	s.mu.Unlock()
	s.SetDND(on)
	return on
}

// SetDNDUntil turns do-not-disturb on until until, after which the rules
// and the manual state apply again.
func (s *Service) SetDNDUntil(until time.Time) {
	s.mu.Lock()
	s.focus.override = &focusOverride{on: true, until: until}
	s.refocusUnlock(true)
}

// SetFocusRules replaces the do-not-disturb rules.
func (s *Service) SetFocusRules(r notifypolicy.FocusRules) {
	s.mu.Lock()
	s.focus.rules = r
	s.refocusUnlock(false)
}

// SetSharing tells whether the screen is shared or recorded. A change
// ends an override that lasted until the rules changed.
func (s *Service) SetSharing(on bool) {
	s.mu.Lock()
	if s.focus.sharing == on {
		s.mu.Unlock()
		return
	}
	s.focus.sharing = on
	s.endOpenOverrideLocked()
	s.refocusUnlock(false)
}

// SetFullscreen tells whether a window is fullscreen. A change ends an
// override that lasted until the rules changed.
func (s *Service) SetFullscreen(on bool) {
	s.mu.Lock()
	if s.focus.fullscreen == on {
		s.mu.Unlock()
		return
	}
	s.focus.fullscreen = on
	s.endOpenOverrideLocked()
	s.refocusUnlock(false)
}

// ClockJumped re-evaluates the schedule after the wall clock jumped (a
// resume, a step, a time-zone change).
func (s *Service) ClockJumped() {
	s.mu.Lock()
	s.refocusUnlock(false)
}

// UseLocation evaluates the schedule in loc instead of the local zone.
func (s *Service) UseLocation(loc *time.Location) {
	s.mu.Lock()
	s.focusLoc = loc
	s.refocusUnlock(false)
}

func (s *Service) endOpenOverrideLocked() {
	if s.focus.override != nil && s.focus.override.until.IsZero() {
		s.focus.override = nil
	}
}

func (s *Service) focusWake() {
	s.mu.Lock()
	if o := s.focus.override; o != nil && !o.until.IsZero() && !s.nowLocal().Before(o.until) {
		s.focus.override = nil
	}
	s.focusStop = nil
	s.refocusUnlock(false)
}

func (s *Service) refocusUnlock(always bool) {
	if s.focusStop != nil {
		s.focusStop()
		s.focusStop = nil
	}
	now := s.nowLocal()
	if wake, ok := s.focus.wake(now); ok {
		s.focusStop = s.clock.AfterFunc(wake.Sub(now), s.focusWake)
	}
	f := s.focus.evaluate(now)
	changed := f != s.focusShown
	s.focusShown = f
	s.mu.Unlock()
	if changed || always {
		s.notify(Event{Kind: EventDnd})
	}
}

// Audible reports whether n may make a sound now: do-not-disturb lets it
// pop up and the screen is not shared or recorded, whatever the rules
// say about popups while sharing.
func (s *Service) Audible(n *Notification) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdictLocked(n) == Popup && !s.focus.sharing
}
