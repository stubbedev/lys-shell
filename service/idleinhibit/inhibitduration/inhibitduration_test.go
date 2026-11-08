package inhibitduration

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

func rangeMessage(minutes int64) string {
	return fmt.Sprintf("idle inhibit duration out of range: %d minutes, valid 0-71582788", minutes)
}

// A duration past MaxMinutes, whose seconds would wrap a uint32, is
// refused with ErrDurationRange, as is a negative one.
func TestDurationPastTheLimitIsRefused(t *testing.T) {
	for _, n := range []int64{MaxMinutes + 1, math.MaxUint32, math.MaxUint32 + 1, -1} {
		d, err := NewDuration(n)
		if !errors.Is(err, ErrDurationRange) || d != (Duration{}) {
			t.Errorf("NewDuration(%d) = %+v, %v; want ErrDurationRange", n, d, err)
			continue
		}
		if err.Error() != rangeMessage(n) {
			t.Errorf("NewDuration(%d) error = %q, want %q", n, err, rangeMessage(n))
		}
	}
}
