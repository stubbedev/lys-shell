package notifypolicy

import (
	"errors"
	"testing"
	"time"
)

func everyDay() DaySet {
	var d DaySet
	for day := time.Sunday; day <= time.Saturday; day++ {
		d = d.With(day)
	}
	return d
}

func TestScheduleWindowsAndTimesAreValidated(t *testing.T) {
	for _, raw := range []string{"24:00", "7:00", "07:60", "0700", "ab:cd"} {
		if _, err := ParseClockTime(raw); !errors.Is(err, ErrClockTime) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	at, err := ParseClockTime("07:00")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWindow(everyDay(), at, at); !errors.Is(err, ErrEmptyWindow) {
		t.Errorf("empty window: %v", err)
	}
}

func TestParseUntilTakesATimeOfDayOrADuration(t *testing.T) {
	now := time.Date(2026, 3, 2, 10, 30, 0, 0, time.UTC)
	for raw, want := range map[string]time.Time{
		"11:00": time.Date(2026, 3, 2, 11, 0, 0, 0, time.UTC),
		"09:00": time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		"10:30": time.Date(2026, 3, 3, 10, 30, 0, 0, time.UTC),
		"1h":    now.Add(time.Hour),
		"45m":   now.Add(45 * time.Minute),
	} {
		got, err := ParseUntil(raw, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q = %s, %v; want %s", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "soon", "-1h", "0s", "25:00"} {
		if _, err := ParseUntil(raw, now); !errors.Is(err, ErrUntil) {
			t.Errorf("%q: %v, want ErrUntil", raw, err)
		}
	}
}

// TestHistoryLimitsAreValidated pins the ranges the config refuses
// outside of.
func TestHistoryLimitsAreValidated(t *testing.T) {
	for _, n := range []int64{MinHistoryMemory, DefaultHistoryMemory, MaxHistoryMemory} {
		if l, err := NewMemoryLimit(n); err != nil || int64(l.Count()) != n {
			t.Errorf("memory %d: %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryMemory - 1, 0, MaxHistoryMemory + 1} {
		if _, err := NewMemoryLimit(n); !errors.Is(err, ErrHistoryLimit) {
			t.Errorf("memory %d: err = %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryStored, MaxHistoryStored} {
		if l, err := NewStoredLimit(n); err != nil || int64(l.Count()) != n {
			t.Errorf("stored %d: %v", n, err)
		}
	}
	for _, n := range []int64{MinHistoryStored - 1, MaxHistoryStored + 1} {
		if _, err := NewStoredLimit(n); !errors.Is(err, ErrHistoryLimit) {
			t.Errorf("stored %d: err = %v", n, err)
		}
	}
}
