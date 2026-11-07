package material

import (
	"image"
	"runtime"
	"testing"
)

func pattern(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		by := y * 36 / h
		for x := range w {
			bx := x * 64 / w
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(bx*4), uint8(by*7), uint8(255-bx*3), 0xff
		}
	}
	return img
}

func extractAllocs(img *image.RGBA) (allocs float64, bytes uint64) {
	run := func() { NewScheme(Score(Quantize(Sample(img, 128), 128))[0], TonalSpot, true, 0) }
	run()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return testing.AllocsPerRun(3, run), after.TotalAlloc - before.TotalAlloc
}

func TestExtractionAllocationsDoNotGrowWithTheWallpaper(t *testing.T) {
	small, big := pattern(1920, 1080), pattern(7680, 4320)
	if s, b := Sample(small, 128), Sample(big, 128); len(s) != 128*72 || len(b) != len(s) {
		t.Fatalf("samples: %d from 1080p, %d from 8K; want 128x72 from both", len(s), len(b))
	}
	smallAllocs, smallBytes := extractAllocs(small)
	bigAllocs, bigBytes := extractAllocs(big)
	if smallAllocs != bigAllocs {
		t.Errorf("8K extraction allocates %v times, 1080p %v", bigAllocs, smallAllocs)
	}
	if bigBytes > smallBytes+smallBytes/50 {
		t.Errorf("8K extraction allocates %d bytes, 1080p %d", bigBytes, smallBytes)
	}
	const sampleBytes = 128 * 72 * 4
	if smallBytes > 64*sampleBytes+4<<20 {
		t.Errorf("extraction allocates %d bytes for a %d-byte sample", smallBytes, sampleBytes)
	}
}

func TestSampleSkipsTranslucentPixelsAndKeepsTheAspect(t *testing.T) {
	img := pattern(300, 100)
	for y := range 100 {
		for x := range 150 {
			img.Pix[img.PixOffset(x, y)+3] = 0x80
		}
	}
	if got := Sample(img, 30); len(got) != 15*10 {
		t.Errorf("%d samples from a half-translucent 30x10 grid, want the opaque 15x10", len(got))
	}
	if n := len(Sample(pattern(100, 300), 30)); n != 10*30 {
		t.Errorf("a portrait image gives %d samples, want 10x30", n)
	}
	if Sample(image.NewRGBA(image.Rect(0, 0, 0, 0)), 30) != nil {
		t.Error("an empty image gives samples")
	}
}
