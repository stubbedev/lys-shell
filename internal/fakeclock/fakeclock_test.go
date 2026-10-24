package fakeclock

import (
	"slices"
	"testing"
	"time"
)

// Timers fire only inside Advance, in due order with Now at their due
// instant; a stopped or not-yet-due timer does not fire, and one armed
// by a callback fires in the same Advance when it falls due.
func TestAdvanceFiresDueTimersInOrder(t *testing.T) {
	c := New()
	start := c.Now()
	var fired []string
	at := func(name string) func() {
		return func() { fired = append(fired, name+"@"+c.Now().Sub(start).String()) }
	}
	c.AfterFunc(30*time.Millisecond, at("b"))
	c.AfterFunc(10*time.Millisecond, func() {
		at("a")()
		c.AfterFunc(5*time.Millisecond, at("chained"))
	})
	stop := c.AfterFunc(20*time.Millisecond, at("stopped"))
	c.AfterFunc(time.Second, at("late"))
	if !stop() || stop() {
		t.Error("stop reports armed once, then disarmed")
	}
	if len(fired) != 0 {
		t.Fatalf("fired before Advance: %v", fired)
	}
	c.Advance(30 * time.Millisecond)
	if want := []string{"a@10ms", "chained@15ms", "b@30ms"}; !slices.Equal(fired, want) {
		t.Errorf("fired %v, want %v", fired, want)
	}
	if got := c.Now().Sub(start); got != 30*time.Millisecond {
		t.Errorf("now = start+%v after Advance(30ms)", got)
	}
}
