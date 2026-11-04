package bar

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/apptest"
)

type firedByHand struct {
	mu   sync.Mutex
	fire func()
}

func (f *firedByHand) source(_ context.Context, fire func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fire = fire
	return nil
}

func (f *firedByHand) tick(n int) {
	f.mu.Lock()
	fire := f.fire
	f.mu.Unlock()
	for range n {
		fire()
	}
}

func countingSpec(key string, pause pollPause, source pollSource, reads *atomic.Int32, closes *atomic.Int32) samplerSpec[int32] {
	return samplerSpec[int32]{
		key: key, pause: pause, source: source,
		sample:    func() (int32, bool) { return reads.Add(1), true },
		same:      func(a, b int32) bool { return a == b },
		closeRead: func() { closes.Add(1) },
	}
}

func TestSamplerReadsOncePerTickForEveryBar(t *testing.T) {
	h := newPollHub()
	src := &firedByHand{}
	var reads, closes atomic.Int32
	var first, second atomic.Int32
	spec := countingSpec("cpu", pauseIdleOrLocked, src.source, &reads, &closes)
	bar1, leave1 := context.WithCancel(t.Context())
	bar2, leave2 := context.WithCancel(t.Context())
	defer leave1()
	defer leave2()
	if err := joinSampler(bar1, h, spec, func(int32) { first.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if err := joinSampler(bar2, h, spec, func(int32) { second.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if got := closes.Load(); got != 1 {
		t.Errorf("the second bar's own reader closed %d times, want once at its join", got)
	}
	const ticks = 5
	src.tick(ticks)
	if got := reads.Load(); got != ticks {
		t.Errorf("%d reads over %d ticks with two bars, want one per tick", got, ticks)
	}
	if first.Load() != ticks || second.Load() != ticks {
		t.Errorf("bars got %d and %d readings, want %d each", first.Load(), second.Load(), ticks)
	}
	leave1()
	waitFor(t, "the first bar left", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		s := h.samplers["cpu"].(*sampler[int32])
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.subs) == 1
	})
	src.tick(1)
	if got := reads.Load(); got != ticks+1 {
		t.Error("one bar leaving ended the sampling for the other")
	}
	leave2()
	waitFor(t, "the last bar's leaving ends the sampler", func() bool { return closes.Load() == 2 })
}

func TestSamplerReadsNothingWhilePausedAndCatchesUpOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pause  pollPause
		set    func(*pollHub, bool)
		paused bool
	}{
		{"idle holds a poll", pauseIdleOrLocked, (*pollHub).setIdle, true},
		{"a lock holds a poll", pauseIdleOrLocked, (*pollHub).setLocked, true},
		{"a lock holds the time", pauseLocked, (*pollHub).setLocked, true},
		{"idle leaves the time running", pauseLocked, (*pollHub).setIdle, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newPollHub()
			src := &firedByHand{}
			var reads, closes atomic.Int32
			delivered := make(chan int32, 16)
			spec := countingSpec("key", tc.pause, src.source, &reads, &closes)
			if err := joinSampler(t.Context(), h, spec, func(v int32) { delivered <- v }); err != nil {
				t.Fatal(err)
			}
			src.tick(1)
			apptest.Answer(t, "the reading before the pause", delivered)
			tc.set(h, true)
			src.tick(10)
			if !tc.paused {
				if got := reads.Load(); got != 11 {
					t.Errorf("%d reads, want the ticks all read", got)
				}
				return
			}
			if got := reads.Load(); got != 1 {
				t.Errorf("%d reads with 10 ticks while paused, want none past the first", got-1)
			}
			tc.set(h, false)
			apptest.Answer(t, "one catching-up read as the pause ends", delivered)
			if got := reads.Load(); got != 2 {
				t.Errorf("%d reads after the pause, want one catching up", got)
			}
		})
	}
}

func TestSamplerSameReadingPostsNothing(t *testing.T) {
	h := newPollHub()
	src := &firedByHand{}
	spec := samplerSpec[string]{
		key: "same", source: src.source,
		sample: func() (string, bool) { return "x", true },
		same:   func(a, b string) bool { return a == b },
	}
	posted := 0
	if err := joinSampler(t.Context(), h, spec, func(string) { posted++ }); err != nil {
		t.Fatal(err)
	}
	src.tick(4)
	if posted != 1 {
		t.Errorf("%d posts for one unchanging reading, want 1", posted)
	}
}
