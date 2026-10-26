package recorder

import (
	"testing"
	"time"
)

// The clock runs from the start, stands still while paused, and goes
// on from where it stopped: a pause cuts its span. Pausing a paused
// clock or resuming a running one changes nothing.
func TestActiveClockCutsPauses(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	c := newActiveClock(t0)
	if got := c.elapsed(at(1500)); got != 1500*time.Millisecond {
		t.Errorf("running: %v", got)
	}
	if c.resume(at(1500)) {
		t.Error("resuming a running clock reported a change")
	}
	if !c.pause(at(2000)) {
		t.Error("pause reported no change")
	}
	if c.pause(at(2500)) {
		t.Error("pausing a paused clock reported a change")
	}
	if got := c.elapsed(at(9000)); got != 2*time.Second {
		t.Errorf("paused: %v, want frozen at 2s", got)
	}
	if !c.resume(at(5000)) {
		t.Error("resume reported no change")
	}
	if got := c.elapsed(at(6000)); got != 3*time.Second {
		t.Errorf("after a 3s pause: %v, want 3s", got)
	}
}
