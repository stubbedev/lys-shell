// Package clock is the time source of components that arm countdowns:
// the wall clock in the shell, internal/fakeclock in tests.
package clock

import "time"

// Clock reads the time and arms one-shot timers.
type Clock interface {
	Now() time.Time
	// AfterFunc runs fn once d has passed; stop disarms it, reporting
	// whether it was still armed.
	AfterFunc(d time.Duration, fn func()) (stop func() bool)
}

// Wall is the system clock.
type Wall struct{}

// Now is the current time.
func (Wall) Now() time.Time { return time.Now() }

// AfterFunc runs fn on its own goroutine once d has passed.
func (Wall) AfterFunc(d time.Duration, fn func()) func() bool { return time.AfterFunc(d, fn).Stop }
