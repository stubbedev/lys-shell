package widgets

import (
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// layout lays child out alone at the start of a wide row and returns
// its bounds.
func layout(child widget.Widget) render.Rect {
	row := widget.NewBox(widget.Row, 0, 0)
	row.AppendAligned(child, false, widget.AlignStart)
	widget.SizeWithin(row, widget.Size{W: 500, H: 500})
	row.Arrange(render.Rect{W: 500, H: 500})
	return child.(interface{ Bounds() render.Rect }).Bounds()
}

// A pinned axis is exact whether the content measures smaller or
// larger; a zero axis keeps the natural extent.
func TestPinHoldsAnExactSize(t *testing.T) {
	if got := layout(Pin(widget.NewDrawingArea(10, 10), 90, 0)); got.W != 90 || got.H != 10 {
		t.Errorf("small content pinned at %+v, want 90 x natural 10", got)
	}
	if got := layout(Pin(widget.NewDrawingArea(300, 200), 40, 30)); got.W != 40 || got.H != 30 {
		t.Errorf("large content pinned at %+v, want 40 x 30", got)
	}
}

// Without a pin the same content keeps its natural size.
func TestUnpinnedKeepsItsNaturalSize(t *testing.T) {
	if got := layout(widget.NewDrawingArea(300, 200)); got.W != 300 || got.H != 200 {
		t.Errorf("unpinned content at %+v, want its natural 300 x 200", got)
	}
}

// Pinning again replaces the old size.
func TestPinAgainRepins(t *testing.T) {
	w := Pin(widget.NewDrawingArea(10, 10), 90, 0)
	layout(w)
	if got := layout(Pin(w, 120, 30)); got.W != 120 || got.H != 30 {
		t.Errorf("re-pinned at %+v, want 120 x 30", got)
	}
}
