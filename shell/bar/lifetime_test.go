package bar

import (
	"testing"
	"time"
)

// A module's followed subscription delivers until its generation
// retires, then unsubscribes; its Life ends with it.
func TestRetiringAGenerationEndsItsModules(t *testing.T) {
	gen := newMountGen()
	ctx := ModuleContext{gen: gen}
	ticks := make(chan struct{}, 1)
	got := make(chan struct{}, 4)
	stopped := make(chan struct{})
	follow(ctx, ticks, func() { close(stopped) }, func(struct{}) { got <- struct{}{} })

	ticks <- struct{}{}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("a live generation did not deliver")
	}
	if err := ctx.Life().Err(); err != nil {
		t.Fatalf("Life ended before retire: %v", err)
	}

	gen.retire()
	gen.retire() // idempotent
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("retire did not unsubscribe the followed feed")
	}
	if ctx.Life().Err() == nil {
		t.Error("Life outlived its generation")
	}
	ticks <- struct{}{}
	select {
	case <-got:
		t.Error("a retired generation still delivered")
	case <-time.After(50 * time.Millisecond):
	}
}

// A closed feed ends the follow while its generation lives on.
func TestFollowEndsWithItsFeed(t *testing.T) {
	ctx := ModuleContext{gen: testGen(t)}
	ticks := make(chan struct{})
	stopped := make(chan struct{})
	follow(ctx, ticks, func() { close(stopped) }, func(struct{}) {})
	close(ticks)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("a closed feed did not end the follow")
	}
	ctx.gen.waitWork()
	if ctx.Life().Err() != nil {
		t.Error("the follow's end ended its generation")
	}
}
