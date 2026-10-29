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

// nearest is the choice whose centre lies strictly beyond from's in
// dir, closest along dir and then across it; nil when none does. The
// window grid and the output map, which places cards as the outputs
// sit, both navigate by where the cards are.
func nearest(choices []widget.Widget, from widget.Widget, dir direction) widget.Widget {
	fx, fy := centre(from)
	var best widget.Widget
	bestAlong, bestAcross := 0, 0
	for _, c := range choices {
		cx, cy := centre(c)
		along := (cx-fx)*dir.dx + (cy-fy)*dir.dy
		if along <= 0 {
			continue
		}
		across := abs((cx-fx)*dir.dy + (cy-fy)*dir.dx)
		if best == nil || along < bestAlong || along == bestAlong && across < bestAcross {
			best, bestAlong, bestAcross = c, along, across
		}
	}
	return best
}

// centre is the centre of w's laid-out bounds.
func centre(w widget.Widget) (x, y int) {
	var r render.Rect
	if b, ok := w.(widget.Boundser); ok {
		r = b.Bounds()
	}
	return r.X + r.W/2, r.Y + r.H/2
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
