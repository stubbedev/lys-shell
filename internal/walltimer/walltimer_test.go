package walltimer

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
			t.Logf("fired %v after the boundary", at.Sub(at.Truncate(time.Second)))
		case <-time.After(3 * time.Second):
			t.Fatal("never fired")
		}
	}
}

func TestArmsTheNextBoundaryAndRepeatsOnTheUnit(t *testing.T) {
	fd, err := unix.TimerfdCreate(unix.CLOCK_REALTIME, unix.TFD_NONBLOCK|unix.TFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := arm(fd, time.Minute); err != nil {
		t.Fatal(err)
	}
	var armed unix.ItimerSpec
	if err := unix.TimerfdGettime(fd, &armed); err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(armed.Interval.Nano()); got != time.Minute {
		t.Errorf("repeats every %v, want a minute", got)
	}
	if left := time.Duration(armed.Value.Nano()); left <= 0 || left > time.Minute {
		t.Errorf("first fire in %v, want within the minute", left)
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
