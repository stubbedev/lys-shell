package intconv

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestExactKeepsValuesTheTargetHolds(t *testing.T) {
	if got, err := Exact[int32](int64(math.MinInt32)); err != nil || got != math.MinInt32 {
		t.Errorf("MinInt32 -> %d %v", got, err)
	}
	if got, err := Exact[uint8](255); err != nil || got != 255 {
		t.Errorf("255 -> %d %v", got, err)
	}
	if got, err := Exact[uint64](int64(math.MaxInt64)); err != nil || got != math.MaxInt64 {
		t.Errorf("MaxInt64 -> %d %v", got, err)
	}
	if got, err := Exact[int64](uint32(math.MaxUint32)); err != nil || got != math.MaxUint32 {
		t.Errorf("MaxUint32 -> %d %v", got, err)
	}
}

func TestExactRefusesValuesTheTargetCannotHold(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"int64 past int32", func() error { _, err := Exact[int32](int64(math.MaxInt32) + 1); return err }()},
		{"negative to uint32", func() error { _, err := Exact[uint32](-1); return err }()},
		{"uint64 past int64", func() error { _, err := Exact[int64](uint64(math.MaxInt64) + 1); return err }()},
		{"256 to uint8", func() error { _, err := Exact[uint8](256); return err }()},
		{"uint32 past int32", func() error { _, err := Exact[int32](uint32(math.MaxInt32) + 1); return err }()},
	}
	for _, c := range cases {
		if _, ok := errors.AsType[*RangeError](c.err); !ok {
			t.Errorf("%s: got %v, want a *RangeError", c.name, c.err)
		}
	}
	_, err := Exact[uint8](-3)
	if err == nil || err.Error() != "-3 does not fit in uint8" {
		t.Errorf("message: %v", err)
	}
}

func TestClampSaturates(t *testing.T) {
	if got := Clamp[int32](int64(math.MaxInt64)); got != math.MaxInt32 {
		t.Errorf("high int32: %d", got)
	}
	if got := Clamp[int32](int64(math.MinInt64)); got != math.MinInt32 {
		t.Errorf("low int32: %d", got)
	}
	if got := Clamp[uint32](-5); got != 0 {
		t.Errorf("negative uint32: %d", got)
	}
	if got := Clamp[uint8](uint64(1) << 40); got != math.MaxUint8 {
		t.Errorf("high uint8: %d", got)
	}
	if got := Clamp[int64](uint64(math.MaxUint64)); got != math.MaxInt64 {
		t.Errorf("high int64: %d", got)
	}
	if got := Clamp[uint64](int64(math.MinInt64)); got != 0 {
		t.Errorf("low uint64: %d", got)
	}
}

func TestClampKeepsValuesInRange(t *testing.T) {
	if got := Clamp[int32](-7); got != -7 {
		t.Errorf("-7: %d", got)
	}
	if got := Clamp[uint16](uint64(65535)); got != 65535 {
		t.Errorf("65535: %d", got)
	}
}

func TestDurationScales(t *testing.T) {
	if got := Duration(uint64(1500), time.Millisecond); got != 1500*time.Millisecond {
		t.Errorf("1500ms: %v", got)
	}
	if got := Duration(int64(-2), time.Second); got != -2*time.Second {
		t.Errorf("-2s: %v", got)
	}
}

func TestDurationSaturatesInsteadOfWrapping(t *testing.T) {
	if got := Duration(uint64(math.MaxUint64), time.Millisecond); got != math.MaxInt64 {
		t.Errorf("huge ms: %v", got)
	}
	if got := Duration(uint64(math.MaxInt64/int64(time.Minute))+1, time.Minute); got != math.MaxInt64 {
		t.Errorf("just past the minute limit: %v", got)
	}
	if got := Duration(int64(math.MinInt64), time.Microsecond); got != math.MinInt64 {
		t.Errorf("huge negative: %v", got)
	}
}

func TestDurationRefusesANonPositiveUnit(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a zero unit did not panic")
		}
	}()
	Duration(1, 0)
}
