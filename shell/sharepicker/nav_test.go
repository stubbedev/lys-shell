package sharepicker

import (
	"testing"

	"github.com/stubbedev/lys-lib/render"
)

func TestNearest(t *testing.T) {
	grid := []render.Rect{
		{X: 0, Y: 0, W: 100, H: 80},
		{X: 110, Y: 0, W: 100, H: 80},
		{X: 220, Y: 0, W: 100, H: 80},
		{X: 330, Y: 0, W: 100, H: 80},
		{X: 0, Y: 90, W: 100, H: 80},
	}
	outputs := []render.Rect{{X: 0, Y: 0, W: 200, H: 110}, {X: 210, Y: 30, W: 150, H: 80}}
	for _, tc := range []struct {
		name  string
		rects []render.Rect
		from  int
		dir   direction
		want  int
	}{
		{"Right reaches the next card of the row", grid, 0, direction{dx: 1}, 1},
		{"Down reaches the card below", grid, 0, direction{dy: 1}, 4},
		{"Up reaches the card above", grid, 4, direction{dy: -1}, 0},
		{"Right reaches an output of another height", outputs, 0, direction{dx: 1}, 1},
		{"Left reaches back", outputs, 1, direction{dx: -1}, 0},
		{"Right from the short last row's card has none", grid, 4, direction{dx: 1}, -1},
		{"Down from a column without a card below has none", grid, 2, direction{dy: 1}, -1},
		{"Left from the first card has none", grid, 0, direction{dx: -1}, -1},
		{"Up from the first row has none", grid, 3, direction{dy: -1}, -1},
	} {
		if got := nearest(tc.rects, tc.rects[tc.from], tc.dir); got != tc.want {
			t.Errorf("%s: nearest = %d, want %d", tc.name, got, tc.want)
		}
	}
}
