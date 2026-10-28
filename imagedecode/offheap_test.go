package imagedecode

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stubbedev/memory-go"
)

func photoJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func wantNothingHeld(t *testing.T, mem *memory.Shared) {
	t.Helper()
	if st := mem.Stats(); st.Live != 0 {
		t.Errorf("%d allocations of %d bytes still held", st.Live, st.Bytes)
	}
}

func TestOpenOffHeapDecodesAJPEGOffTheHeap(t *testing.T) {
	const w, h = 1024, 768
	path := write(t, "photo.jpg", photoJPEG(t, w, h))
	want, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	onHeap := allocated(t, func() error { _, err := Open(path); return err })
	if onHeap < w*h*3/2 {
		t.Fatalf("Open allocated %d bytes, less than the %dx%d image: the measurement is off", onHeap, w, h)
	}
	var mem memory.Shared
	var got Decoded
	offHeap := allocated(t, func() error {
		var err error
		got, err = OpenOffHeap(&mem, path)
		return err
	})
	if offHeap > 64<<10 {
		t.Errorf("OpenOffHeap allocated %d bytes on the heap, want at most 64 KiB (Open: %d)", offHeap, onHeap)
	}
	if !reflect.DeepEqual(got.Image, want) {
		t.Error("OpenOffHeap decodes other pixels than Open")
	}
	if st := mem.Stats(); st.Live != 1 {
		t.Errorf("%d allocations held, want the image's", st.Live)
	}
	if err := got.Free(); err != nil {
		t.Fatal(err)
	}
	wantNothingHeld(t, &mem)
}

func TestOpenOffHeapDecodesOtherFormatsAsOpenDoes(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	path := write(t, "a.png", buf.Bytes())
	var mem memory.Shared
	got, err := OpenOffHeap(&mem, path)
	if err != nil {
		t.Fatal(err)
	}
	if rgba(got.Image, 1, 0) != (color.NRGBA{B: 255, A: 255}) {
		t.Errorf("pixel 1 = %v", rgba(got.Image, 1, 0))
	}
	wantNothingHeld(t, &mem)
	if err := got.Free(); err != nil {
		t.Errorf("freeing a heap image: %v", err)
	}
}

func TestOpenOffHeapRefusesWhatIsNoImageAndHoldsNothing(t *testing.T) {
	data := photoJPEG(t, 64, 64)
	dir := t.TempDir()
	for name, path := range map[string]string{
		"truncated JPEG": write(t, "cut.jpg", data[:len(data)/2]),
		"garbage":        write(t, "garbage", []byte("not an image at all")),
		"missing":        filepath.Join(dir, "missing.jpg"),
	} {
		var mem memory.Shared
		got, err := OpenOffHeap(&mem, path)
		if err == nil {
			t.Errorf("%s: decoded a %v image", name, got.Image.Bounds())
		}
		if got.Image != nil {
			t.Errorf("%s: an image came back with the error", name)
		}
		wantNothingHeld(t, &mem)
	}
}
