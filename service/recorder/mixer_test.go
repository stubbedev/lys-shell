package recorder

import (
	"testing"
	"time"
)

// Frames fall due every 20 ms of clock past the latency, less those
// already emitted; none before it.
func TestFramesDue(t *testing.T) {
	for _, c := range []struct {
		elapsed, latency time.Duration
		emitted, want    int
	}{
		{50 * time.Millisecond, 100 * time.Millisecond, 0, 0},
		{159 * time.Millisecond, 100 * time.Millisecond, 0, 2},
		{1100 * time.Millisecond, 100 * time.Millisecond, 10, 40},
		{1 * time.Second, 0, 50, 0},
		{1 * time.Second, 0, 60, 0},
	} {
		if got := framesDue(c.elapsed, c.latency, c.emitted); got != c.want {
			t.Errorf("framesDue(%v, %v, %d) = %d, want %d", c.elapsed, c.latency, c.emitted, got, c.want)
		}
	}
}
