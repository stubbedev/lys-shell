package bar

import (
	"fmt"
	"slices"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/wmipc"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// revealPx is the strip a hidden bar or dock keeps at its edge, which
// the pointer reaches to reveal it.
const revealPx = 2

// peekDuration is how long `lys-shell panel reveal` shows a hidden bar.
const peekDuration = 3 * time.Second

// autoHide is the reveal controller the bar and the dock share: it
// decides when an edge surface hides, from its mode, the pointer on it,
// what holds it open (a dropdown, a peek) and, for the window-dependent
// modes, the windows on its output; and it hands each change to apply.
// It runs on the loop and does nothing between changes: no timer runs
// while the surface rests hidden or shown.
type autoHide struct {
	mode  config.BarAutoHide
	delay time.Duration
	// after runs fn on the loop after d; the returned stop cancels. A
	// controller without a delay (the dock) needs none.
	after func(d time.Duration, fn func()) (stop func())
	// covered reports a window overlapping the surface, fullscreen a
	// window filling its output; false, false without window geometry,
	// which makes both modes hide like always.
	covered, fullscreen func() (bool, bool)
	apply               func(hidden bool)

	hovered bool
	holds   int
	peeking func()
	hiding  func()
	hidden  bool
	// hides and reveals count the transitions.
	hides, reveals int
}

// wantsHidden is whether the surface should hide now.
func (a *autoHide) wantsHidden() bool {
	if a.hovered || a.holds > 0 || a.peeking != nil {
		return false
	}
	switch a.mode {
	case config.AutoHideAlways:
		return true
	case config.AutoHideWindowsOverlap:
		on, known := a.covered()
		return on || !known
	case config.AutoHideFullscreen:
		on, known := a.fullscreen()
		return on || !known
	}
	return false
}

// sync hides or reveals for what holds now: a reveal at once, a hide
// once delay has passed with nothing revealing it again.
func (a *autoHide) sync() {
	if !a.wantsHidden() {
		a.cancelHide()
		a.set(false)
		return
	}
	if a.hidden || a.hiding != nil {
		return
	}
	if a.delay <= 0 {
		a.set(true)
		return
	}
	a.hiding = a.after(a.delay, func() {
		a.hiding = nil
		if a.wantsHidden() {
			a.set(true)
		}
	})
}

func (a *autoHide) cancelHide() {
	if a.hiding != nil {
		a.hiding()
		a.hiding = nil
	}
}

func (a *autoHide) set(hidden bool) {
	if hidden == a.hidden {
		return
	}
	a.hidden = hidden
	if hidden {
		a.hides++
	} else {
		a.reveals++
	}
	a.apply(hidden)
}

// setMode changes the mode and the delay, hiding or revealing for it.
func (a *autoHide) setMode(mode config.BarAutoHide, delay time.Duration) {
	a.mode, a.delay = mode, delay
	a.cancelHide()
	a.sync()
}

// setHovered is the pointer entering or leaving the surface.
func (a *autoHide) setHovered(on bool) {
	a.hovered = on
	a.sync()
}

// hold keeps the surface revealed until the returned release.
func (a *autoHide) hold() (release func()) {
	a.holds++
	a.sync()
	released := false
	return func() {
		if released {
			return
		}
		released = true
		a.holds--
		a.sync()
	}
}

// peek reveals the surface for d, then lets it hide again.
func (a *autoHide) peek(d time.Duration) {
	if a.peeking != nil {
		a.peeking()
	}
	a.peeking = a.after(d, func() {
		a.peeking = nil
		a.sync()
	})
	a.sync()
}

// close stops every pending timer.
func (a *autoHide) close() {
	a.cancelHide()
	if a.peeking != nil {
		a.peeking()
		a.peeking = nil
	}
}

// loopAfter runs fn on application's loop after d.
func loopAfter(application *app.Application) func(time.Duration, func()) func() {
	return func(d time.Duration, fn func()) func() {
		t := time.AfterFunc(d, func() { application.Invoke(fn) })
		return func() { t.Stop() }
	}
}

// windowsOn reports, from lys-wm's geometry, a window overlapping area
// on output and a window filling output; known is false without the
// geometry.
func windowsOn(hub *taskHub, output string, area wmipc.Rect) (overlapping, fullscreen, known bool) {
	if hub == nil {
		return false, false, false
	}
	windows, ok := hub.geometry()
	if !ok {
		return false, false, false
	}
	full := slices.ContainsFunc(windows, func(w wmipc.Window) bool {
		return w.Output == output && w.Stack != nil && !w.Minimized && w.Fullscreen
	})
	return covered(windows, output, area), full, true
}

// barExclusive reports whether a bar reserves its edge: only one that
// never hides, so revealing and hiding never move the windows.
func barExclusive(bar config.BarConfig, layout config.BarLayout) bool {
	return bar.Exclusive && bar.AutoHideOn(layout) == config.AutoHideNever
}

// autoHideDelay is an auto-hide-delay-ms setting.
func autoHideDelay(ms uint32) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// startAutoHide gives the bar its reveal controller: hovering the bar
// reveals it, and with lys-wm the window-dependent modes follow the
// windows on its output.
func (w *barWindow) startAutoHide(ctx ModuleContext, layout config.BarLayout, after func(time.Duration, func()) func()) {
	w.hide = &autoHide{
		mode:       ctx.Config.Bar.AutoHideOn(layout),
		delay:      autoHideDelay(ctx.Config.Bar.AutoHideDelayMs),
		after:      after,
		covered:    func() (bool, bool) { on, _, known := w.windows(); return on, known },
		fullscreen: func() (bool, bool) { _, on, known := w.windows(); return on, known },
		apply:      w.applyHidden,
	}
	w.bar.SetOnHoverWithin(w.hide.setHovered)
	if ctx.Tasks != nil {
		w.stopGeom = ctx.Tasks.subscribeGeometry(w.hide.sync)
	}
	w.hide.sync()
}

// windows reports a window over the bar's strip of its output and one
// filling the output; known is false without lys-wm's geometry.
func (w *barWindow) windows() (covering, fullscreen, known bool) {
	return windowsOn(w.host.rt.ctx.Tasks, w.output.Name, barArea(w.bar.Edge(), w.output, w.shownThickness()))
}

// shownThickness is the bar's size across its edge as it shows: now
// when it shows, as it last showed while hidden.
func (w *barWindow) shownThickness() int {
	if w.hide == nil || !w.hide.hidden {
		width, height := w.layer.Size()
		w.thickness = height
		if w.bar.Edge().Vertical() {
			w.thickness = width
		}
	}
	return w.thickness
}

// barArea is the strip of out a bar along edge covers.
func barArea(edge widget.BarEdge, out *app.Output, thickness int) wmipc.Rect {
	r := wmipc.Rect{X: int(out.LogicalX), Y: int(out.LogicalY), Width: int(out.LogicalW), Height: int(out.LogicalH)}
	switch edge {
	case widget.BarTop:
		r.Height = thickness
	case widget.BarBottom:
		r.Y, r.Height = r.Y+r.Height-thickness, thickness
	case widget.BarLeft:
		r.Width = thickness
	case widget.BarRight:
		r.X, r.Width = r.X+r.Width-thickness, thickness
	}
	return r
}

// applyHidden collapses the bar to its reveal strip, its zones out of
// the frame so their updates owe no repaint, or restores it.
func (w *barWindow) applyHidden(hidden bool) {
	w.shownThickness()
	for _, zone := range []*widget.Box{w.bar.Start(), w.bar.Center(), w.bar.End()} {
		zone.SetVisible(!hidden)
	}
	w.bar.SetClass("auto-hidden", hidden)
	thickness := 0
	if hidden {
		thickness = revealPx
	}
	w.bar.SetMinThickness(thickness)
}

// peek reveals the bar for d.
func (w *barWindow) peek(d time.Duration) { w.hide.peek(d) }

// walkKeyboard hands the bar the keyboard (`lys-shell panel focus`),
// revealed until the walk ends.
func (w *barWindow) walkKeyboard() error {
	release := w.hide.hold()
	if err := w.layer.WalkKeyboard(release); err != nil {
		release()
		return fmt.Errorf("walk the bar on %s: %w", w.output.Name, err)
	}
	return nil
}
