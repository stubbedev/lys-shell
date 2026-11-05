package regionoverlay

import (
	"context"
	"image"
	"log"
	"slices"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xkb"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/keys"
)

// Window is a window a click can pick, (ssiiii): its identifier, the
// output that shows it, and its global logical rectangle.
type Window struct {
	ID                  string
	Output              string
	X, Y, Width, Height int32
}

func (w Window) rect() image.Rectangle {
	return image.Rect(int(w.X), int(w.Y), int(w.X+w.Width), int(w.Y+w.Height))
}

// WindowAt is the frontmost of windows, listed front to back on each
// output, that output shows at the global point p.
func WindowAt(windows []Window, output string, p image.Point) (Window, bool) {
	i := slices.IndexFunc(windows, func(w Window) bool { return w.Output == output && p.In(w.rect()) })
	if i < 0 {
		return Window{}, false
	}
	return windows[i], true
}

// Picked is a window pick's answer: the window clicked, or the screen
// Enter shares.
type Picked struct {
	// Window is the clicked window's ID, "" when Enter chose the screen.
	Window string
	// Screen is the output Enter shared.
	Screen string
}

// windowOutline is the line around a window not under the pointer.
var windowOutline = render.RGBA(255, 255, 255, 96)

// WindowPick is the click-to-pick overlay: a layer surface on every
// monitor dims the desktop and outlines the windows a click can pick,
// the one under the pointer cleared and outlined in the accent. A click
// picks it, Enter shares the screen where the request offers one, and
// Escape gives up. Request is its only entry point; everything else
// runs on the lys-ui loop goroutine.
type WindowPick struct {
	app     *app.Application
	gate    *Gate
	outputs func() []*app.Output
	font    render.Font
	sheet   *widget.Stylesheet

	reply    chan windowResult
	surfaces []*app.LayerWindow
	areas    []*windowArea
	windows  []Window
	screen   string
	// pointer is the last global pointer position any surface saw;
	// onSurface is false once it left them all.
	pointer   image.Point
	onSurface bool
}

type windowResult struct {
	picked Picked
	ok     bool
}

// NewWindowPick builds the overlay on the application, on the shell's
// one capture gate; outputs lists the monitors to cover at each request.
func NewWindowPick(a *app.Application, gate *Gate, outputs func() []*app.Output, font render.Font, sheet *widget.Stylesheet) *WindowPick {
	return &WindowPick{app: a, gate: gate, outputs: outputs, font: font, sheet: sheet}
}

// Request opens the overlay over windows, listed front to back on each
// output, and blocks until a click picks one or Enter shares screen
// ("" offers no screen): ok. Escape, or ctx ending, answers not ok
// without an error; another capture session holding the gate answers
// ErrBusy. Call it from any goroutine but the loop's.
func (o *WindowPick) Request(ctx context.Context, windows []Window, screen string) (Picked, bool, error) {
	r, err := OpenSession(ctx, o.gate, o.app.Invoke, nil, func(_ map[string]render.StillImage, ch chan windowResult) {
		o.show(ch, windows, screen)
	}, o.cancel)
	return r.picked, r.ok, err
}

// Hovered is the window under the pointer, on the loop.
func (o *WindowPick) Hovered() (Window, bool) {
	if !o.onSurface {
		return Window{}, false
	}
	for _, a := range o.areas {
		if o.pointer.In(a.mon.rect()) {
			return WindowAt(o.windows, a.mon.Connector, o.pointer)
		}
	}
	return Window{}, false
}

func (o *WindowPick) cancel(ch chan windowResult) {
	if o.reply == ch {
		o.finish(Picked{}, false)
	}
}

// show maps one surface per monitor for the request.
func (o *WindowPick) show(ch chan windowResult, windows []Window, screen string) {
	o.reply, o.windows, o.screen, o.onSurface = ch, windows, screen, false
	for _, out := range o.outputs() {
		a := &windowArea{o: o, mon: MonitorOf(out)}
		a.AddClass("region-overlay-area")
		if o.font != nil {
			items := []legendItem{{[]keyCap{{icon: "ld-mouse-symbolic"}}, "window-pick-click"}}
			if screen != "" {
				items = append(items, legendItem{[]keyCap{{icon: "ld-corner-down-left-symbolic"}}, "window-pick-screen"})
			}
			a.legend = legend(o.font, append(items, legendItem{[]keyCap{{text: "Esc"}}, "window-pick-picker"})...)
		}
		layer, err := OpenSurface(o.app, out, "lys-shell-window-pick", overlayRoot(a, o.sheet),
			keys.OnKey(o.app.KeySym, func(_ *widget.Router, sym xkb.Keysym, _ app.Mods) { o.key(sym) }))
		if err != nil {
			log.Printf("window pick: surface on %s: %v", a.mon.Connector, err)
			continue
		}
		o.surfaces = append(o.surfaces, layer)
		o.areas = append(o.areas, a)
	}
	if len(o.surfaces) == 0 {
		log.Print("window pick: no surface could be mapped")
		o.finish(Picked{}, false)
	}
}

// finish tears the surfaces down and answers the open request, if any.
func (o *WindowPick) finish(p Picked, ok bool) {
	for _, s := range o.surfaces {
		s.Close()
	}
	o.surfaces, o.areas, o.windows = nil, nil, nil
	if o.reply != nil {
		o.reply <- windowResult{picked: p, ok: ok}
		o.reply = nil
	}
}

// key handles the overlay's keys by the symbol the seat's keymap gives
// them: Escape gives up, Enter shares the offered screen.
func (o *WindowPick) key(sym xkb.Keysym) {
	switch sym {
	case xkb.KeyEscape:
		o.finish(Picked{}, false)
	case xkb.KeyReturn, xkb.KeyKPEnter:
		if o.screen != "" {
			o.finish(Picked{Screen: o.screen}, true)
		}
	}
}

// click picks the window under the global point p; a click on none
// does nothing.
func (o *WindowPick) click(p image.Point) {
	o.pointer, o.onSurface = p, true
	if w, ok := o.Hovered(); ok {
		o.finish(Picked{Window: w.ID}, true)
	}
}

func (o *WindowPick) redraw() {
	for _, a := range o.areas {
		a.Invalidate()
	}
}

// windowArea is one monitor's drawing area: the wash, the windows'
// outlines, the hovered window's hole and the legend.
type windowArea struct {
	widget.Base
	o      *WindowPick
	mon    Monitor
	legend *widget.Box
}

// Children are the legend.
func (a *windowArea) Children() []widget.Widget {
	if a.legend == nil {
		return nil
	}
	return []widget.Widget{a.legend}
}

// Arrange lays the legend centred at the bottom of the surface.
func (a *windowArea) Arrange(r render.Rect) {
	a.ArrangeSelf(r)
	if a.legend == nil {
		return
	}
	widget.SetParents(a, a.legend)
	size := widget.SizeWithin(a.legend, widget.Size{W: r.W, H: r.H})
	a.legend.Arrange(render.Rect{X: r.X + (r.W-size.W)/2, Y: r.Y + r.H - legendMargin - size.H, W: size.W, H: size.H})
}

// Measure asks for nothing: the area takes the whole output.
func (a *windowArea) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

func (a *windowArea) HitTest(p widget.Point) widget.Widget { return a.HitLeaf(a, p) }

// global maps a surface-local point to the layout.
func (a *windowArea) global(p widget.Point) image.Point {
	b := a.Bounds()
	return image.Pt(a.mon.X+p.X-b.X, a.mon.Y+p.Y-b.Y)
}

// CursorName implements widget.CursorNamer: a hand over a window a
// click picks.
func (a *windowArea) CursorName() string {
	if _, ok := a.o.Hovered(); ok {
		return "pointer"
	}
	return "default"
}

// HoverMove implements widget.HoverMover: the window under the pointer
// lights up.
func (a *windowArea) HoverMove(p widget.Point) {
	a.o.pointer, a.o.onSurface = a.global(p), true
	a.o.redraw()
}

// SetHovered implements widget.HoverSetter: leaving every surface
// lights none.
func (a *windowArea) SetHovered(on bool) {
	if !on {
		a.o.onSurface = false
		a.o.redraw()
	}
}

// ClickAt implements widget.Clicker: a click picks the window under it.
func (a *windowArea) ClickAt(p widget.Point) { a.o.click(a.global(p)) }

// local maps a global rectangle into this surface's coordinates.
func (a *windowArea) local(r image.Rectangle) render.Rect {
	b := a.Bounds()
	return render.Rect{X: r.Min.X - a.mon.X + b.X, Y: r.Min.Y - a.mon.Y + b.Y, W: r.Dx(), H: r.Dy()}
}

// Paint washes the monitor and draws its windows back to front, each
// covering the outlines of those behind it: the hovered one as a hole
// in the wash outlined in the accent, the others outlined faintly.
func (a *windowArea) Paint(cv *render.Canvas) {
	dev := cv.MapRect(a.Bounds())
	wash := render.Color(dimAlpha << 24)
	cv.ClearDevice(dev, wash)
	hovered, lit := a.o.Hovered()
	for _, w := range slices.Backward(a.o.windows) {
		if w.Output != a.mon.Connector {
			continue
		}
		r := a.local(w.rect())
		if lit && w == hovered {
			cv.ClearDevice(cv.MapRect(r).Intersect(dev), 0)
			strokeAccent(cv, r, widget.CascadeColor(a))
			continue
		}
		cv.ClearDevice(cv.MapRect(r).Intersect(dev), wash)
		cv.BorderRect(r, 1, windowOutline)
	}
	if a.legend != nil {
		widget.PaintChild(cv, a.legend)
	}
}
