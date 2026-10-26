package recorder

import (
	"image"
	"testing"
)

func TestWebcamMargin(t *testing.T) {
	if m := WebcamMargin(1920, 1080); m != 54 {
		t.Errorf("margin = %d, want 5%% of 1080", m)
	}
}

// The frame is Size percent of the width at 16:9, placed at X and Y
// percent of the travel inside the margin; out-of-range positions
// clamp, a tiny size floors at 80x60, and a frame wider than the screen
// has no travel.
func TestWebcamRect(t *testing.T) {
	for name, c := range map[string]struct {
		cam  Webcam
		w, h int32
		want image.Rectangle
	}{
		"bottom right": {Webcam{X: 100, Y: 100, Size: 20}, 1920, 1080, image.Rect(1482, 810, 1482+384, 810+216)},
		"top left":     {Webcam{X: 0, Y: 0, Size: 20}, 1920, 1080, image.Rect(54, 54, 54+384, 54+216)},
		"clamped":      {Webcam{X: 250, Y: 50, Size: 20}, 1920, 1080, image.Rect(1482, 432, 1482+384, 432+216)},
		"tiny":         {Webcam{Size: 0}, 1920, 1080, image.Rect(54, 54, 54+80, 54+60)},
		"no travel":    {Webcam{X: 100, Size: 100}, 100, 100, image.Rect(5, 5, 105, 65)},
	} {
		if got := webcamRect(c.cam, c.w, c.h); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// A camera frame is padded to the overlay's aspect, centred, never
// stretched: a 4:3 camera gains side bars in a 16:9 frame, a wider one
// bars above and below, and a matching one none.
func TestLetterbox(t *testing.T) {
	dst := image.Rect(0, 0, 384, 216)
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
		size, at := letterbox(c.w, c.h, dst)
		if size != c.size || at != c.at {
			t.Errorf("%s: size %v at %v, want %v at %v", name, size, at, c.size, c.at)
		}
		if at.X+c.w > size.X || at.Y+c.h > size.Y {
			t.Errorf("%s: the %dx%d frame at %v overflows %v", name, c.w, c.h, at, size)
		}
	}
}
