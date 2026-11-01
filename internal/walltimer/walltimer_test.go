package walltimer

import (
	"context"
	"testing"
	"time"
)

func TestNextIsTheFollowingMultiple(t *testing.T) {
	at := time.Date(2026, 11, 1, 20, 33, 42, 500, time.UTC)
	if got, want := next(at, time.Minute), time.Date(2026, 11, 1, 20, 34, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("next minute = %v, want %v", got, want)
	}
	if got, want := next(at, time.Second), time.Date(2026, 11, 1, 20, 33, 43, 0, time.UTC); !got.Equal(want) {
		t.Errorf("next second = %v, want %v", got, want)
	}
	onTheDot := time.Date(2026, 11, 1, 20, 34, 0, 0, time.UTC)
	if got := next(onTheDot, time.Minute); !got.After(onTheDot) {
		t.Errorf("next from a boundary = %v, must move on", got)
	}
}

func TestFiresOnTheBoundaryAndNowhereElse(t *testing.T) {
	fired := make(chan time.Time, 8)
	if err := Start(t.Context(), time.Second, func() { fired <- time.Now() }); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case at := <-fired:
			if off := at.Sub(at.Truncate(time.Second)); off > 200*time.Millisecond {
				t.Errorf("fired %v after the boundary", off)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("never fired")
		}
	}
}

func TestStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fired := make(chan struct{}, 8)
	if err := Start(ctx, time.Second, func() { fired <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-fired:
		t.Fatal("fired after its context ended")
	case <-time.After(1200 * time.Millisecond):
	}
}
