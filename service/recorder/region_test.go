package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"

	"github.com/stubbedev/lys-shell/swaytest"
)

// A region of a 100x50 logical output maps to the buffer by each
// transform turned back, then the scale; one not inside the output is
// an error naming it.
func TestRegionSource(t *testing.T) {
	logical := image.Pt(100, 50)
	region := image.Rect(10, 5, 40, 25)
	for _, c := range []struct {
		t      capture.Transform
		buffer image.Point
		want   image.Rectangle
	}{
		{capture.TransformNormal, image.Pt(200, 100), image.Rect(20, 10, 80, 50)},
		{capture.Transform90, image.Pt(100, 200), image.Rect(10, 120, 50, 180)},
		{capture.Transform180, image.Pt(200, 100), image.Rect(120, 50, 180, 90)},
		{capture.Transform270, image.Pt(100, 200), image.Rect(50, 20, 90, 80)},
		{capture.TransformFlipped, image.Pt(200, 100), image.Rect(120, 10, 180, 50)},
		{capture.TransformFlipped90, image.Pt(100, 200), image.Rect(10, 20, 50, 80)},
		{capture.TransformFlipped180, image.Pt(200, 100), image.Rect(20, 50, 80, 90)},
		{capture.TransformFlipped270, image.Pt(100, 200), image.Rect(50, 120, 90, 180)},
	} {
		got, err := regionSource(region, logical, c.buffer, c.t)
		if err != nil || got != c.want {
			t.Errorf("transform %d: %v (%v), want %v", c.t, got, err, c.want)
		}
	}
	for _, bad := range []image.Rectangle{image.Rect(90, 0, 110, 10), image.Rect(-1, 0, 10, 10), image.Rect(5, 5, 5, 20)} {
		if _, err := regionSource(bad, logical, image.Pt(200, 100), capture.TransformNormal); err == nil || !strings.Contains(err.Error(), "not inside the 100x50 output") {
			t.Errorf("%v: %v, want an error naming the output", bad, err)
		}
	}
}

// writeNoise writes a PNG whose every pixel differs from its
// neighbours, so a region one pixel off reads different pixels.
func writeNoise(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint32(x*7919+y*104729) * 2654435761
			img.SetRGBA(x, y, color.RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), 255})
		}
	}
	return writePNG(t, "noise.png", img)
}

// writePNG writes img into the test's directory as name.
func writePNG(t *testing.T, name string, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// swayOutput is one output as swaymsg -t get_outputs reports it.
type swayOutput struct {
	Name string `json:"name"`
	Rect struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"rect"`
}

// logicalSize is the output's size in sway's logical space.
func logicalSize(t *testing.T, name string) image.Point {
	t.Helper()
	var outs []swayOutput
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_outputs"), &outs); err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		if o.Name == name {
			return image.Pt(o.Rect.Width, o.Rect.Height)
		}
	}
	t.Fatalf("sway lists no output %s", name)
	return image.Point{}
}

// grab captures the whole output, or region of it, as RGBA in buffer
// orientation.
func grab(t *testing.T, c *capture.Client, o capture.Output, region *image.Rectangle) *image.RGBA {
	t.Helper()
	var f *capture.Frame
	var err error
	if region == nil {
		f, err = c.CaptureOutput(o, capture.Options{})
	} else {
		f, err = c.CaptureOutputRegion(o, *region, capture.Options{})
	}
	if err != nil {
		t.Fatal(err)
	}
	img, err := f.Image()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// For every transform at integer and fractional scales, the buffer
// rectangle a region maps to holds exactly the pixels the compositor
// itself copies for that region.
func TestRegionSourceMatchesTheCompositor(t *testing.T) {
	swaytest.Start(t)
	noise := writeNoise(t, 640, 480)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	name := c.Outputs()[0].Name
	seen := map[capture.Transform]bool{}
	for _, scale := range []string{"1", "2", "1.5"} {
		// sway names rotations clockwise, wl_output anticlockwise: what
		// the output reports is the transform under test.
		for _, transform := range []string{"normal", "90", "180", "270", "flipped", "flipped-90", "flipped-180", "flipped-270"} {
			swaytest.Msg(t, "output", name, "scale", scale, "transform", transform, "bg", noise, "stretch")
			if err := c.Refresh(); err != nil {
				t.Fatal(err)
			}
			o, ok := c.OutputByName(name)
			if !ok {
				t.Fatalf("output %s is gone", name)
			}
			seen[o.Transform] = true
			logical := logicalSize(t, name)
			full := settledScreen(t, c, o, "scale "+scale+" transform "+transform, 1000)
			buffer := full.Bounds().Size()
			for _, region := range []image.Rectangle{
				image.Rect(0, 0, logical.X, logical.Y),
				image.Rect(13, 7, 13+101, 7+57),
				image.Rect(logical.X-77, logical.Y-31, logical.X, logical.Y),
			} {
				what := fmt.Sprintf("scale %s transform %s region %v", scale, transform, region)
				src, err := regionSource(region, logical, buffer, o.Transform)
				if err != nil {
					t.Errorf("%s: %v", what, err)
					continue
				}
				part := grab(t, c, o, &region)
				if part.Bounds().Size() != src.Size() {
					t.Errorf("%s: the compositor copies %v, the mapping gives %v", what, part.Bounds().Size(), src)
					continue
				}
				if !samePixels(full.SubImage(src).(*image.RGBA), part) {
					t.Errorf("%s: the buffer at %v differs from the compositor's copy", what, src)
				}
			}
		}
	}
	if len(seen) != 8 {
		t.Errorf("the output went through transforms %v, want all eight", seen)
	}
}

// settledScreen is the full capture of o once the background has
// redrawn after a reconfiguration: the screen shows at least
// minColours colours of the pattern and two captures in a row agree,
// so what is grabbed or recorded next sees the same frame. swaybg
// redraws on its own time after sway applied the change, blank
// meanwhile. It fails after five seconds with what the screen showed
// last.
func settledScreen(t *testing.T, c *capture.Client, o capture.Output, what string, minColours int) *image.RGBA {
	t.Helper()
	var prev *image.RGBA
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		full := grab(t, c, o, nil)
		n := distinctColors(full)
		if n >= minColours && prev != nil && full.Bounds() == prev.Bounds() && samePixels(full, prev) {
			return full
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the screen never settled on the pattern (last capture %v, %d colours, want %d)", what, full.Bounds(), n, minColours)
		}
		prev = full
	}
}

func distinctColors(img *image.RGBA) int {
	seen := map[color.RGBA]bool{}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y += 3 {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x += 3 {
			seen[img.RGBAAt(x, y)] = true
		}
	}
	return len(seen)
}

func samePixels(a, b *image.RGBA) bool {
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	for y := range h {
		for x := range w {
			if a.RGBAAt(a.Rect.Min.X+x, a.Rect.Min.Y+y) != b.RGBAAt(b.Rect.Min.X+x, b.Rect.Min.Y+y) {
				return false
			}
		}
	}
	return true
}
