package filechooser

import (
	"slices"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// dropZone is a box that takes a file dropped anywhere on it: onDrop
// receives the first dropped path. Embedding the box keeps it the
// parent its children's drop walk reaches.
type dropZone struct {
	*widget.Box
	onDrop func(path string)
}

// DragEnter accepts a file drag.
func (z *dropZone) DragEnter(mimes []string, _ widget.Point) string {
	if slices.Contains(mimes, uriListMime) {
		return uriListMime
	}
	return ""
}

// Drop hands on the first dropped path.
func (z *dropZone) Drop(_ string, data []byte, _ widget.Point) {
	if p, ok := droppedPath(data); ok {
		z.onDrop(p)
	}
}

// columnGrip is the column header's resize handle, filling the gap
// before its column:
// onStart hears a press, onDrag the horizontal offset from it, onEnd
// the release.
type columnGrip struct {
	widget.Base
	anchor  widget.Point
	onStart func()
	onDrag  func(dx int)
	onEnd   func()
}

func newColumnGrip() *columnGrip {
	g := &columnGrip{}
	g.SetCursorName("col-resize")
	return g
}

func (g *columnGrip) Measure(con widget.Constraints) widget.Size {
	return con.Min
}

func (g *columnGrip) Paint(*render.Canvas) {}

func (g *columnGrip) HitTest(p widget.Point) widget.Widget { return g.HitLeaf(g, p) }

// PressAt anchors the drag where the press landed.
func (g *columnGrip) PressAt(p widget.Point) {
	g.anchor = p
	g.onStart()
}

// DragMove reports the offset from the press.
func (g *columnGrip) DragMove(p widget.Point) { g.onDrag(p.X - g.anchor.X) }

// PressEnd ends the drag.
func (g *columnGrip) PressEnd() { g.onEnd() }

// hideOrder is the order the size (0), modified (1) and kind (2)
// columns give way in when the name column would fall under nameMinW:
// the kind the icon already tells first, then the size, the
// modification time a picker sorts by last.
var hideOrder = [...]int{2, 0, 1}

// shownColumns is which of the fixed columns fit beside a name column
// of at least nameMinW in a row w wide, with spacing before each shown
// column.
func shownColumns(w, spacing int, widths [3]int) [3]bool {
	shown := [3]bool{true, true, true}
	used := 0
	for _, cw := range widths {
		used += spacing + cw
	}
	for _, i := range hideOrder {
		if w-used >= nameMinW {
			break
		}
		shown[i] = false
		used -= spacing + widths[i]
	}
	return shown
}

// columnRow lays out a list row, or the column header over the rows:
// the lead (the name) takes the width the fixed columns leave, each
// column sits spacing after the one before it, and when the lead would
// fall under nameMinW the columns give way in hideOrder. The header and
// the rows share it, so a header sits over its values by construction.
// gaps, the header's column grips, fill the spacing before their
// column; a row has none.
type columnRow struct {
	widget.Base
	lead    widget.Widget
	cols    [3]widget.Widget
	gaps    [3]widget.Widget
	widths  [3]int
	spacing int
	kids    []widget.Widget
}

func newColumnRow(lead widget.Widget, cols, gaps [3]widget.Widget, widths [3]int, spacing int) *columnRow {
	r := &columnRow{lead: lead, cols: cols, gaps: gaps, widths: widths, spacing: spacing}
	r.kids = append(r.kids, lead)
	for i, col := range cols {
		if gaps[i] != nil {
			r.kids = append(r.kids, gaps[i])
		}
		r.kids = append(r.kids, col)
	}
	return r
}

// room is the widest column i can grow to while the name keeps
// nameMinW beside the other columns shown.
func (r *columnRow) room(i int) int {
	shown := shownColumns(r.Bounds().W, r.spacing, r.widths)
	w := r.Bounds().W - nameMinW - r.spacing
	for j, cw := range r.widths {
		if j != i && shown[j] {
			w -= r.spacing + cw
		}
	}
	return w
}

// setWidths re-lays the columns at new widths.
func (r *columnRow) setWidths(widths [3]int) {
	if widths != r.widths {
		r.widths = widths
		r.InvalidateLayout()
	}
}

// Measure asks for the name column's floor: the columns give way below
// their widths instead of widening the view.
func (r *columnRow) Measure(con widget.Constraints) widget.Size {
	widget.SetParents(r, r.kids...)
	h := 0
	for _, k := range r.kids {
		h = max(h, k.Measure(widget.Constraints{Max: widget.Size{W: con.Max.W, H: con.Max.H}}).H)
	}
	return widget.Size{W: min(max(nameMinW, con.Min.W), con.Max.W), H: min(max(h, con.Min.H), con.Max.H)}
}

func (r *columnRow) Arrange(rect render.Rect) {
	r.ArrangeSelf(rect)
	widget.SetParents(r, r.kids...)
	shown := shownColumns(rect.W, r.spacing, r.widths)
	right := rect.X + rect.W
	for i := len(r.cols) - 1; i >= 0; i-- {
		setShown(r.cols[i], shown[i])
		setShown(r.gaps[i], shown[i])
		if !shown[i] {
			continue
		}
		right -= r.widths[i]
		r.place(r.cols[i], render.Rect{X: right, Y: rect.Y, W: r.widths[i], H: rect.H})
		right -= r.spacing
		if r.gaps[i] != nil {
			r.place(r.gaps[i], render.Rect{X: right, Y: rect.Y, W: r.spacing, H: rect.H})
		}
	}
	r.place(r.lead, render.Rect{X: rect.X, Y: rect.Y, W: right - rect.X, H: rect.H})
}

// place lays k out in exactly rect.
func (r *columnRow) place(k widget.Widget, rect render.Rect) {
	size := widget.Size{W: rect.W, H: rect.H}
	k.Measure(widget.Constraints{Min: size, Max: size})
	k.Arrange(rect)
}

// setShown shows or hides a column's widget, nil for a missing gap.
func setShown(w widget.Widget, on bool) {
	if v, ok := w.(interface{ SetVisible(bool) }); ok {
		v.SetVisible(on)
	}
}

func (r *columnRow) Paint(cv *render.Canvas) {
	for _, k := range r.kids {
		widget.PaintChild(cv, k)
	}
}

func (r *columnRow) HitTest(p widget.Point) widget.Widget {
	for _, k := range r.kids {
		if hit := widget.HitChild(k, p); hit != nil {
			return hit
		}
	}
	return nil
}

// Children exposes the cells to the tree walks and the cascade.
func (r *columnRow) Children() []widget.Widget { return r.kids }
