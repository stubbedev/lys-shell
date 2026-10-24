// Package fakeclock is a clock tests advance by hand: Now stands still
// and AfterFunc callbacks run only inside Advance, in due order, so a
// countdown's expiry is an assertion rather than a race against the
// scheduler.
package fakeclock

import (
	"sync"
	"time"
)

// Clock is the hand-driven clock; New starts it at a fixed instant.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
}

type timer struct {
	at   time.Time
	fn   func()
	done bool
}

// New is a clock standing at a fixed UTC instant.
func New() *Clock { return &Clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

// Now is the clock's current instant.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc arms fn to run once the clock is advanced d past now; stop
// disarms it, reporting whether it was still armed.
func (c *Clock) AfterFunc(d time.Duration, fn func()) (stop func() bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		armed := !t.done
		t.done = true
		return armed
	}
}

// Advance moves the clock d forward, running every timer due by then
// in due order (arming order breaks ties), each with Now at its due
// instant and outside the clock's lock, so a callback may arm more.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *timer
		for _, t := range c.timers {
			if !t.done && !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.timers = deleteDone(c.timers)
			c.mu.Unlock()
			return
		}
		next.done = true
		c.now = next.at
		c.mu.Unlock()
		next.fn()
	}
}

func deleteDone(timers []*timer) []*timer {
	kept := timers[:0]
	for _, t := range timers {
		if !t.done {
			kept = append(kept, t)
		}
	}
	return kept
}
