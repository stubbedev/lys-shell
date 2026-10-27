package greeter

import (
	"image"
	"math"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/auth"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// jpegPhoto is a w x h picture as the JPEG decoder returns one, YCbCr
// 4:2:0 rather than RGBA, with smooth detail that a downscale of any
// filter shows alike.
func jpegPhoto(w, h int) *image.YCbCr {
	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for y := range h {
		for x := range w {
			img.Y[img.YOffset(x, y)] = uint8(128 + 100*math.Sin(float64(x)/90)*math.Cos(float64(y)/70))
		}
	}
	for y := range (h + 1) / 2 {
		for x := range (w + 1) / 2 {
			o := img.COffset(2*x, 2*y)
			img.Cb[o] = uint8(64 + 128*x/((w+1)/2))
			img.Cr[o] = uint8(64 + 128*y/((h+1)/2))
		}
	}
	return img
}

// imageRig is a greeter showing src as its background on outputs.
func imageRig(t *testing.T, src image.Image, outputs ...image.Point) *rig {
	t.Helper()
	return buildRig(t, func(i *Init) {
		i.Config.Greeter.Background.Mode = config.BackgroundImage
		i.Config.Greeter.Background.Image = "/srv/greeter.jpg"
	}, &rig{background: src, outputs: outputs})
}

// paintInto lays w out over size and paints it into data, which holds
// a size raster.
func paintInto(w widget.Widget, size image.Point, data []byte) {
	w.Measure(widget.Constraints{Max: widget.Size{W: size.X, H: size.Y}})
	w.Arrange(render.Rect{W: size.X, H: size.Y})
	clear(data)
	w.Paint(render.NewScaled(data, render.Stride(size.X), size.X, size.Y, 1, 1))
}

// Showing a JPEG-sized background, from building the greeter through
// its first paint, allocates on the Go heap no more than showing one
// already at the output's size does, give or take a quarter of one
// RGBA copy of the source, and keeps one raster of the output's size
// off the heap: the source's size no longer matters.
func TestGreeterBackgroundAllocationIsBoundedByTheOutput(t *testing.T) {
	out := image.Pt(480, 270)
	raster := 4 * 480 * 320
	data := make([]byte, render.Stride(out.X)*out.Y)
	greet := func(src image.Image) (*rig, uint64) {
		var r *rig
		heap := treetest.HeapBytes(func() {
			r = imageRig(t, src, out)
			paintInto(r.s.root, out, data)
		})
		return r, heap
	}
	greet(jpegPhoto(480, 320))
	_, sized := greet(jpegPhoto(480, 320))
	r, heap := greet(jpegPhoto(3000, 2000))
	if copied := uint64(4 * 3000 * 2000); heap > sized+copied/4 {
		t.Errorf("the Go heap took %d bytes, %d for a source at the output's size: more than a quarter of the %d-byte RGBA copy of the source more",
			heap, sized, copied)
	}
	if r.s.bg == nil {
		t.Fatal("image mode made no background")
	}
	if got := r.s.bg.Image.Bounds().Size(); got != image.Pt(480, 320) {
		t.Errorf("background %v, want the 3000x2000 source covering 480x270: 480x320", got)
	}
	if st := r.s.mem.Stats(); st.Live != 1 || st.Bytes < raster || st.Bytes >= 2*raster {
		t.Errorf("off the heap: %d allocations of %d bytes, want the %d-byte output raster alone, its mapping rounded up",
			st.Live, st.Bytes, raster)
	}
}

// The background looks as it did when the greeter drew the decoded
// source scaled to cover: on average within a level, nowhere off by
// more than a few. With no output known, nothing is resampled.
func TestGreeterBackgroundMatchesTheSourceDrawnToCover(t *testing.T) {
	src := jpegPhoto(1500, 1000)
	out := image.Pt(384, 216)
	old := widget.NewImage(src)
	old.SetScale(widget.ImageCover)
	want := make([]byte, render.Stride(out.X)*out.Y)
	paintInto(old, out, want)

	r := imageRig(t, src, out)
	pic := treetest.First[*widget.Image](t, r.s.root)
	got := make([]byte, len(want))
	paintInto(pic, out, got)
	total, largest := 0, 0
	for i := range got {
		d := int(got[i]) - int(want[i])
		d = max(d, -d)
		total += d
		largest = max(largest, d)
	}
	if mean := float64(total) / float64(len(got)); mean > 1 || largest > 6 {
		t.Errorf("mean difference %.3f, largest %d from the source drawn to cover", mean, largest)
	}

	r = imageRig(t, src)
	if got := r.s.bg.Image.Bounds().Size(); got != image.Pt(1500, 1000) {
		t.Errorf("with no outputs the background is %v, want the source's 1500x1000", got)
	}
}

// When the greeter ends the background's texture is forgotten once, by
// its identity, and its off-heap pixels are freed; until then the
// greeter keeps showing it.
func TestGreeterFreesTheBackgroundWhenItEnds(t *testing.T) {
	r := imageRig(t, jpegPhoto(600, 400), image.Pt(192, 108))
	id := r.s.bg.Image.ID
	if id == 0 {
		t.Fatal("the off-heap background has no identity to drop its texture by")
	}
	r.s.prompt.Username.SetText("ada")
	r.s.submit("wrong")
	r.conv(t).emit(prompt(auth.PromptSecret, "Password:"))
	if live := r.s.mem.Stats().Live; live != 1 || len(r.forgotten) != 0 {
		t.Fatalf("while greeting: %d allocations, forgot %v; the greeter still shows the background", live, r.forgotten)
	}
	r.s.end()
	if st := r.s.mem.Stats(); st.Live != 0 || st.Bytes != 0 {
		t.Errorf("after the greeter ended: %d allocations of %d bytes", st.Live, st.Bytes)
	}
	if !slices.Equal(r.forgotten, []render.ImageID{id}) {
		t.Errorf("forgot %v, want the background %v once", r.forgotten, id)
	}
	if r.s.bg != nil {
		t.Error("the background outlived the greeter")
	}
	r.s.end()
	if len(r.forgotten) != 1 {
		t.Errorf("ending twice forgot %v", r.forgotten)
	}
}

// The pixels are freed only once every List recorded while the greeter
// was up has ended: a frame still being played keeps them.
func TestGreeterFreesTheBackgroundAfterTheFence(t *testing.T) {
	r := imageRig(t, jpegPhoto(600, 400), image.Pt(192, 108))
	drawing := render.NewRecorder(1, 1, 1, 1).Recording()
	r.s.end()
	if live := r.s.mem.Stats().Live; live != 1 {
		t.Fatalf("%d allocations while a List from before the end is open, want the background kept", live)
	}
	drawing.End()
	if live := r.s.mem.Stats().Live; live != 0 {
		t.Fatalf("%d allocations after the last List ended", live)
	}
}

// The color mode and an unreadable image draw the color: nothing is
// allocated off the heap and nothing is forgotten when the greeter
// ends.
func TestGreeterBackgroundColorFallback(t *testing.T) {
	for name, r := range map[string]*rig{
		"color mode":       newRig(t, nil),
		"unreadable image": imageRig(t, nil, image.Pt(192, 108)),
	} {
		if r.s.bg != nil || r.s.mem.Stats().Live != 0 {
			t.Errorf("%s: made a background", name)
		}
		if len(treetest.WithClass(r.s.root, "lock-bg")) != 1 {
			t.Errorf("%s: no .lock-bg color fill", name)
		}
		r.s.end()
		if len(r.forgotten) != 0 {
			t.Errorf("%s: forgot %v", name, r.forgotten)
		}
	}
}
