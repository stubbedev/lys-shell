package bar

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/vinput"
)

// On a real compositor an always-hiding bar collapses to its strip after
// the delay, reveals when the pointer reaches the edge, stays while a
// dropdown opened from it is open, hides once it closes, and peeks for
// `panel reveal`; through all of it the workspace never loses the bar's
// edge, so no window is configured for it.
func TestHeadlessAutoHidingBarRevealsAtTheEdge(t *testing.T) {
	swaytest.Start(t)
	cfg := config.Defaults()
	cfg.Bar.AutoHide = config.AutoHideAlways
	cfg.Bar.AutoHideDelayMs = 50
	h := openHeadlessBar(t, cfg, clockLayout)
	thickness := func() int {
		var thick int
		h.onLoop(func() { _, thick = h.bar.layer.Size() })
		return thick
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (thickness %d, reserved %d)", what, thickness(), reservedTop(t))
			}
		}
		if top := reservedTop(t); top != 0 {
			t.Fatalf("%s: the workspace starts %dpx down; the bar must reserve nothing", what, top)
		}
	}
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pointer.Close()
	cursor := func(x, y int) {
		t.Helper()
		ow, oh := pointer.OutputSize()
		if err := pointer.MotionAbsolute(intconv.Clamp[uint32](x), intconv.Clamp[uint32](y), intconv.Clamp[uint32](ow), intconv.Clamp[uint32](oh)); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	cursor(400, 400)
	wait("the bar to hide", func() bool { return thickness() == revealPx })
	cursor(50, 40)
	cursor(50, 1)
	wait("the pointer at the edge to reveal it", func() bool { return thickness() > revealPx })
	shown := thickness()

	h.onLoop(func() {
		anchor := h.bar.bar.Start().Children()[0]
		if err := h.rt.ctx.Dropdowns.open(h.output.Name, "calendar", anchor); err != nil {
			t.Errorf("open the calendar: %v", err)
		}
	})
	cursor(300, 500)
	time.Sleep(300 * time.Millisecond)
	if got := thickness(); got != shown {
		t.Fatalf("the bar is %d thick under an open dropdown, want %d", got, shown)
	}
	h.onLoop(h.rt.ctx.Dropdowns.closeAll)
	wait("the bar to hide once the dropdown closed", func() bool { return thickness() == revealPx })

	h.onLoop(func() { h.set.peek("", 200*time.Millisecond) })
	wait("panel reveal to show it", func() bool { return thickness() > revealPx })
	wait("the peek to end", func() bool { return thickness() == revealPx })
}
