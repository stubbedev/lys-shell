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
