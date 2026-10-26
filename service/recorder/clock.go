package recorder

import "time"

// activeClock is a recording's timeline: the time since it started,
// less the time spent paused. Video frames and audio packets are both
// stamped on it, so a pause cuts the same span from each.
type activeClock struct {
	start     time.Time
	paused    bool
	pausedAt  time.Time
	pausedFor time.Duration
}

func newActiveClock(start time.Time) activeClock { return activeClock{start: start} }

// pause stops the clock at now; false when it was already stopped.
func (c *activeClock) pause(now time.Time) bool {
	if c.paused {
		return false
	}
	c.paused, c.pausedAt = true, now
	return true
}

// resume runs the clock again from where pause stopped it; false when
// it was running.
func (c *activeClock) resume(now time.Time) bool {
	if !c.paused {
		return false
	}
	c.paused = false
	c.pausedFor += now.Sub(c.pausedAt)
	return true
}

// elapsed is the recorded time at now, frozen while paused.
func (c *activeClock) elapsed(now time.Time) time.Duration {
	if c.paused {
		now = c.pausedAt
	}
	return now.Sub(c.start) - c.pausedFor
}

// wallClock is the time a recording reads and the tickers that pace its
// loops: systemClock in production.
type wallClock interface {
	Now() time.Time
	// Ticker ticks every period until stop is called.
	Ticker(period time.Duration) (ticks <-chan time.Time, stop func())
}

// systemClock is the system's wall clock and tickers.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Ticker(period time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(period)
	return t.C, t.Stop
}
