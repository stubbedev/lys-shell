package nightschedule

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNewLocationRefusesWhatIsNotAPosition(t *testing.T) {
	for _, tc := range []struct {
		lat, lng float64
		want     string
	}{
		{90.5, 10, "latitude 90.5"},
		{-90.5, 10, "latitude -90.5"},
		{math.NaN(), 10, "latitude NaN"},
		{10, 180.5, "longitude 180.5"},
		{10, math.NaN(), "longitude NaN"},
		{0, 0, "0, 0"},
	} {
		if _, err := NewLocation(tc.lat, tc.lng); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("NewLocation(%v, %v) = %v, want an error naming %q", tc.lat, tc.lng, err, tc.want)
		}
	}
	if _, err := NewLocation(0, 0); !errors.Is(err, ErrNullIsland) {
		t.Errorf("0, 0 = %v, want ErrNullIsland", err)
	}
	for _, ok := range [][2]float64{{90, 180}, {-90, -180}, {0, 12.5}, {55.6, 0}} {
		loc, err := NewLocation(ok[0], ok[1])
		if err != nil || loc.Latitude() != ok[0] || loc.Longitude() != ok[1] {
			t.Errorf("NewLocation(%v, %v) = %+v, %v", ok[0], ok[1], loc, err)
		}
	}
}

func TestNewTransitionHoldsItsRange(t *testing.T) {
	for _, minutes := range []int64{0, 15, MaxTransitionMinutes} {
		tr, err := NewTransition(minutes)
		if err != nil || int64(tr.Minutes()) != minutes || tr.Duration() != time.Duration(minutes)*time.Minute {
			t.Errorf("NewTransition(%d) = %+v, %v", minutes, tr, err)
		}
	}
	for _, minutes := range []int64{-1, MaxTransitionMinutes + 1} {
		if _, err := NewTransition(minutes); err == nil {
			t.Errorf("NewTransition(%d) was accepted", minutes)
		}
	}
}
