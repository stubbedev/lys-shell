// Package markup is the screenshot markup editor: annotations drawn over a
// captured image (crop, arrow, rectangle, ellipse, freehand, text,
// highlighter, numbered markers and pixelation) with undo and redo, painted
// live on screen and flattened into the pixels that are saved.
package markup

import (
	"image"
	"slices"

	"github.com/stubbedev/lys-lib/render"
)

// Tool is what a drag on the image draws.
type Tool uint8

// The editor's tools.
const (
	ToolArrow Tool = iota
	ToolRect
	ToolEllipse
	ToolFreehand
	ToolText
	ToolHighlight
	ToolMarker
	ToolPixelate
	ToolCrop
)

// Tools lists every tool in toolbar order.
var Tools = []Tool{ToolCrop, ToolArrow, ToolRect, ToolEllipse, ToolFreehand, ToolText, ToolHighlight, ToolMarker, ToolPixelate}

// Mark is one annotation in image pixels. Points holds the drag's
// endpoints (every sampled point for freehand); Size is the stroke width;
// Text is a text mark's string; Number is a marker's ordinal.
type Mark struct {
	Tool   Tool
	Points []image.Point
	Color  render.Color
	Size   int
	Text   string
	Number int
	cells  []render.Color
}

// Rect is the rectangle the mark's first and last points span.
func (m Mark) Rect() image.Rectangle {
	if len(m.Points) == 0 {
		return image.Rectangle{}
	}
	return image.Rectangle{Min: m.Points[0], Max: m.Points[len(m.Points)-1]}.Canon()
}

// Doc is an image under edit: the captured pixels, never changed, and the
// marks over them with an undo cursor.
type Doc struct {
	base    render.StillImage
	marks   []Mark
	applied int
}

// NewDoc starts editing base, which it never writes.
func NewDoc(base render.StillImage) *Doc { return &Doc{base: base} }

// Base is the captured image.
func (d *Doc) Base() render.StillImage { return d.base }

// Marks are the marks in effect, oldest first.
func (d *Doc) Marks() []Mark { return d.marks[:d.applied] }

// Push applies m on top, discarding what undo had taken back. A pixelation
// samples its cells now, a marker takes the next number, and a mark with
// nothing to show is dropped; it reports whether m was applied.
func (d *Doc) Push(m Mark) bool {
	m = d.Prepare(m)
	if !visible(m) {
		return false
	}
	d.marks = append(d.marks[:d.applied], m)
	d.applied++
	return true
}

// Prepare is m as Push would apply it: numbered and sampled.
func (d *Doc) Prepare(m Mark) Mark {
	m.Points = slices.Clone(m.Points)
	switch m.Tool {
	case ToolMarker:
		m.Number = d.nextNumber()
	case ToolPixelate:
		r := m.Rect().Intersect(d.base.Bounds())
		m.Points = []image.Point{r.Min, r.Max}
		m.cells = pixelCells(d.base.RGBA, r)
	}
	return m
}

func (d *Doc) nextNumber() int {
	n := 1
	for _, m := range d.Marks() {
		if m.Tool == ToolMarker {
			n++
		}
	}
	return n
}

// Undo takes the newest mark back, reporting whether there was one.
func (d *Doc) Undo() bool {
	if !d.CanUndo() {
		return false
	}
	d.applied--
	return true
}

// Redo reapplies the mark undo took back last, reporting whether there was one.
func (d *Doc) Redo() bool {
	if !d.CanRedo() {
		return false
	}
	d.applied++
	return true
}

// CanUndo reports whether a mark is in effect to take back.
func (d *Doc) CanUndo() bool { return d.applied > 0 }

// CanRedo reports whether undo took back a mark to reapply.
func (d *Doc) CanRedo() bool { return d.applied < len(d.marks) }

// Crop is the part of the image that is kept: the newest crop, else all of it.
func (d *Doc) Crop() image.Rectangle {
	b := d.base.Bounds()
	for _, m := range slices.Backward(d.Marks()) {
		if m.Tool == ToolCrop {
			return m.Rect().Intersect(b)
		}
	}
	return b
}

func visible(m Mark) bool {
	switch m.Tool {
	case ToolText:
		return m.Text != "" && len(m.Points) > 0
	case ToolMarker:
		return len(m.Points) > 0
	case ToolFreehand:
		return len(m.Points) > 1
	case ToolArrow:
		return len(m.Points) > 1 && m.Points[0] != m.Points[len(m.Points)-1]
	}
	r := m.Rect()
	return r.Dx() > 0 && r.Dy() > 0
}
