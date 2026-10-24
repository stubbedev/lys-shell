package loupe

import (
	"image"
	"image/color"
	"testing"

	"github.com/stubbedev/lys-ui/render"
)

func gradient() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for y := range 80 {
		for x := range 120 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 7, 255})
		}
	}
	return img
}

func TestSampleAt(t *testing.T) {
	img := gradient()
	if x, y := SampleAt(img, 60, 40, 10.6, 5.2); x != 21 || y != 10 {
		t.Errorf("scaled sample = %d,%d", x, y)
	}
	if x, y := SampleAt(img, 60, 40, -5, 900); x != 0 || y != 79 {
		t.Errorf("clamped sample = %d,%d", x, y)
	}
}

// A card sits below and right of the cursor, and flips to the other
// side on an axis where it would leave the box.
func TestPlace(t *testing.T) {
	box := render.Rect{X: 100, Y: 50, W: 400, H: 300}
	if x, y := Place(box, 10, 10, 80, 60); x != 100+34 || y != 50+34 {
		t.Errorf("placed at %d,%d", x, y)
	}
	if x, y := Place(box, 390, 290, 80, 60); x != 100+390-24-80 || y != 50+290-24-60 {
		t.Errorf("flipped to %d,%d", x, y)
	}
}

// The grid shows the frame's pixels around the centre, one Zoom square
// each, with the centre pixel boxed.
func TestDraw(t *testing.T) {
	const w, h = Size + 10, Size + 10
	data := make([]byte, render.Stride(w)*h)
	cv := render.New(data, render.Stride(w), w, h)
	img := gradient()
	Draw(cv, img, 40, 30, 0, 0)
	at := func(x, y int) render.Color { return render.ColorFromBytes(data[y*render.Stride(w)+x*4:]) }
	corner := img.RGBAAt(40-Radius, 30-Radius)
	if got := at(Zoom/2, Zoom/2); got != render.RGB(corner.R, corner.G, corner.B) {
		t.Errorf("top-left cell = %#x, want %v", uint32(got), corner)
	}
	above := img.RGBAAt(40, 30-1)
	if got := at(Radius*Zoom+Zoom/2, Radius*Zoom-1); got == render.RGB(above.R, above.G, above.B) {
		t.Errorf("no box around the centre pixel: %#x", uint32(got))
	}
}
