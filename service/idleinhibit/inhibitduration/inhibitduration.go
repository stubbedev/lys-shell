package inhibitduration

import (
	"errors"
	"fmt"
	"math"
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
