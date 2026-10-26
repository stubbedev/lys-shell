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

// columnGrip is the column header's resize handle, colGripPx wide:
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
	return widget.Size{W: min(max(colGripPx, con.Min.W), con.Max.W), H: con.Min.H}
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
