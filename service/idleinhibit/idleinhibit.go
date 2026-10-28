// Package idleinhibit is the shared idle-inhibit service: reactive
// state (active, duration, remaining), the session-bus daemon the
// `lys-shell idle` CLI drives, and the client half of that interface.
package idleinhibit

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/internal/feed"
)

// D-Bus identity.
const (
	ServiceName = "dev.stubbe.lys.IdleInhibit1"
	ServicePath = "/dev/stubbe/lys/IdleInhibit"
)

// MaxMinutes is the longest duration: its seconds, which the timer
// counts and the Remaining property reports, still fit a uint32.
const MaxMinutes = math.MaxUint32 / 60

// ErrDurationRange is a duration outside 0-MaxMinutes.
var ErrDurationRange = errors.New("idle inhibit duration out of range")

// Duration is a timed inhibition's length in whole minutes, 0 for
// indefinite. NewDuration is the only way to a non-zero one, so it
// never exceeds MaxMinutes.
type Duration struct{ minutes uint32 }

// NewDuration validates minutes, refusing a negative count or one above
// MaxMinutes with ErrDurationRange.
func NewDuration(minutes int64) (Duration, error) {
	if minutes < 0 || minutes > MaxMinutes {
		return Duration{}, fmt.Errorf("%w: %d minutes, valid 0-%d", ErrDurationRange, minutes, MaxMinutes)
	}
	return Duration{minutes: uint32(minutes)}, nil
}

// Minutes is the length in minutes, 0 for indefinite.
func (d Duration) Minutes() uint32 { return d.minutes }

// Seconds is the length in seconds, 0 for indefinite.
func (d Duration) Seconds() uint32 { return d.minutes * 60 }

// Indefinite reports the zero duration, which has no timer.
func (d Duration) Indefinite() bool { return d.minutes == 0 }

// State is the reactive state: whether the user's inhibition runs,
// the stored duration in minutes (0 = indefinite), the seconds left on
// the timer when one is running, and the holds apps (through the
// desktop portal) keep alongside it.
type State struct {
	mu            sync.Mutex
	active        bool
	duration      Duration
	remainingSecs uint32
	timerStop     chan struct{}
	holds         map[uint32]Hold
	nextHold      uint32
	changes       feed.Tick
}

// Hold is one app's inhibition: who asked (an owner the daemon
// releases together when it leaves the bus), for which app, and why.
type Hold struct {
	Owner, App, Reason string
}

// NewState starts with the given duration and inhibition off.
func NewState(duration Duration) *State {
	return &State{duration: duration}
}

// Active reports whether inhibition is on.
func (s *State) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Duration returns the stored duration.
func (s *State) Duration() Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.duration
}

// Inhibiting reports whether idle must be inhibited: the user's
// inhibition is on, or an app holds one.
func (s *State) Inhibiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active || len(s.holds) > 0
}

// AddHold registers an app's inhibition, leaving the user's untouched,
// until the returned id is released.
func (s *State) AddHold(h Hold) uint32 {
	s.mu.Lock()
	if s.holds == nil {
		s.holds = map[uint32]Hold{}
	}
	s.nextHold++
	id := s.nextHold
	s.holds[id] = h
	s.mu.Unlock()
	s.notify()
	return id
}

// ReleaseHold drops one hold; false when id names none.
func (s *State) ReleaseHold(id uint32) bool {
	s.mu.Lock()
	_, ok := s.holds[id]
	delete(s.holds, id)
	s.mu.Unlock()
	if ok {
		s.notify()
	}
	return ok
}

// ReleaseOwner drops every hold owner took.
func (s *State) ReleaseOwner(owner string) {
	s.mu.Lock()
	n := len(s.holds)
	for id, h := range s.holds {
		if h.Owner == owner {
			delete(s.holds, id)
		}
	}
	dropped := len(s.holds) != n
	s.mu.Unlock()
	if dropped {
		s.notify()
	}
}

// Holds lists the apps' holds, by id.
func (s *State) Holds() map[uint32]Hold {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.holds)
}

// Remaining returns the seconds left, 0 when inactive or indefinite.
func (s *State) Remaining() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remainingSecs
}

// Indefinite reports the indefinite mode: active with no timer.
func (s *State) Indefinite() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && s.duration.Indefinite()
}

// Changes ticks after every state flip or timer second; every
// subscriber (the module on each output, the daemon) sees each one.
// stop ends the feed.
func (s *State) Changes() (<-chan struct{}, func()) { return s.changes.Subscribe() }

// notify ticks every subscriber, coalescing when one is pending.
func (s *State) notify() { feed.Notify(&s.changes) }

// Enable turns inhibition on. With indefinite the run has no timer;
// otherwise the stored duration (if any) counts down and auto-disables.
func (s *State) Enable(indefinite bool) {
	s.mu.Lock()
	s.stopTimerLocked()
	if !indefinite && !s.duration.Indefinite() {
		s.remainingSecs = s.duration.Seconds()
		s.startTimerLocked()
	} else {
		s.remainingSecs = 0
	}
	s.active = true
	s.mu.Unlock()
	s.notify()
}

// Disable turns inhibition off and clears the timer.
func (s *State) Disable() {
	s.mu.Lock()
	s.stopTimerLocked()
	s.active = false
	s.remainingSecs = 0
	s.mu.Unlock()
	s.notify()
}

// SetDuration stores the duration; an active run restarts on it.
func (s *State) SetDuration(duration Duration) {
	s.mu.Lock()
	s.setDurationLocked(duration)
	s.mu.Unlock()
	s.notify()
}

// setDurationLocked stores the duration and restarts an active run on
// it. The caller holds mu.
func (s *State) setDurationLocked(duration Duration) {
	s.duration = duration
	if !s.active {
		return
	}
	s.stopTimerLocked()
	s.remainingSecs = duration.Seconds()
	if !duration.Indefinite() {
		s.startTimerLocked()
	}
}

// AdjustDuration shifts the stored duration: below zero it saturates at
// zero (indefinite), above MaxMinutes it is refused with
// ErrDurationRange and the duration stays.
func (s *State) AdjustDuration(deltaMinutes int32) error {
	s.mu.Lock()
	next, err := NewDuration(max(int64(s.duration.minutes)+int64(deltaMinutes), 0))
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.setDurationLocked(next)
	s.mu.Unlock()
	s.notify()
	return nil
}

// AdjustRemaining shifts the seconds left by minutes: a positive
// delta caps at the stored duration, a negative one saturates at
// zero, and zero remaining disables the run. Inactive or indefinite
// runs ignore the call.
func (s *State) AdjustRemaining(deltaMinutes int32) {
	s.retime(func() uint32 {
		return min(intconv.Clamp[uint32](int64(s.remainingSecs)+int64(deltaMinutes)*60), s.duration.Seconds())
	})
}

// SetRemaining replaces the seconds left, capped at the stored
// duration; zero disables the run. Inactive or indefinite runs
// ignore the call.
func (s *State) SetRemaining(minutes uint32) {
	s.retime(func() uint32 {
		return min(intconv.Clamp[uint32](uint64(minutes)*60), s.duration.Seconds())
	})
}

// retime sets a timed run's seconds left to next, which runs under mu
// and stays within the stored duration; zero disables the run.
// Inactive or indefinite runs are left alone.
func (s *State) retime(next func() uint32) {
	s.mu.Lock()
	if !s.active || s.duration.Indefinite() {
		s.mu.Unlock()
		return
	}
	s.remainingSecs = next()
	if s.remainingSecs == 0 {
		s.stopTimerLocked()
		s.active = false
	}
	s.mu.Unlock()
	s.notify()
}

// startTimerLocked runs the one-second countdown that auto-disables
// at zero. The caller holds mu.
func (s *State) startTimerLocked() {
	if s.timerStop != nil {
		return
	}
	stop := make(chan struct{})
	s.timerStop = stop
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.timerStop != stop || !s.active || s.remainingSecs == 0 {
					s.mu.Unlock()
					return
				}
				s.remainingSecs--
				done := s.remainingSecs == 0
				if done {
					s.active = false
					s.remainingSecs = 0
					s.timerStop = nil
				}
				s.mu.Unlock()
				if done {
					s.notify()
					return
				}
				s.notify()
			}
		}
	}()
}

// stopTimerLocked cancels the countdown goroutine. The caller holds mu.
func (s *State) stopTimerLocked() {
	if s.timerStop != nil {
		close(s.timerStop)
		s.timerStop = nil
	}
}

// FormatDuration renders seconds as H:MM:SS above the hour and M:SS
// within it (helpers.rs's format_duration).
func FormatDuration(totalSecs uint32) string {
	hours := totalSecs / 3600
	minutes := (totalSecs % 3600) / 60
	seconds := totalSecs % 60
	if hours > 0 {
		return strconv.FormatUint(uint64(hours), 10) + ":" + pad2(minutes) + ":" + pad2(seconds)
	}
	return strconv.FormatUint(uint64(minutes), 10) + ":" + pad2(seconds)
}

func pad2(v uint32) string {
	if v < 10 {
		return "0" + strconv.FormatUint(uint64(v), 10)
	}
	return strconv.FormatUint(uint64(v), 10)
}

// Snapshot is the status view the CLI prints.
type Snapshot struct {
	Active     bool
	Duration   Duration
	RemainingS uint32
}

// Status reads the current state as a snapshot.
func (s *State) Status() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{Active: s.active, Duration: s.duration, RemainingS: s.remainingSecs}
}
