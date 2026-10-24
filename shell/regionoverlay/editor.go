package regionoverlay

import "image"

// gesture is what the press under way does to the selection.
type gesture uint8

const (
	gestureNone gesture = iota
	// gestureCreate draws a new rectangle from the press.
	gestureCreate
	// gestureMove carries the selection with the pointer.
	gestureMove
	// gestureResize drags the selection's grabbed edges.
	gestureResize
)

// edges is a set of the selection's edges a resize drags.
type edges uint8

const (
	edgeLeft edges = 1 << iota
	edgeRight
	edgeTop
	edgeBottom
)

// handleReach is how far from an edge, in logical pixels, a press
// still grabs it.
const handleReach = 8

// editor is the selection being made, in global logical pixels: drawn
// by a drag, then moved, resized and nudged until it is confirmed; a
// plain click picks what lies under it.
type editor struct {
	// bounds is the layout every selection stays inside.
	bounds image.Rectangle
	// sel is the selection; empty before there is one.
	sel image.Rectangle

	gesture gesture
	grabbed edges
	// anchor is where the press landed, from the selection the press
	// began with.
	anchor image.Point
	from   image.Rectangle
	moved  bool
}

// grabbedAt is the edges within reach of p on the selection, none
// away from it.
func (e *editor) grabbedAt(p image.Point) edges {
	if e.sel.Empty() || !e.sel.Inset(-handleReach).Overlaps(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}) {
		return 0
	}
	near := func(a, b int) bool { return abs(a-b) <= handleReach }
	var g edges
	if near(p.X, e.sel.Min.X) {
		g |= edgeLeft
	} else if near(p.X, e.sel.Max.X) {
		g |= edgeRight
	}
	if near(p.Y, e.sel.Min.Y) {
		g |= edgeTop
	} else if near(p.Y, e.sel.Max.Y) {
		g |= edgeBottom
	}
	return g
}

// press starts the gesture p calls for: resizing a grabbed edge,
// moving the selection from inside it, else drawing a new one.
func (e *editor) press(p image.Point) {
	e.anchor, e.from, e.moved = p, e.sel, false
	switch g := e.grabbedAt(p); {
	case g != 0:
		e.gesture, e.grabbed = gestureResize, g
	case p.In(e.sel):
		e.gesture = gestureMove
	default:
		e.gesture = gestureCreate
	}
}

// drag follows the pointer to p.
func (e *editor) drag(p image.Point) {
	if e.gesture == gestureNone {
		return
	}
	e.moved = e.moved || p != e.anchor
	d := p.Sub(e.anchor)
	switch e.gesture {
	case gestureCreate:
		e.sel = image.Rectangle{Min: e.anchor, Max: p}.Canon().Intersect(e.bounds)
	case gestureMove:
		e.sel = e.inside(e.from.Add(d))
	case gestureResize:
		r := e.from
		if e.grabbed&edgeLeft != 0 {
			r.Min.X += d.X
		}
		if e.grabbed&edgeRight != 0 {
			r.Max.X += d.X
		}
		if e.grabbed&edgeTop != 0 {
			r.Min.Y += d.Y
		}
		if e.grabbed&edgeBottom != 0 {
			r.Max.Y += d.Y
		}
		e.sel = r.Canon().Intersect(e.bounds)
	}
}

// release ends the gesture. A press that never moved and drew nothing
// is a click: it selects what pick finds under it, clipped to the
// layout (or keeps the selection when pick finds nothing).
func (e *editor) release(pick func(image.Point) (image.Rectangle, bool)) {
	clicked := e.gesture == gestureCreate && !e.moved
	e.gesture, e.grabbed = gestureNone, 0
	if !clicked {
		return
	}
	if r, ok := pick(e.anchor); ok {
		e.sel = r.Intersect(e.bounds)
	} else {
		e.sel = e.from
	}
}

// nudge moves the selection by (dx, dy), stopping at the layout's
// edge.
func (e *editor) nudge(dx, dy int) {
	if e.sel.Empty() {
		return
	}
	e.sel = e.inside(e.sel.Add(image.Pt(dx, dy)))
}

// inside shifts r back inside the layout without resizing it (a
// rectangle larger than the layout is clipped to it).
func (e *editor) inside(r image.Rectangle) image.Rectangle {
	if r.Max.X > e.bounds.Max.X {
		r = r.Add(image.Pt(e.bounds.Max.X-r.Max.X, 0))
	}
	if r.Max.Y > e.bounds.Max.Y {
		r = r.Add(image.Pt(0, e.bounds.Max.Y-r.Max.Y))
	}
	if r.Min.X < e.bounds.Min.X {
		r = r.Add(image.Pt(e.bounds.Min.X-r.Min.X, 0))
	}
	if r.Min.Y < e.bounds.Min.Y {
		r = r.Add(image.Pt(0, e.bounds.Min.Y-r.Min.Y))
	}
	return r.Intersect(e.bounds)
}

// adjusting reports a selection that is up for adjustment: one exists
// and no drag is drawing it.
func (e *editor) adjusting() bool { return !e.sel.Empty() && e.gesture != gestureCreate }

// cursor is the pointer shape at p: a resize arrow over a grabbable
// edge or corner, the move hand inside the selection, else the
// crosshair that draws a new one.
func (e *editor) cursor(p image.Point) string {
	g := e.grabbed
	if e.gesture == gestureNone {
		g = e.grabbedAt(p)
	}
	switch {
	case e.gesture == gestureCreate:
		return "crosshair"
	case g == edgeLeft|edgeTop, g == edgeRight|edgeBottom:
		return "nwse-resize"
	case g == edgeRight|edgeTop, g == edgeLeft|edgeBottom:
		return "nesw-resize"
	case g&(edgeLeft|edgeRight) != 0:
		return "ew-resize"
	case g != 0:
		return "ns-resize"
	case e.gesture == gestureMove || p.In(e.sel):
		return "move"
	}
	return "crosshair"
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
