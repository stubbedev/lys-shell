package offheap

import (
	"image"
	"math"
	"testing"

	"github.com/stubbedev/memory-go"
)

func TestNewRGBAAndFree(t *testing.T) {
	var mem memory.Shared
	img, pix, err := NewRGBA(&mem, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect != image.Rect(0, 0, 7, 3) || img.Stride != 28 || len(img.Pix) != 84 || pix.Len() != 84 {
		t.Fatalf("raster %v stride %d, %d pixels bytes, slice %d", img.Rect, img.Stride, len(img.Pix), pix.Len())
	}
	for _, v := range img.Pix {
		if v != 0 {
			t.Fatal("a fresh raster is not zeroed")
		}
	}
	img.Pix[83] = 9
	if st := mem.Stats(); st.Live != 1 {
		t.Errorf("%d allocations, want the raster", st.Live)
	}
	if err := Free(&mem, pix); err != nil {
		t.Fatal(err)
	}
	if st := mem.Stats(); st.Live != 0 || st.Bytes != 0 {
		t.Errorf("after Free: %d allocations of %d bytes", st.Live, st.Bytes)
	}
}

func TestNewRGBARejectsImpossibleSizes(t *testing.T) {
	var mem memory.Shared
	for _, size := range []image.Point{{0, 5}, {5, 0}, {-1, 5}, {math.MaxInt / 2, 4}} {
		if _, _, err := NewRGBA(&mem, size.X, size.Y); err == nil {
			t.Errorf("a %v raster was allocated", size)
		}
	}
	if live := mem.Stats().Live; live != 0 {
		t.Errorf("%d allocations left by rejected sizes", live)
	}
}
