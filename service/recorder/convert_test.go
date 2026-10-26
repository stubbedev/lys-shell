package recorder

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
	img, at := paddedFrame(f, image.Rect(0, 0, 4, 2))
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
	img, at := paddedFrame(f, image.Rect(0, 0, 16, 16))
	if err := decodeFrame(img, at, f, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	rgbaNear(t, "blue", img.RGBAAt(at.X+8, at.Y+4), color.RGBA{0, 0, 255, 255})
	if got := img.RGBAAt(0, 0); got != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("padding = %v, want opaque black", got)
	}
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
