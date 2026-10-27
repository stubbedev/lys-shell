package handoff

import (
	"slices"
	"testing"
)

func run(fn func()) { fn() }

func TestDeliverLandsOnALiveLoop(t *testing.T) {
	var h Handoffs
	var landed, freed int
	if !h.Go(run, func() { h.Deliver(run, func() { landed++ }, func() { freed++ }) }) {
		t.Fatal("work refused before Close")
	}
	h.Close()
	if landed != 1 || freed != 0 {
		t.Errorf("landed %d, freed %d; want landed once", landed, freed)
	}
}

func TestCloseFreesWhatTheLoopNeverTook(t *testing.T) {
	var h Handoffs
	var dropped []func()
	drop := func(fn func()) { dropped = append(dropped, fn) }
	var freed []int
	for i := range 3 {
		h.Go(run, func() {
			h.Deliver(drop, func() { t.Errorf("result %d landed", i) }, func() { freed = append(freed, i) })
		})
	}
	h.Close()
	if !slices.Equal(freed, []int{0, 1, 2}) {
		t.Errorf("freed %v, want every result in delivery order", freed)
	}
	for _, fn := range dropped {
		fn()
	}
	h.Close()
	if len(freed) != 3 {
		t.Errorf("freed %v after a late land and a second Close", freed)
	}
}

func TestDeliverAfterCloseFreesAtOnce(t *testing.T) {
	var h Handoffs
	h.Close()
	freed := 0
	h.Deliver(func(func()) { t.Error("invoked after Close") }, func() { t.Error("landed after Close") }, func() { freed++ })
	if freed != 1 {
		t.Errorf("freed %d times, want once", freed)
	}
}

func TestGoAfterCloseRunsNothing(t *testing.T) {
	var h Handoffs
	h.Close()
	if h.Go(func(func()) { t.Error("spawned after Close") }, func() {}) {
		t.Error("work accepted after Close")
	}
	if !h.Closed() {
		t.Error("not closed after Close")
	}
}

func TestCloseWaitsForRunningWork(t *testing.T) {
	var h Handoffs
	started, release := make(chan struct{}), make(chan struct{})
	freed := make(chan struct{}, 1)
	h.Go(func(fn func()) { go fn() }, func() {
		close(started)
		<-release
		h.Deliver(func(func()) {}, func() { t.Error("landed on an ended loop") }, func() { freed <- struct{}{} })
	})
	<-started
	if h.Closed() {
		t.Fatal("closed before Close")
	}
	closed := make(chan struct{})
	go func() {
		h.Close()
		close(closed)
	}()
	close(release)
	<-closed
	select {
	case <-freed:
	default:
		t.Error("Close returned before the running work's result was freed")
	}
}
