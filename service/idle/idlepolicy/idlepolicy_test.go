package idlepolicy

import (
	"errors"
	"testing"
	"time"
)

func TestTimeoutAndDimPercentBounds(t *testing.T) {
	for _, d := range []time.Duration{0, time.Millisecond * 999, MaxTimeout + time.Millisecond} {
		if _, err := NewTimeout(d); !errors.Is(err, ErrTimeoutRange) {
			t.Errorf("NewTimeout(%s) = %v, want ErrTimeoutRange", d, err)
		}
	}
	if _, err := NewTimeout(MinTimeout); err != nil {
		t.Errorf("NewTimeout(MinTimeout) = %v", err)
	}
	for _, p := range []int64{0, 101, -1} {
		if _, err := NewDimPercent(p); !errors.Is(err, ErrDimPercentRange) {
			t.Errorf("NewDimPercent(%d) = %v, want ErrDimPercentRange", p, err)
		}
	}
	if _, ok := Never.After(); ok {
		t.Error("Never has an idle time")
	}
}
