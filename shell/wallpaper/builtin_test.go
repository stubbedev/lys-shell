package wallpaper

import (
	"bytes"
	"image"
	"testing"

	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/offheap"
)

func paintedBuiltin(t testing.TB, w, h int) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	paintBuiltin(img)
	return img
}

func TestBuiltinWallpaperIsOpaqueAndDeterministic(t *testing.T) {
	a, b := paintedBuiltin(t, 64, 36), paintedBuiltin(t, 64, 36)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Error("two paints of one size differ")
	}
	for i := 3; i < len(a.Pix); i += 4 {
		if a.Pix[i] != 0xff {
			t.Fatalf("alpha %d at byte %d", a.Pix[i], i)
		}
	}
}

func TestBuiltinWallpaperIsNotFlat(t *testing.T) {
	img := paintedBuiltin(t, 192, 108)
	at := func(x, y int) [3]uint8 {
		c := img.RGBAAt(x, y)
		return [3]uint8{c.R, c.G, c.B}
	}
	dark, warm := at(5, 5), at(180, 100)
	if int(warm[0]) < int(dark[0])+40 {
		t.Errorf("no glow toward the warm corner: %v against %v", warm, dark)
	}
}

func TestBuiltinWallpaperDithersItsSlopes(t *testing.T) {
	img := paintedBuiltin(t, 512, 64)
	row := img.Pix[32*img.Stride : 32*img.Stride+4*512]
	changes, jumps := 0, 0
	for x := 4; x < len(row); x += 4 {
		d := int(row[x]) - int(row[x-4])
		if d != 0 {
			changes++
		}
		if d > 3 || d < -3 {
			jumps++
		}
	}
	if changes < 100 {
		t.Errorf("only %d of 511 neighbours differ: the row bands", changes)
	}
	if jumps != 0 {
		t.Errorf("%d neighbours differ by more than the dither", jumps)
	}
}

func TestBuiltinRasterFitsTheMonitorItIsMadeFor(t *testing.T) {
	var mem memory.Shared
	r, err := builtinRaster(&mem, target{w: 320, h: 200, scale: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.size() != image.Pt(320, 200) {
		t.Errorf("raster %v for a 320x200 monitor", r.size())
	}
	if err := offheap.Free(&mem, r.pix); err != nil {
		t.Fatal(err)
	}
	if r, err = builtinRaster(&mem, target{}, false); err != nil {
		t.Fatal(err)
	}
	if r.size() != builtinFallback {
		t.Errorf("raster %v with no monitor size, want %v", r.size(), builtinFallback)
	}
	if err := offheap.Free(&mem, r.pix); err != nil {
		t.Fatal(err)
	}
	if live := mem.Stats().Live; live != 0 {
		t.Errorf("%d allocations outlive the rasters", live)
	}
}

func BenchmarkBuiltinWallpaper1080p(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	b.ReportAllocs()
	for b.Loop() {
		paintBuiltin(img)
	}
}
