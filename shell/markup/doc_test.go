package markup

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stubbedev/lys-lib/render"
)

var (
	red   = render.RGB(255, 0, 0)
	white = color.RGBA{255, 255, 255, 255}
)

func canvasImage(w, h int, c color.RGBA) render.StillImage {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return render.NewStillImage(img)
}

func pts(p ...int) []image.Point {
	out := make([]image.Point, 0, len(p)/2)
	for i := 0; i+1 < len(p); i += 2 {
		out = append(out, image.Pt(p[i], p[i+1]))
	}
	return out
}

func TestUndoRedoAndANewMarkDropsTheRedo(t *testing.T) {
	d := NewDoc(canvasImage(50, 50, white))
	a := Mark{Tool: ToolRect, Points: pts(1, 1, 10, 10), Color: red, Size: 2}
	b := Mark{Tool: ToolArrow, Points: pts(1, 1, 30, 30), Color: red, Size: 2}
	d.Push(a)
	d.Push(b)
	if !d.Undo() || len(d.Marks()) != 1 || d.Marks()[0].Tool != ToolRect {
		t.Fatalf("undo left %v", d.Marks())
	}
	if !d.Redo() || len(d.Marks()) != 2 {
		t.Fatalf("redo left %v", d.Marks())
	}
	if d.Redo() {
		t.Fatal("redo past the newest mark")
	}
	d.Undo()
	d.Push(Mark{Tool: ToolEllipse, Points: pts(2, 2, 20, 20), Color: red, Size: 2})
	if d.Redo() || len(d.Marks()) != 2 || d.Marks()[1].Tool != ToolEllipse {
		t.Fatalf("a new mark kept the undone one: %v", d.Marks())
	}
	for d.Undo() {
	}
	if len(d.Marks()) != 0 || d.Undo() {
		t.Fatal("undo past the first mark")
	}
}

func TestEmptyMarksAreNotApplied(t *testing.T) {
	d := NewDoc(canvasImage(50, 50, white))
	for _, m := range []Mark{
		{Tool: ToolRect, Points: pts(5, 5, 5, 20)},
		{Tool: ToolArrow, Points: pts(5, 5, 5, 5)},
		{Tool: ToolFreehand, Points: pts(5, 5)},
		{Tool: ToolText, Points: pts(5, 5)},
		{Tool: ToolPixelate, Points: pts(60, 60, 80, 80)},
	} {
		if d.Push(m) {
			t.Errorf("%v was applied", m)
		}
	}
	if !d.Push(Mark{Tool: ToolText, Points: pts(5, 5), Text: "hi"}) || !d.Push(Mark{Tool: ToolMarker, Points: pts(5, 5)}) {
		t.Fatal("a text and a marker were refused")
	}
}

func TestMarkersNumberInOrderAndUndoReusesTheNumber(t *testing.T) {
	d := NewDoc(canvasImage(50, 50, white))
	d.Push(Mark{Tool: ToolMarker, Points: pts(5, 5)})
	d.Push(Mark{Tool: ToolRect, Points: pts(1, 1, 9, 9)})
	d.Push(Mark{Tool: ToolMarker, Points: pts(20, 20)})
	if got := d.Marks()[2].Number; got != 2 {
		t.Fatalf("second marker numbered %d", got)
	}
	d.Undo()
	d.Push(Mark{Tool: ToolMarker, Points: pts(30, 30)})
	if got := d.Marks()[2].Number; got != 2 {
		t.Fatalf("a marker after undo numbered %d, want 2", got)
	}
}

func TestCropIsTheNewestCropWithinTheImage(t *testing.T) {
	d := NewDoc(canvasImage(50, 40, white))
	if d.Crop() != image.Rect(0, 0, 50, 40) {
		t.Fatalf("no crop = %v", d.Crop())
	}
	d.Push(Mark{Tool: ToolCrop, Points: pts(30, 30, 10, 5)})
	d.Push(Mark{Tool: ToolCrop, Points: pts(20, 10, 70, 70)})
	if d.Crop() != image.Rect(20, 10, 50, 40) {
		t.Fatalf("crop = %v", d.Crop())
	}
	d.Undo()
	if d.Crop() != image.Rect(10, 5, 30, 30) {
		t.Fatalf("crop after undo = %v", d.Crop())
	}
}

func at(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, y) }

func roundTrip(t *testing.T, img *image.RGBA) *image.RGBA {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := dec.(*image.RGBA)
	if !ok {
		out = image.NewRGBA(dec.Bounds())
		for y := range dec.Bounds().Dy() {
			for x := range dec.Bounds().Dx() {
				out.Set(x, y, dec.At(x, y))
			}
		}
	}
	return out
}

func TestFlattenedMarksSurviveSaveAndReload(t *testing.T) {
	font, err := render.NewFixtureTypeface()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDoc(canvasImage(120, 80, white))
	d.Push(Mark{Tool: ToolRect, Points: pts(10, 10, 50, 40), Color: red, Size: 4})
	d.Push(Mark{Tool: ToolHighlight, Points: pts(60, 10, 100, 30), Color: render.RGB(255, 255, 0), Size: 4})
	d.Push(Mark{Tool: ToolMarker, Points: pts(90, 60), Color: render.RGB(0, 0, 255), Size: 2})
	d.Push(Mark{Tool: ToolEllipse, Points: pts(10, 50, 50, 78), Color: render.RGB(0, 160, 0), Size: 4})
	d.Push(Mark{Tool: ToolText, Points: pts(55, 40), Text: "Hi", Color: red, Size: 2})

	img := roundTrip(t, Flatten(d, font))

	if got := at(img, 11, 25); got != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the rectangle's edge is %v, want red", got)
	}
	if got := at(img, 30, 25); got != white {
		t.Errorf("inside the rectangle is %v, want the image", got)
	}
	if got := at(img, 80, 20); got.R != 255 || got.G != 255 || got.B == 255 || got.B == 0 {
		t.Errorf("the highlight is %v, want a translucent yellow over white", got)
	}
	if got := at(img, 85, 60); got.B < 200 || got.R > 60 {
		t.Errorf("the marker disc is %v, want blue", got)
	}
	if got := at(img, 10, 64); got.G < 120 || got.R > 60 {
		t.Errorf("the ellipse's left edge is %v, want green", got)
	}
	if got := at(img, 30, 64); got != white {
		t.Errorf("the ellipse's middle is %v, want the image", got)
	}
	reds := 0
	for y := 40; y < 70; y++ {
		for x := 55; x < 85; x++ {
			if c := at(img, x, y); c.R > 200 && c.G < 80 && c.B < 80 {
				reds++
			}
		}
	}
	if reds == 0 {
		t.Error("the text left no ink")
	}
	if base := d.Base(); at(base.RGBA, 11, 25) != white {
		t.Error("flattening wrote into the captured image")
	}
}

func TestFlattenCropsToTheCrop(t *testing.T) {
	d := NewDoc(canvasImage(100, 100, white))
	d.Push(Mark{Tool: ToolRect, Points: pts(20, 20, 60, 60), Color: red, Size: 2})
	d.Push(Mark{Tool: ToolCrop, Points: pts(20, 20, 70, 50)})
	img := Flatten(d, nil)
	if img.Bounds() != image.Rect(0, 0, 50, 30) {
		t.Fatalf("cropped to %v", img.Bounds())
	}
	if got := at(img, 0, 10); got != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the rectangle's edge moved: %v", got)
	}
}

func TestPixelationLeavesNoneOfTheSecret(t *testing.T) {
	base := canvasImage(96, 48, white)
	secret := color.RGBA{255, 0, 0, 255}
	for y := 12; y < 36; y++ {
		for x := 12; x < 84; x += 3 {
			base.SetRGBA(x, y, secret)
		}
	}
	d := NewDoc(base)
	d.Push(Mark{Tool: ToolPixelate, Points: pts(0, 0, 96, 48)})

	img := roundTrip(t, Flatten(d, nil))

	distinct := map[color.RGBA]bool{}
	for y := range 48 {
		for x := range 96 {
			c := at(img, x, y)
			if c == secret {
				t.Fatalf("an original secret pixel survives at %d,%d", x, y)
			}
			distinct[c] = true
		}
	}
	if cells := (96 / cellPx) * (48 / cellPx); len(distinct) > cells {
		t.Fatalf("%d distinct colors in %d cells", len(distinct), cells)
	}
	if c := at(img, 30, 20); c == white || c.R != 255 || c.G == 0 {
		t.Fatalf("a cell over the secret is %v, want its average", c)
	}
}
