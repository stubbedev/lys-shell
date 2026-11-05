package regionoverlay

import (
	"image"
	"testing"

	"github.com/stubbedev/lys-lib/render"
)

var stacked = []Window{
	{ID: "front", Output: "DP-1", X: 10, Y: 10, Width: 10, Height: 8},
	{ID: "other", Output: "DP-2", X: 0, Y: 0, Width: 40, Height: 30},
	{ID: "back", Output: "DP-1", X: 4, Y: 4, Width: 30, Height: 20},
}

func TestWindowAtIsTheFrontWindowItsOutputShows(t *testing.T) {
	for _, c := range []struct {
		output string
		at     image.Point
		want   string
	}{
		{"DP-1", image.Pt(12, 12), "front"},
		{"DP-1", image.Pt(5, 5), "back"},
		{"DP-2", image.Pt(12, 12), "other"},
		{"DP-1", image.Pt(35, 25), ""},
		{"HDMI-A-1", image.Pt(12, 12), ""},
	} {
		w, ok := WindowAt(stacked, c.output, c.at)
		if w.ID != c.want || ok != (c.want != "") {
			t.Errorf("%s at %v: %q %v, want %q", c.output, c.at, w.ID, ok, c.want)
		}
	}
}

// pickArea is a 40x30 DP-1 area of a window pick over stacked, the
// pointer at p.
func pickArea(p image.Point) []byte {
	o := &WindowPick{windows: stacked, pointer: p, onSurface: true}
	a := &windowArea{o: o, mon: Monitor{Connector: "DP-1", Width: 40, Height: 30}}
	o.areas = []*windowArea{a}
	a.Arrange(render.Rect{W: 40, H: 30})
	data := make([]byte, render.Stride(40)*30)
	a.Paint(render.New(data, render.Stride(40), 40, 30))
	return data
}

func TestTheWindowUnderThePointerShowsThroughTheWash(t *testing.T) {
	wash := render.Color(dimAlpha << 24)
	back := pickArea(image.Pt(6, 6))
	if got := px(back, 6, 22); got != 0 {
		t.Errorf("the hovered back window is %#x, want a hole in the wash", uint32(got))
	}
	if got := px(back, 14, 14); got != wash {
		t.Errorf("the front window over the hovered one is %#x, want the wash", uint32(got))
	}
	if got := px(back, 37, 27); got != wash {
		t.Errorf("beside the windows %#x, want the wash", uint32(got))
	}
	front := pickArea(image.Pt(14, 14))
	if got := px(front, 14, 14); got != 0 {
		t.Errorf("the hovered front window is %#x, want a hole", uint32(got))
	}
	if got := px(front, 6, 22); got != wash {
		t.Errorf("the window behind is %#x, want the wash", uint32(got))
	}
	if got := px(pickArea(image.Pt(37, 27)), 14, 14); got != wash {
		t.Errorf("with no window under the pointer the front one is %#x, want the wash", uint32(got))
	}
}
