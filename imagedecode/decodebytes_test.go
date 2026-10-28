package imagedecode

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"runtime"
	"testing"
)

// allocated is how many bytes open allocates on the heap.
func allocated(t *testing.T, open func() error) int64 {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := open(); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	return int64(after.TotalAlloc - before.TotalAlloc)
}

// DecodeBytes covers what decoding the file allocates, up to the
// decoder's fixed buffers, without overstating it.
func TestDecodeBytesCoversTheDecode(t *testing.T) {
	const w, h = 1003, 701
	const slack = 128 << 10
	gradient := func(img interface{ Set(x, y int, c color.Color) }) {
		for y := range h {
			for x := range w {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
			}
		}
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	gradient(rgba)
	gray := image.NewGray(image.Rect(0, 0, w, h))
	gradient(gray)
	deep := image.NewNRGBA64(image.Rect(0, 0, w, h))
	gradient(deep)
	files := map[string]func(*bytes.Buffer) error{
		"color.jpg": func(b *bytes.Buffer) error { return jpeg.Encode(b, rgba, nil) },
		"gray.jpg":  func(b *bytes.Buffer) error { return jpeg.Encode(b, gray, nil) },
		"rgba.png":  func(b *bytes.Buffer) error { return png.Encode(b, rgba) },
		"deep.png":  func(b *bytes.Buffer) error { return png.Encode(b, deep) },
		"gray.png":  func(b *bytes.Buffer) error { return png.Encode(b, gray) },
	}
	for name, encode := range files {
		var buf bytes.Buffer
		if err := encode(&buf); err != nil {
			t.Fatal(err)
		}
		path := write(t, name, buf.Bytes())
		est, err := DecodeBytes(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := allocated(t, func() error { _, err := Open(path); return err })
		if est < got-slack || est > got {
			t.Errorf("%s: estimated %d bytes, the decode allocated %d", name, est, got)
		}
	}
	tga := append(tgaHeader(2, 32, 0, 300, 200), make([]byte, 300*200*4)...)
	path := write(t, "a.tga", tga)
	est, err := DecodeBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := allocated(t, func() error { _, err := Open(path); return err }); est < got-slack || est > got {
		t.Errorf("a.tga: estimated %d bytes, the decode allocated %d", est, got)
	}
}

func TestDecodeBytesRefusesWhatIsNoImage(t *testing.T) {
	for name, data := range map[string][]byte{
		"garbage.png":   []byte("definitely not an image"),
		"headless.jpg":  {0xff, 0xd8, 0xff, 0xd9},
		"truncated.jpg": {0xff, 0xd8, 0xff, 0xe0, 0x00},
	} {
		if n, err := DecodeBytes(write(t, name, data)); err == nil {
			t.Errorf("%s sized at %d bytes", name, n)
		}
	}
	if _, err := DecodeBytes(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Error("a missing file sized")
	}
}

// OpenContext stops reading once its context is done, and decodes as
// Open does while it is not.
func TestOpenContextGivesUpWhenCancelled(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	path := write(t, "a.png", buf.Bytes())
	if _, err := OpenContext(context.Background(), path); err != nil {
		t.Errorf("a live context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if img, err := OpenContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context = %v, %v, want context.Canceled", img, err)
	}
}
