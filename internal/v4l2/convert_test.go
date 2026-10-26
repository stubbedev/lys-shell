package v4l2

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

func near(a, b uint8) bool { return a-b < 3 || b-a < 3 }

func rgbaNear(t *testing.T, what string, got color.RGBA, want color.RGBA) {
	t.Helper()
	if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) || got.A != want.A {
		t.Errorf("%s = %v, want about %v", what, got, want)
	}
}

// YUYV converts with BT.601 limited range into the padded picture at
// its offset; the padding stays opaque black.
func TestYUYVToRGBA(t *testing.T) {
	f := cameraFrame{width: 4, height: 1, format: pixYUYV, stride: 10}
	// White and black share a pair; red is the next pair.
	data := []byte{235, 128, 16, 128, 81, 90, 81, 240, 0xee, 0xee}
	img, at := paddedFrame(f, image.Pt(4, 2))
	if at != image.Pt(0, 0) || img.Bounds().Dx() != 4 || img.Bounds().Dy() != 2 {
		t.Fatalf("padded %v at %v, want 4x2 at the origin", img.Bounds(), at)
	}
	if err := decodeFrame(img, at, f, data); err != nil {
		t.Fatal(err)
	}
	rgbaNear(t, "white", img.RGBAAt(0, 0), color.RGBA{255, 255, 255, 255})
	rgbaNear(t, "black", img.RGBAAt(1, 0), color.RGBA{0, 0, 0, 255})
	rgbaNear(t, "red", img.RGBAAt(2, 0), color.RGBA{255, 0, 0, 255})
	if got := img.RGBAAt(0, 1); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("padding = %v, want opaque black", got)
	}
}

// A short or odd-width YUYV frame is an error naming its layout, never
// a read past the buffer.
func TestYUYVRejectsAShortFrame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for _, f := range []cameraFrame{
		{width: 4, height: 2, format: pixYUYV, stride: 8},
		{width: 3, height: 1, format: pixYUYV, stride: 6},
		{width: 4, height: 1, format: pixYUYV, stride: 4},
	} {
		if err := decodeFrame(img, image.Point{}, f, make([]byte, 8)); err == nil || !strings.Contains(err.Error(), "YUYV camera frame") {
			t.Errorf("%+v: %v, want an error naming the frame", f, err)
		}
	}
}

// MJPEG frames decode into the padded picture; one of the wrong size or
// not a JPEG is an error.
func TestMJPEGToRGBA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for i := range src.Pix {
		src.Pix[i] = []byte{0, 0, 255, 255}[i%4]
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	f := cameraFrame{width: 16, height: 8, format: pixMJPG}
	img, at := paddedFrame(f, image.Pt(16, 16))
	if err := decodeFrame(img, at, f, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	rgbaNear(t, "blue", img.RGBAAt(at.X+8, at.Y+4), color.RGBA{0, 0, 255, 255})
	if got := img.RGBAAt(0, 0); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("padding = %v, want opaque black", got)
	}
	// UVC cameras leave the standard Huffman tables out of their frames.
	dht, ok := dhtSegment(buf.Bytes())
	if !ok {
		t.Fatal("the encoded JPEG has no DHT segment")
	}
	bare := bytes.Replace(buf.Bytes(), dht, nil, 1)
	if _, ok := dhtSegment(bare); ok {
		t.Fatal("the DHT segment was not cut")
	}
	clear(img.Pix)
	if err := decodeFrame(img, at, f, bare); err != nil {
		t.Fatalf("a frame without Huffman tables: %v", err)
	}
	rgbaNear(t, "blue without tables", img.RGBAAt(at.X+8, at.Y+4), color.RGBA{0, 0, 255, 255})
	if err := decodeFrame(img, at, cameraFrame{width: 8, height: 8, format: pixMJPG}, buf.Bytes()); err == nil {
		t.Error("a 16x8 JPEG decoded as an 8x8 stream")
	}
	if err := decodeFrame(img, at, f, []byte("not a jpeg")); err == nil {
		t.Error("garbage decoded")
	}
	if err := decodeFrame(img, at, cameraFrame{width: 16, height: 8, format: 'N' | 'V'<<8 | '1'<<16 | '2'<<24}, buf.Bytes()); err == nil || !strings.Contains(err.Error(), "NV12") {
		t.Errorf("NV12: %v, want an error naming it", err)
	}
}

// A camera frame is padded to the requested aspect, centred, never
// stretched: a 4:3 camera gains side bars in a 16:9 frame, a wider one
// bars above and below, and a matching one none.
func TestLetterbox(t *testing.T) {
	aspect := image.Pt(384, 216)
	for name, c := range map[string]struct {
		w, h     int
		size, at image.Point
	}{
		"4:3":   {640, 480, image.Pt(854, 480), image.Pt(107, 0)},
		"16:9":  {1280, 720, image.Pt(1280, 720), image.Pt(0, 0)},
		"21:9":  {2560, 1080, image.Pt(2560, 1440), image.Pt(0, 180)},
		"tall":  {480, 640, image.Pt(1138, 640), image.Pt(329, 0)},
		"exact": {384, 216, image.Pt(384, 216), image.Pt(0, 0)},
	} {
		size, at := letterbox(c.w, c.h, aspect)
		if size != c.size || at != c.at {
			t.Errorf("%s: size %v at %v, want %v at %v", name, size, at, c.size, c.at)
		}
		if at.X+c.w > size.X || at.Y+c.h > size.Y {
			t.Errorf("%s: the %dx%d frame at %v overflows %v", name, c.w, c.h, at, size)
		}
	}
}
