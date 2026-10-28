package screen

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

// photo is a w x h opaque test picture with detail at every scale:
// waves, a gradient, and per-pixel noise.
func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(7, 9))
	for y := range h {
		for x := range w {
			v := 128 + 90*math.Sin(float64(x)/7)*math.Cos(float64(y)/5)
			img.SetRGBA(x, y, color.RGBA{
				R: intconv.Clamp[uint8](int(v) + r.IntN(60) - 30),
				G: uint8(x * 255 / w),
				B: uint8((x ^ y) & 0xff),
				A: 255,
			})
		}
	}
	return img
}

// fullResolutionBlur is the lock's blur before #99, verbatim: the whole
// source on the Go heap, three box passes per axis at the source's own
// resolution.
func fullResolutionBlur(src image.Image, sigma float64) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	if sigma <= 0 || out.Rect.Empty() {
		return out
	}
	tmp := make([]uint8, len(out.Pix))
	for _, r := range boxRadii(sigma, 3) {
		fullResolutionBoxPass(out.Pix, tmp, out.Rect.Dx(), out.Rect.Dy(), out.Stride, r, true)
		fullResolutionBoxPass(tmp, out.Pix, out.Rect.Dx(), out.Rect.Dy(), out.Stride, r, false)
	}
	return out
}

func fullResolutionBoxPass(src, dst []uint8, w, h, stride, radius int, horizontal bool) {
	if radius <= 0 {
		copy(dst, src)
		return
	}
	lines, length := h, w
	if !horizontal {
		lines, length = w, h
	}
	at := func(line, i int) int {
		if horizontal {
			return line*stride + i*4
		}
		return i*stride + line*4
	}
	span := 2*radius + 1
	for line := range lines {
		for ch := range 4 {
			sum := 0
			for i := -radius; i <= radius; i++ {
				sum += int(src[at(line, min(max(i, 0), length-1))+ch])
			}
			for i := range length {
				dst[at(line, i)+ch] = intconv.Clamp[uint8](sum / span)
				out := min(max(i-radius, 0), length-1)
				in := min(max(i+radius+1, 0), length-1)
				sum += int(src[at(line, in)+ch]) - int(src[at(line, out)+ch])
			}
		}
	}
}

// difference is the mean and the largest absolute channel difference
// of two same-sized rasters.
func difference(t *testing.T, a, b *image.RGBA) (mean float64, largest int) {
	t.Helper()
	if a.Rect.Size() != b.Rect.Size() {
		t.Fatalf("sizes %v and %v differ", a.Rect, b.Rect)
	}
	total := 0
	for y := range a.Rect.Dy() {
		for x := range a.Rect.Dx() {
			pa := a.Pix[a.PixOffset(a.Rect.Min.X+x, a.Rect.Min.Y+y):]
			pb := b.Pix[b.PixOffset(b.Rect.Min.X+x, b.Rect.Min.Y+y):]
			for c := range 4 {
				d := int(pa[c]) - int(pb[c])
				d = max(d, -d)
				total += d
				largest = max(largest, d)
			}
		}
	}
	return float64(total) / float64(4*a.Rect.Dx()*a.Rect.Dy()), largest
}

func newBackdrop(t *testing.T, mem *memory.Shared, src image.Image, sigma float64, outputs ...image.Point) Backdrop {
	t.Helper()
	bg, err := NewBackdrop(mem, src, sigma, outputs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if mem.Stats().Live > 0 {
			_ = bg.Free()
		}
	})
	return bg
}

// Blurring the resampled image with sigma scaled by the same factor
// looks like blurring the source and scaling the result: the old
// full-resolution blur, downsampled with lys-lib's own resampler, on
// average within half a level and nowhere off by more than 10. The
// source is the 6000x4000 photo onto a 1080p and a 4K output, at a
// fifth of the size.
func TestBackdropMatchesTheFullResolutionBlur(t *testing.T) {
	src := photo(1200, 800)
	const sigma = 12
	reference := fullResolutionBlur(src, sigma)
	for _, out := range []image.Point{{384, 216}, {768, 432}} {
		var mem memory.Shared
		bg := newBackdrop(t, &mem, src, sigma, out)
		size := bg.Image.Bounds().Size()
		if want := image.Pt(out.X, out.X*2/3); size != want {
			t.Fatalf("onto %v: backdrop %v, want the source covering it at %v", out, size, want)
		}
		want := render.Resample(reference, reference.Rect, size.X, size.Y)
		mean, largest := difference(t, bg.Image.RGBA, want)
		if mean > 0.5 || largest > 10 {
			t.Errorf("onto %v: mean difference %.3f, largest %d from the full-resolution blur", out, mean, largest)
		}
	}
}

// Making the backdrop allocates on the Go heap only row-sized buffers,
// never a raster, and keeps one raster of the output's size off the
// heap: the source's size no longer matters.
func TestBackdropAllocationIsBoundedByTheOutput(t *testing.T) {
	src := photo(3000, 2000)
	out := image.Pt(480, 270)
	raster := 4 * 480 * 320
	var mem memory.Shared
	var bg Backdrop
	heap := treetest.HeapBytes(func() { bg = newBackdrop(t, &mem, src, 20, out) })
	if heap > uint64(raster/4) {
		t.Errorf("the Go heap took %d bytes, more than a quarter of the %d-byte output raster", heap, raster)
	}
	if st := mem.Stats(); st.Live != 1 || st.Bytes < raster || st.Bytes >= 2*raster {
		t.Errorf("off the heap: %d allocations of %d bytes, want the %d-byte output raster alone, its mapping rounded up",
			st.Live, st.Bytes, raster)
	}
	if got := bg.Image.Bounds().Size(); got != image.Pt(480, 320) {
		t.Errorf("backdrop %v, want 480x320", got)
	}
}

// The blur's scratch raster is freed before NewBackdrop returns, and
// Free frees the backdrop itself.
func TestBackdropFreesItsScratch(t *testing.T) {
	var mem memory.Shared
	bg := newBackdrop(t, &mem, photo(300, 200), 6, image.Pt(150, 100))
	if st := mem.Stats(); st.Live != 1 {
		t.Fatalf("%d allocations after the blur, want the backdrop alone", st.Live)
	}
	if err := bg.Free(); err != nil {
		t.Fatal(err)
	}
	if st := mem.Stats(); st.Live != 0 || st.Bytes != 0 {
		t.Errorf("after Free: %d allocations of %d bytes", st.Live, st.Bytes)
	}
}

// A source no larger than the output keeps its size and is blurred
// at the configured sigma: exactly the old blur, nothing enlarged
// before it. With no outputs known the source keeps its size too.
func TestBackdropOfASmallSource(t *testing.T) {
	src := photo(160, 90)
	want := fullResolutionBlur(src, 4)
	for name, outputs := range map[string][]image.Point{
		"larger output": {{1920, 1080}},
		"no outputs":    nil,
	} {
		var mem memory.Shared
		bg := newBackdrop(t, &mem, src, 4, outputs...)
		if mean, largest := difference(t, bg.Image.RGBA, want); largest != 0 {
			t.Errorf("%s: differs from the full-resolution blur (mean %.3f, largest %d)", name, mean, largest)
		}
	}
	var mem memory.Shared
	if bg := newBackdrop(t, &mem, src, 0, image.Pt(1920, 1080)); bg.Image.RGBAAt(5, 7) != src.RGBAAt(5, 7) {
		t.Error("sigma 0 changed the pixels")
	}
}

// An empty source is an error, with nothing left allocated.
func TestBackdropOfAnEmptySource(t *testing.T) {
	var mem memory.Shared
	if _, err := NewBackdrop(&mem, image.NewRGBA(image.Rect(0, 0, 0, 5)), 4, nil); err == nil {
		t.Error("an empty source made a backdrop")
	}
	if live := mem.Stats().Live; live != 0 {
		t.Errorf("%d allocations left", live)
	}
}

// The backdrop covers every output, and the largest one pixel for
// pixel: the cover crop for it is its own size, so it is drawn without
// another resample.
func TestBackdropCoversEveryOutput(t *testing.T) {
	src := image.Pt(6000, 4000)
	for _, outputs := range [][]image.Point{
		{{1920, 1080}},
		{{3840, 2160}},
		{{1920, 1080}, {1080, 1920}},
		{{2560, 1440}, {1280, 1024}, {3440, 1440}},
	} {
		size := scaledSize(src, coverScale(src, outputs))
		largest := false
		for _, o := range outputs {
			crop, dst := render.PlaceImage(size.X, size.Y, o.X, o.Y, render.ImageCover, render.ImageCentered)
			if dst.W != o.X || dst.H != o.Y {
				t.Errorf("%v: %v does not cover %v (%v)", outputs, size, o, dst)
			}
			if crop.Dx() == o.X && crop.Dy() == o.Y {
				largest = true
			}
			if crop.Dx() < o.X || crop.Dy() < o.Y {
				t.Errorf("%v: %v is enlarged onto %v (crop %v)", outputs, size, o, crop)
			}
		}
		if !largest {
			t.Errorf("%v: %v covers no output one to one", outputs, size)
		}
	}
}

// A source pixel spreads over the destination pixels it covers, and
// every destination pixel takes the same total weight.
func TestAreaSpans(t *testing.T) {
	spans := areaSpans(5, 2)
	got := make([]float32, 2)
	for _, s := range spans {
		got[s.to] += s.first
		if s.second > 0 {
			got[s.to+1] += s.second
		}
	}
	if got[0] != 2.5 || got[1] != 2.5 || spans[2].first != 0.5 || spans[2].to != 0 {
		t.Errorf("spans %+v weigh %v, want 2.5 each with the middle pixel split", spans, got)
	}
	for i, s := range areaSpans(4, 4) {
		if s.to != i || s.first != 1 || s.second != 0 {
			t.Errorf("identity span %d = %+v", i, s)
		}
	}
}
