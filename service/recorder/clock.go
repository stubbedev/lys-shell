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
