package regionoverlay

import (
	"image"
	"testing"
)

func newEditor() *editor { return &editor{bounds: image.Rect(0, 0, 1000, 800)} }

// stroke presses at from, drags to to and releases with pick.
func (e *editor) stroke(from, to image.Point, pick func(image.Point) (image.Rectangle, bool)) {
	e.press(from)
	e.drag(to)
	e.release(pick)
}

func noPick(image.Point) (image.Rectangle, bool) { return image.Rectangle{}, false }

// A drag draws a canonical rectangle clipped to the layout, whichever
// way it went.
func TestEditorDraws(t *testing.T) {
	e := newEditor()
	e.stroke(image.Pt(300, 200), image.Pt(100, 50), noPick)
	if e.sel != image.Rect(100, 50, 300, 200) {
		t.Errorf("reverse drag = %v", e.sel)
	}
	e = newEditor()
	e.stroke(image.Pt(900, 700), image.Pt(1200, 900), noPick)
	if e.sel != image.Rect(900, 700, 1000, 800) {
		t.Errorf("drag past the layout = %v", e.sel)
	}
}

// Moving keeps the size and stops at the layout's edge; a resize drags
// only the grabbed edges and flips cleanly past the opposite one.
func TestEditorMovesAndResizes(t *testing.T) {
	e := newEditor()
	e.sel = image.Rect(100, 100, 300, 200)
	e.stroke(image.Pt(200, 150), image.Pt(-500, 150), noPick)
	if e.sel != image.Rect(0, 100, 200, 200) {
		t.Errorf("moved past the left edge = %v", e.sel)
	}
	e.stroke(image.Pt(200, 150), image.Pt(260, 190), noPick)
	if e.sel != image.Rect(0, 100, 260, 200) {
		t.Errorf("right edge dragged = %v", e.sel)
	}
	e.stroke(image.Pt(0, 150), image.Pt(460, 150), noPick)
	if e.sel != image.Rect(260, 100, 460, 200) {
		t.Errorf("left edge dragged past the right = %v", e.sel)
	}
	e.stroke(image.Pt(260, 100), image.Pt(150, 40), noPick)
	if e.sel != image.Rect(150, 40, 460, 200) {
		t.Errorf("top-left corner dragged = %v", e.sel)
	}
}

// A press that never moves picks; with nothing to pick, the selection
// it began with stays.
func TestEditorClickPicks(t *testing.T) {
	e := newEditor()
	window := image.Rect(-50, 10, 400, 300)
	e.stroke(image.Pt(600, 600), image.Pt(600, 600), func(p image.Point) (image.Rectangle, bool) {
		if p != image.Pt(600, 600) {
			t.Errorf("picked at %v", p)
		}
		return window, true
	})
	if e.sel != image.Rect(0, 10, 400, 300) {
		t.Errorf("click selected %v, want the window clipped to the layout", e.sel)
	}
	e.stroke(image.Pt(900, 700), image.Pt(900, 700), noPick)
	if e.sel != image.Rect(0, 10, 400, 300) {
		t.Errorf("a click on nothing replaced the selection: %v", e.sel)
	}
}

// Nudging moves within the layout, and does nothing without a
// selection.
func TestEditorNudges(t *testing.T) {
	e := newEditor()
	e.nudge(5, 5)
	if !e.sel.Empty() {
		t.Errorf("a nudge made a selection: %v", e.sel)
	}
	e.sel = image.Rect(990, 0, 1000, 10)
	e.nudge(10, -10)
	if e.sel != image.Rect(990, 0, 1000, 10) {
		t.Errorf("nudged past the corner: %v", e.sel)
	}
	e.nudge(-10, 10)
	if e.sel != image.Rect(980, 10, 990, 20) {
		t.Errorf("nudged = %v", e.sel)
	}
}

// The cursor says what a press would do there.
func TestEditorCursor(t *testing.T) {
	e := newEditor()
	if got := e.cursor(image.Pt(5, 5)); got != "crosshair" {
		t.Errorf("no selection = %q", got)
	}
	e.sel = image.Rect(100, 100, 300, 200)
	for p, want := range map[image.Point]string{
		image.Pt(100, 100): "nwse-resize",
		image.Pt(305, 205): "nwse-resize",
		image.Pt(300, 100): "nesw-resize",
		image.Pt(96, 150):  "ew-resize",
		image.Pt(200, 203): "ns-resize",
		image.Pt(200, 150): "move",
		image.Pt(320, 150): "crosshair",
	} {
		if got := e.cursor(p); got != want {
			t.Errorf("cursor at %v = %q, want %q", p, got, want)
		}
	}
	e.press(image.Pt(500, 500))
	if got := e.cursor(image.Pt(200, 150)); got != "crosshair" {
		t.Errorf("while drawing = %q", got)
	}
}
