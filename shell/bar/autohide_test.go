package bar

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

type pendingTimer struct {
	d       time.Duration
	fn      func()
	stopped bool
}

type autoHideRig struct {
	a       *autoHide
	timers  []*pendingTimer
	applied []bool
	cover   bool
	full    bool
	known   bool
}

func newAutoHideRig(mode config.BarAutoHide) *autoHideRig {
	r := &autoHideRig{known: true}
	r.a = &autoHide{
		mode:  mode,
		delay: 300 * time.Millisecond,
		after: func(d time.Duration, fn func()) func() {
			t := &pendingTimer{d: d, fn: fn}
			r.timers = append(r.timers, t)
			return func() { t.stopped = true }
		},
		covered:    func() (bool, bool) { return r.cover, r.known },
		fullscreen: func() (bool, bool) { return r.full, r.known },
		apply:      func(hidden bool) { r.applied = append(r.applied, hidden) },
	}
	return r
}

// fire runs the timers due, the live ones only.
func (r *autoHideRig) fire() {
	timers := r.timers
	r.timers = nil
	for _, t := range timers {
		if !t.stopped {
			t.fn()
		}
	}
}

func (r *autoHideRig) live() int {
	n := 0
	for _, t := range r.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

func TestAutoHideAlwaysHidesAfterTheDelayAndRevealsUnderThePointer(t *testing.T) {
	r := newAutoHideRig(config.AutoHideAlways)
	r.a.sync()
	if r.a.hidden || len(r.timers) != 1 || r.timers[0].d != 300*time.Millisecond {
		t.Fatalf("hid %t with %d timers, want one delay pending first", r.a.hidden, len(r.timers))
	}
	r.fire()
	if !r.a.hidden || r.live() != 0 {
		t.Fatalf("after the delay: hidden %t, %d timers live", r.a.hidden, r.live())
	}
	r.a.setHovered(true)
	if r.a.hidden {
		t.Fatal("the pointer at the edge did not reveal it at once")
	}
	r.a.setHovered(false)
	r.a.setHovered(true)
	r.fire()
	if r.a.hidden {
		t.Fatal("a delay that ran out under the pointer hid the bar")
	}
	r.a.setHovered(false)
	r.fire()
	if !r.a.hidden || r.a.hides != 2 || r.a.reveals != 1 {
		t.Errorf("hidden %t, %d hides, %d reveals; want 2 and 1", r.a.hidden, r.a.hides, r.a.reveals)
	}
	if r.live() != 0 {
		t.Errorf("%d timers run while the bar rests hidden", r.live())
	}
}

func TestAutoHideStaysWhileHeldAndForAPeek(t *testing.T) {
	r := newAutoHideRig(config.AutoHideAlways)
	r.a.sync()
	r.fire()
	release := r.a.hold()
	if r.a.hidden {
		t.Fatal("a dropdown opening did not reveal the bar")
	}
	r.fire()
	if r.a.hidden {
		t.Fatal("the bar hid under an open dropdown")
	}
	release()
	release()
	r.fire()
	if !r.a.hidden || r.a.holds != 0 {
		t.Fatalf("after the dropdown closed: hidden %t, %d holds", r.a.hidden, r.a.holds)
	}
	r.a.peek(peekDuration)
	if r.a.hidden {
		t.Fatal("panel reveal did not reveal the bar")
	}
	var peek *pendingTimer
	for _, tm := range r.timers {
		if tm.d == peekDuration {
			peek = tm
		}
	}
	if peek == nil {
		t.Fatal("the peek has no end")
	}
	r.fire()
	r.fire()
	if !r.a.hidden {
		t.Error("the bar did not hide again after the peek")
	}
	r.a.close()
	if r.live() != 0 {
		t.Errorf("%d timers live after close", r.live())
	}
}

func TestAutoHideFollowsTheWindows(t *testing.T) {
	r := newAutoHideRig(config.AutoHideWindowsOverlap)
	r.a.sync()
	if r.a.hidden || len(r.timers) != 0 {
		t.Fatalf("nothing over the bar: hidden %t, %d timers", r.a.hidden, len(r.timers))
	}
	r.cover = true
	r.a.sync()
	r.fire()
	if !r.a.hidden {
		t.Fatal("a window over the bar did not hide it")
	}
	r.cover = false
	r.a.sync()
	if r.a.hidden {
		t.Fatal("the window leaving did not reveal the bar")
	}

	full := newAutoHideRig(config.AutoHideFullscreen)
	full.cover = true
	full.a.sync()
	if full.a.hidden || len(full.timers) != 0 {
		t.Fatal("fullscreen mode hid for a window that only overlaps")
	}
	full.full = true
	full.a.sync()
	full.fire()
	if !full.a.hidden {
		t.Fatal("a fullscreen window did not hide the bar")
	}

	blind := newAutoHideRig(config.AutoHideWindowsOverlap)
	blind.known = false
	blind.a.sync()
	blind.fire()
	if !blind.a.hidden {
		t.Error("without window geometry the mode does not hide as always does")
	}

	never := newAutoHideRig(config.AutoHideNever)
	never.cover, never.full = true, true
	never.a.sync()
	if never.a.hidden || len(never.timers) != 0 || len(never.applied) != 0 {
		t.Error("never hid the bar")
	}
}

func TestAnAutoHidingBarNeverReservesItsEdge(t *testing.T) {
	bar := config.Defaults().Bar
	if !barExclusive(bar, clockLayout) {
		t.Fatal("the default exclusive bar does not reserve its edge")
	}
	for _, mode := range []config.BarAutoHide{config.AutoHideAlways, config.AutoHideWindowsOverlap, config.AutoHideFullscreen} {
		b := bar
		b.AutoHide = mode
		if barExclusive(b, clockLayout) {
			t.Errorf("auto-hide %s reserves the edge", mode)
		}
	}
	always := config.AutoHideAlways
	layout := clockLayout
	layout.AutoHide = &always
	if barExclusive(bar, layout) || bar.AutoHideOn(layout) != config.AutoHideAlways {
		t.Error("a layout's auto-hide does not override the bar's")
	}
}

func TestAHiddenBarOwesNoFrameForItsModules(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	b := buildRoot(ctx, config.BarLayout{Monitor: "*"}, "DP-1")
	clock := widget.NewLabel(ctx.Font, 14, "12:00", 0)
	b.Start().Append(clock, false)
	b.Arrange(render.Rect{W: 800, H: 30})
	widget.CollectDamage(b)
	w := &barWindow{bar: b, hide: &autoHide{hidden: true}, thickness: 30}
	w.applyHidden(true)
	if thick := barThickness(b, widget.Size{W: 800, H: 400}); thick != revealPx {
		t.Errorf("the hidden bar is %d thick, want the %dpx strip", thick, revealPx)
	}
	b.Arrange(render.Rect{W: 800, H: revealPx})
	widget.CollectDamage(b)
	for _, text := range []string{"12:01", "12:02", "12:03"} {
		clock.SetText(text)
		if rects, owed := widget.CollectDamage(b); owed {
			t.Fatalf("a clock tick on the hidden bar owes a frame: %v", rects)
		}
	}
}
