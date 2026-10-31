package sharepicker

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/unxed/xkb-go"
)

// direction is an arrow key's way across the page, as a unit step.
type direction struct{ dx, dy int }

// arrowDirs maps the arrow keysyms to their directions.
var arrowDirs = map[xkb.Keysym]direction{
	xkb.KeyLeft:  {dx: -1},
	xkb.KeyRight: {dx: 1},
	xkb.KeyUp:    {dy: -1},
	xkb.KeyDown:  {dy: 1},
}

// nearest is the index of the rect in rects that lies beyond from in
// dir and shares part of its span across dir (GTK's directional focus
// keeps to the widgets overlapping the focused one so), the closest
// centre along dir and then across it; -1 when none does. The window
// grid and the output map, which places cards as the outputs sit, both
// navigate by where the cards are, so a grid's short last row has
// nothing to its right and a column without a card below has nothing
// down.
func nearest(rects []render.Rect, from render.Rect, dir direction) int {
	fx, fy := centre(from)
	best, bestAlong, bestAcross := -1, 0, 0
	for i, r := range rects {
		if !overlapsAcross(from, r, dir) {
			continue
		}
		cx, cy := centre(r)
		along := (cx-fx)*dir.dx + (cy-fy)*dir.dy
		if along <= 0 {
			continue
		}
		across := abs((cx-fx)*dir.dy + (cy-fy)*dir.dx)
		if best < 0 || along < bestAlong || along == bestAlong && across < bestAcross {
			best, bestAlong, bestAcross = i, along, across
		}
	}
	return best
}

// overlapsAcross reports whether a and b share part of their spans on
// the axis across dir.
func overlapsAcross(a, b render.Rect, dir direction) bool {
	if dir.dx != 0 {
		return a.Y < b.Y+b.H && b.Y < a.Y+a.H
	}
	return a.X < b.X+b.W && b.X < a.X+a.W
}

// centre is r's centre.
func centre(r render.Rect) (x, y int) { return r.X + r.W/2, r.Y + r.H/2 }

// bounds is w's laid-out bounds.
func bounds(w widget.Widget) render.Rect {
	if b, ok := w.(widget.Boundser); ok {
		return b.Bounds()
	}
	return render.Rect{}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// choicesIn lists the widgets under root carrying choiceClass, in paint
// order: a page's keyboard stops.
func choicesIn(root widget.Widget) []widget.Widget {
	var out []widget.Widget
	var walk func(widget.Widget)
	walk = func(w widget.Widget) {
		if s, ok := w.(interface{ HasClass(string) bool }); ok && s.HasClass(choiceClass) {
			out = append(out, w)
		}
		if c, ok := w.(interface{ Children() []widget.Widget }); ok {
			for _, k := range c.Children() {
				walk(k)
			}
		}
	}
	walk(root)
	return out
}
