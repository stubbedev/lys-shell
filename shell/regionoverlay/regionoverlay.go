// Package regionoverlay is the region selection overlay: a layer
// surface on every monitor, a dim wash with a transparent hole where
// the selection overlaps that monitor, an accent border with resize
// handles, and a "W × H" size label. A drag draws the selection and a
// plain click picks the window (else the output) under the pointer;
// the selection is then moved, resized from its edges and corners or
// nudged with the arrow keys until Enter or a double-click confirms
// it. Everything is tracked in global (compositor-layout) logical
// coordinates, so a selection can span monitors. The screenshot host
// passes frozen per-output frames, which the surfaces paint behind the
// wash (freeze-frame) with a magnifier at the pointer; the share
// picker passes none, so the live screen shows through.
package regionoverlay

import (
	"fmt"
	"image"
	"log"
	"math"
	"slices"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/loupe"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// Selection is a region the user dragged, in logical pixels relative
// to Output: slurp's `<output>@x,y,w,h` and the screencopy region
// semantics.
type Selection struct {
	// Output is the connector name of the output the selection is
	// relative to.
	Output              string
	X, Y, Width, Height int
}

// Monitor is one output's connector name and global logical geometry.
type Monitor struct {
	Connector           string
	X, Y, Width, Height int
}

// MonitorOf reads an output's logical geometry: the xdg-output
// position and size, falling back to the mode divided by the scale
// when the compositor reported no logical size.
func MonitorOf(o *app.Output) Monitor {
	m := Monitor{Connector: o.Name, X: int(o.LogicalX), Y: int(o.LogicalY), Width: int(o.LogicalW), Height: int(o.LogicalH)}
	if m.Width <= 0 || m.Height <= 0 {
		scale := max(o.Scale, 1)
		m.Width, m.Height = o.ModeW/scale, o.ModeH/scale
	}
	return m
}

// rect is the monitor's global logical rectangle.
func (m Monitor) rect() image.Rectangle { return image.Rect(m.X, m.Y, m.X+m.Width, m.Y+m.Height) }

// layout is the bounding box of the monitors.
func layout(mons []Monitor) image.Rectangle {
	var b image.Rectangle
	for _, m := range mons {
		b = b.Union(m.rect())
	}
	return b
}

// finalize resolves a global selection to an output-relative one: the
// output is the one containing the rectangle's top-left corner, else
// the first monitor. An empty rectangle is no selection.
func finalize(r image.Rectangle, mons []Monitor) (Selection, bool) {
	if r.Empty() || len(mons) == 0 {
		return Selection{}, false
	}
	mon := mons[0]
	for _, m := range mons {
		if r.Min.In(m.rect()) {
			mon = m
			break
		}
	}
	return Selection{Output: mon.Connector, X: r.Min.X - mon.X, Y: r.Min.Y - mon.Y, Width: r.Dx(), Height: r.Dy()}, true
}

// pick is what a click at p selects: the topmost window under it, else
// the monitor it is on.
func pick(p image.Point, windows []image.Rectangle, mons []Monitor) (image.Rectangle, bool) {
	for _, w := range windows {
		if p.In(w) {
			return w, true
		}
	}
	for _, m := range mons {
		if p.In(m.rect()) {
			return m.rect(), true
		}
	}
	return image.Rectangle{}, false
}

// Paint constants of region_overlay/mod.rs's draw func.
const (
	// dimAlpha is the wash: rgba(0, 0, 0, 0.35).
	dimAlpha    = 89
	borderWidth = 2
	labelGap    = 4
	// handleSize is the edge of the square handles on the corners and
	// edge midpoints of a selection up for adjustment.
	handleSize = 7
	hintMargin = 24
)

// hintText tells how to finish once a selection is up for adjustment.
const hintText = "Drag to adjust · Enter or double-click to confirm · Esc to cancel"

// labelBacking is the pills' dark backing, rgba(0, 0, 0, 0.75).
var labelBacking = render.Color(191 << 24)

// The evdev codes of the overlay's keys.
const (
	keyEscape      = 1
	keyEnter       = 28
	keyKeypadEnter = 96
	keyUp          = 103
	keyLeft        = 105
	keyRight       = 106
	keyDown        = 108
)

// nudgeStep is how far an arrow key moves the selection; Shift moves
// it nudgeStepShift.
const (
	nudgeStep      = 1
	nudgeStepShift = 10
)

// result is one request's answer.
type result struct {
	sel Selection
	ok  bool
}

// Overlay is the overlay component. Request is its only entry point;
// everything else runs on the lys-ui loop goroutine.
type Overlay struct {
	app     *app.Application
	outputs func() []*app.Output
	// windows lists the visible windows' global logical rectangles,
	// topmost first, that a click picks from; nil picks outputs only.
	windows func() []image.Rectangle
	// font is the size label's monospace bold face; sheet styles the
	// surfaces, the area's color (--palette-primary) being the accent.
	font  render.Font
	sheet *widget.Stylesheet

	reply    chan result
	surfaces []*app.LayerWindow
	areas    []*area
	monitors []Monitor
	// shown are the windows a click picks from, read when the request
	// opened.
	shown []image.Rectangle
	// ed is the selection every surface paints.
	ed editor
	// pointer is the last global pointer position any surface saw,
	// where a press lands; onSurface is false once it left them all.
	pointer   image.Point
	onSurface bool
}

// New builds the overlay on the application; outputs lists the
// monitors to cover at each request, windows (nil for none) the
// windows a click can pick.
func New(a *app.Application, outputs func() []*app.Output, windows func() []image.Rectangle, font render.Font, sheet *widget.Stylesheet) *Overlay {
	return &Overlay{app: a, outputs: outputs, windows: windows, font: font, sheet: sheet}
}

// surfaceRoot is one surface's tree, window.region-overlay-window >
// .region-overlay-area, on the stylesheet.
func (o *Overlay) surfaceRoot(a *area) widget.Widget {
	a.AddClass("region-overlay-area")
	root := widget.NewBox(widget.Column, 0, 0)
	root.SetElement("window")
	root.AddClass("region-overlay-window")
	if o.sheet != nil {
		root.AttachStylesheet(o.sheet)
	}
	root.Append(a, true)
	return root
}

// Request opens the overlay and blocks until the user confirms a
// region (ok) or cancels with Escape (not ok). frames maps
// connector names to frozen frames painted behind the wash; nil or a
// missing connector leaves that surface transparent over the live
// screen. A request arriving while one is open cancels the older one.
// Call it from any goroutine but the loop's.
func (o *Overlay) Request(frames map[string]render.StillImage) (Selection, bool) {
	ch := make(chan result, 1)
	o.app.Invoke(func() { o.show(ch, frames) })
	r := <-ch
	return r.sel, r.ok
}

// Cancel answers the open request, if any, as cancelled and takes the
// surfaces down. Call it from any goroutine but the loop's.
func (o *Overlay) Cancel() {
	o.app.Invoke(func() { o.finish(Selection{}, false) })
}

// show replaces any open request with a fresh one and maps the
// surfaces.
func (o *Overlay) show(ch chan result, frames map[string]render.StillImage) {
	o.finish(Selection{}, false)
	o.reply = ch
	o.open(frames)
	if len(o.surfaces) == 0 {
		log.Print("region overlay: no surface could be mapped")
		o.finish(Selection{}, false)
	}
}

// open maps one surface per monitor.
func (o *Overlay) open(frames map[string]render.StillImage) {
	outs := o.outputs()
	o.monitors = o.monitors[:0]
	for _, out := range outs {
		o.monitors = append(o.monitors, MonitorOf(out))
	}
	o.ed = editor{bounds: layout(o.monitors)}
	o.shown = nil
	if o.windows != nil {
		o.shown = o.windows()
	}
	for i, out := range outs {
		mon := o.monitors[i]
		a := &area{o: o, mon: mon, frame: frames[mon.Connector]}
		layer, err := o.app.NewLayer(app.LayerConfig{
			Output:        out,
			Layer:         app.LayerOverlay,
			Anchor:        app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
			ExclusiveZone: -1,
			Keyboard:      app.KeyboardExclusive,
			Namespace:     "lys-shell-region-overlay",
			Root:          o.surfaceRoot(a),
			OnKey:         func(_ *widget.Router, keycode uint32, mods app.Mods) { o.key(keycode, mods) },
		})
		if err != nil {
			log.Printf("region overlay: surface on %s: %v", mon.Connector, err)
			continue
		}
		o.surfaces = append(o.surfaces, layer)
		o.areas = append(o.areas, a)
	}
}

// finish tears the surfaces down and then answers the open request (if
// any): once the requester has its answer nothing draws its frames, so
// it may release them.
func (o *Overlay) finish(sel Selection, ok bool) {
	for _, s := range o.surfaces {
		s.Close()
	}
	o.surfaces, o.areas, o.ed = nil, nil, editor{}
	if o.reply != nil {
		o.reply <- result{sel: sel, ok: ok}
		o.reply = nil
	}
}

// confirm answers the selection; without one it does nothing.
func (o *Overlay) confirm() {
	if sel, ok := finalize(o.ed.sel, o.monitors); ok {
		o.finish(sel, true)
	}
}

// key handles the overlay's keys: Escape cancels, Enter confirms, the
// arrows nudge the selection (further with Shift).
func (o *Overlay) key(keycode uint32, mods app.Mods) {
	step := nudgeStep
	if mods&app.ModShift != 0 {
		step = nudgeStepShift
	}
	switch keycode {
	case keyEscape:
		o.finish(Selection{}, false)
	case keyEnter, keyKeypadEnter:
		o.confirm()
	case keyLeft:
		o.ed.nudge(-step, 0)
	case keyRight:
		o.ed.nudge(step, 0)
	case keyUp:
		o.ed.nudge(0, -step)
	case keyDown:
		o.ed.nudge(0, step)
	default:
		return
	}
	o.redraw()
}

// press starts a gesture at the last pointer position.
func (o *Overlay) press() {
	o.ed.press(o.pointer)
	o.redraw()
}

// move follows the gesture to a global position.
func (o *Overlay) move(p image.Point) {
	o.pointer = p
	o.ed.drag(p)
	o.redraw()
}

// release ends the gesture: a click picks what lies under it.
func (o *Overlay) release() {
	o.ed.release(func(p image.Point) (image.Rectangle, bool) { return pick(p, o.shown, o.monitors) })
	o.redraw()
}

func (o *Overlay) redraw() {
	for _, a := range o.areas {
		a.Invalidate()
	}
}

// area is one monitor's drawing area: the frozen frame (if any), the
// wash with its hole, the border, and the label.
type area struct {
	widget.Base
	o   *Overlay
	mon Monitor
	// frame is the frozen frame, without pixels over the live screen.
	frame render.StillImage
}

// frozen reports whether the area paints a frozen frame.
func (a *area) frozen() bool { return a.frame.RGBA != nil }

// Measure asks for nothing: the area takes the whole output.
func (a *area) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

func (a *area) HitTest(p widget.Point) widget.Widget { return a.HitLeaf(a, p) }

// CursorName implements widget.CursorNamer: the shape for what a press
// at the pointer would do.
func (a *area) CursorName() string { return a.o.ed.cursor(a.o.pointer) }

// global maps a surface-local point to the layout.
func (a *area) global(p widget.Point) image.Point {
	b := a.Bounds()
	return image.Pt(a.mon.X+p.X-b.X, a.mon.Y+p.Y-b.Y)
}

// HoverMove implements widget.HoverMover: track where a press lands,
// which the magnifier and the cursor follow.
func (a *area) HoverMove(p widget.Point) {
	a.o.pointer, a.o.onSurface = a.global(p), true
	a.o.redraw()
}

// SetHovered implements widget.HoverSetter: leaving every surface
// hides the magnifier.
func (a *area) SetHovered(on bool) {
	if !on {
		a.o.onSurface = false
		a.o.redraw()
	}
}

// SetPressed implements widget.PressSetter: a press starts a gesture.
func (a *area) SetPressed(on bool) {
	if on {
		a.o.press()
	}
}

// DragMove implements widget.DragMover. Under the implicit grab the
// point can lie beyond this surface; mapping it through the monitor
// offset carries the gesture across outputs.
func (a *area) DragMove(p widget.Point) { a.o.move(a.global(p)) }

// PressEnd implements widget.PressEnder: the release (or a lost
// pointer) ends the gesture.
func (a *area) PressEnd() { a.o.release() }

// DoubleClickAt implements widget.DoubleClicker: a double-click on the
// selection confirms it.
func (a *area) DoubleClickAt(p widget.Point) {
	if a.global(p).In(a.o.ed.sel) {
		a.o.confirm()
	}
}

// local maps a global rectangle into this surface's coordinates.
func (a *area) local(r image.Rectangle) render.Rect {
	b := a.Bounds()
	return render.Rect{X: r.Min.X - a.mon.X + b.X, Y: r.Min.Y - a.mon.Y + b.Y, W: r.Dx(), H: r.Dy()}
}

func (a *area) Paint(cv *render.Canvas) {
	b := a.Bounds()
	dev := cv.MapRect(b)
	ed := &a.o.ed
	selected := !ed.sel.Empty()
	sel := a.local(ed.sel)
	selDev := cv.MapRect(sel).Intersect(dev)
	if a.frozen() {
		loupe.DrawFrame(cv, a.frame, b)
		for _, r := range around(dev, selDev) {
			cv.FillRectDevice(r, render.Color(dimAlpha<<24))
		}
	} else {
		cv.ClearDevice(dev, render.Color(dimAlpha<<24))
		if selected && !selDev.Empty() {
			cv.ClearDevice(selDev, 0)
		}
	}
	if selected {
		a.paintBorderAndLabel(cv, sel)
	}
	if ed.adjusting() {
		a.paintHandles(cv, sel)
		a.paintHint(cv, b)
	}
	if a.magnifying() {
		p := a.o.pointer
		lx, ly := float64(p.X-a.mon.X), float64(p.Y-a.mon.Y)
		cx, cy := loupe.SampleAt(a.frame.RGBA, a.mon.Width, a.mon.Height, lx, ly)
		x0, y0 := loupe.Place(b, lx, ly, loupe.Size, loupe.Size)
		cv.FillRect(render.Rect{X: x0 - 2, Y: y0 - 2, W: loupe.Size + 4, H: loupe.Size + 4}, labelBacking)
		loupe.Draw(cv, a.frame.RGBA, cx, cy, x0, y0)
	}
}

// magnifying reports whether this surface shows the magnifier: it has
// a frozen frame, the pointer is on it, and an edge is being placed
// (before the first press, while drawing, or while resizing).
func (a *area) magnifying() bool {
	ed := &a.o.ed
	placing := ed.gesture == gestureCreate || ed.gesture == gestureResize || ed.sel.Empty()
	return a.frozen() && a.o.onSurface && placing && a.o.pointer.In(a.mon.rect())
}

// around is outer less hole: the bands above and below the hole across
// outer's width, and those left and right of it, the empty ones left
// out. Without a hole it is outer whole.
func around(outer, hole render.Rect) []render.Rect {
	hole = hole.Intersect(outer)
	if hole.Empty() {
		return []render.Rect{outer}
	}
	bands := []render.Rect{
		{X: outer.X, Y: outer.Y, W: outer.W, H: hole.Y - outer.Y},
		{X: outer.X, Y: hole.Y + hole.H, W: outer.W, H: outer.Y + outer.H - hole.Y - hole.H},
		{X: outer.X, Y: hole.Y, W: hole.X - outer.X, H: hole.H},
		{X: hole.X + hole.W, Y: hole.Y, W: outer.X + outer.W - hole.X - hole.W, H: hole.H},
	}
	return slices.DeleteFunc(bands, render.Rect.Empty)
}

// paintBorderAndLabel strokes the accent border (2px, centered on the
// selection edge like cairo's stroke) and draws the "W × H" label on
// its dark backing box, above the selection when it fits, else just
// inside it. The accent is the area's color, as the draw func reads
// it, so it follows the palette.
func (a *area) paintBorderAndLabel(cv *render.Canvas, sel render.Rect) {
	accent := widget.CascadeColor(a)
	cv.BorderRect(render.Rect{X: sel.X - borderWidth/2, Y: sel.Y - borderWidth/2, W: sel.W + borderWidth, H: sel.H + borderWidth}, borderWidth, accent)
	if a.o.font == nil {
		return
	}
	p := a.pill(labelText(sel.W, sel.H))
	bx, by := labelOrigin(sel, p.h)
	p.paint(cv, bx, by, accent)
}

// pill is text shaped at the body size on a dark backing padded
// and rounded by the theme.
type pill struct {
	font   render.Font
	shaped *render.ShapedText
	ascent int
	pad    int
	radius int
	w, h   int
}

func (a *area) pill(text string) pill {
	th := widget.Current()
	shaped := a.o.font.Shape(text, widgets.TextPx(styling.Body, 1))
	ascent := int(math.Ceil(shaped.Ascent()))
	return pill{
		font: a.o.font, shaped: shaped, ascent: ascent, pad: th.Padding, radius: th.Radius,
		w: int(math.Ceil(shaped.Advance())) + 2*th.Padding,
		h: ascent + 2*th.Padding,
	}
}

func (p pill) paint(cv *render.Canvas, x, y int, ink render.Color) {
	cv.RoundedRect(render.Rect{X: x, Y: y, W: p.w, H: p.h}, p.radius, labelBacking)
	p.font.Draw(cv, p.shaped, x+p.pad, y+p.pad+p.ascent, ink)
}

// handleRects are the handles' squares: the four corners, then the
// four edge midpoints, centred on the selection's edge.
func handleRects(sel render.Rect) []render.Rect {
	h := handleSize
	xs := []int{sel.X, sel.X + sel.W/2, sel.X + sel.W}
	ys := []int{sel.Y, sel.Y + sel.H/2, sel.Y + sel.H}
	at := func(x, y int) render.Rect { return render.Rect{X: x - h/2, Y: y - h/2, W: h, H: h} }
	return []render.Rect{
		at(xs[0], ys[0]), at(xs[2], ys[0]), at(xs[0], ys[2]), at(xs[2], ys[2]),
		at(xs[1], ys[0]), at(xs[1], ys[2]), at(xs[0], ys[1]), at(xs[2], ys[1]),
	}
}

// paintHandles fills the handles in the accent, ringed in the label's
// dark backing so they read on any content.
func (a *area) paintHandles(cv *render.Canvas, sel render.Rect) {
	accent := widget.CascadeColor(a)
	for _, r := range handleRects(sel) {
		cv.FillRect(render.Rect{X: r.X - 1, Y: r.Y - 1, W: r.W + 2, H: r.H + 2}, labelBacking)
		cv.FillRect(r, accent)
	}
}

// paintHint draws how to finish, centred at the bottom of the surface.
func (a *area) paintHint(cv *render.Canvas, b render.Rect) {
	if a.o.font == nil {
		return
	}
	p := a.pill(hintText)
	p.paint(cv, b.X+(b.W-p.w)/2, b.Y+b.H-hintMargin-p.h, widget.CascadeColor(a))
}

// labelText is the size label in logical pixels.
func labelText(w, h int) string { return fmt.Sprintf("%d × %d", w, h) }

// labelOrigin places the label box at the selection's left edge,
// above it when there is room on the surface, else just inside.
func labelOrigin(sel render.Rect, boxH int) (int, int) {
	if above := sel.Y - boxH - labelGap; above >= 0 {
		return sel.X, above
	}
	return sel.X, sel.Y + labelGap
}
