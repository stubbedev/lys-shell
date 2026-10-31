package recorder

import "time"

// DriveElapsed replaces s's elapsed-seconds ticker with ticks, which
// the test sends on: an unbuffered send returns once the timer took
// the tick, so a second send proves the first was counted (or skipped).
func DriveElapsed(s *State, ticks <-chan time.Time) {
	s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
}
