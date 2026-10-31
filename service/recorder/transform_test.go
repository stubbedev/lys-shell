package recorder

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-lib/capture"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
	"github.com/stubbedev/lys-shell/swaytest"
)

// blockSize is the side of the test pattern's blocks in pixels: large
// enough that H.264 keeps each block's colour at the centre.
const blockSize = 32

// blockColour is the pattern's colour for the block at bx, by: no two
// neighbours alike, so a picture turned, flipped or shifted by a block
// reads different colours.
func blockColour(bx, by int) color.RGBA {
	v := uint32(bx*7919+by*104729+1) * 2654435761
	return color.RGBA{uint8(v>>24) | 0x20, uint8(v>>16) | 0x20, uint8(v>>8) | 0x20, 255}
}

// writeBlocks writes the pattern at size, the output's upright pixel
// size, so sway's stretched background shows it pixel for pixel.
func writeBlocks(t *testing.T, size image.Point) string {
	t.Helper()
	img := image.NewRGBA(image.Rectangle{Max: size})
	for y := range size.Y {
		for x := range size.X {
			img.SetRGBA(x, y, blockColour(x/blockSize, y/blockSize))
		}
	}
	return writePNG(t, fmt.Sprintf("blocks-%dx%d.png", size.X, size.Y), img)
}

// colourNear compares two colours within H.264's error at a block's
// centre.
func colourNear(a, b color.RGBA) bool {
	d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
	return d(a.R, b.R) <= 24 && d(a.G, b.G) <= 24 && d(a.B, b.B) <= 24
}

// The recording shows the output upright whatever its transform, and a
// region of it pixel-exact: every block centre of the decoded frame has
// the colour the upright screen has there. The output is 1280x720 at
// scale 2, so a region's logical coordinates double into pixels.
func TestRecordsTransformedOutputsAndRegionsUpright(t *testing.T) {
	requireFFmpeg(t)
	output := gpuSway(t)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mode := image.Pt(1280, 720)
	for _, transform := range []string{"normal", "90", "180", "270", "flipped", "flipped-90", "flipped-180", "flipped-270"} {
		upright := mode
		if transform == "90" || transform == "270" || transform == "flipped-90" || transform == "flipped-270" {
			upright = image.Pt(mode.Y, mode.X)
		}
		swaytest.Msg(t, "output", output, "scale", "2", "transform", transform, "bg", writeBlocks(t, upright), "stretch")
		if err := c.Refresh(); err != nil {
			t.Fatal(err)
		}
		o, ok := c.OutputByName(output)
		if !ok {
			t.Fatalf("output %s is gone", output)
		}
		settledScreen(t, c, o, "transform "+transform, (upright.X/blockSize)*(upright.Y/blockSize)/2)
		logical := logicalSize(t, output)
		layout := func(name string) (image.Point, bool) { return logical, name == output }
		for _, target := range []sharepicker.Target{
			{Kind: sharepicker.TargetOutput, Name: output},
			{Kind: sharepicker.TargetRegion, Name: output, X: 37, Y: 21, Width: 150, Height: 101},
		} {
			t.Run(transform+" "+target.Payload(), func(t *testing.T) {
				want := image.Rectangle{Max: upright}
				if target.Kind == sharepicker.TargetRegion {
					want = image.Rect(int(target.X)*2, int(target.Y)*2, int(target.X+target.Width)*2, int(target.Y+target.Height)*2)
				}
				e := NewGPUEngine(&fakePicker{reply: "/" + target.Payload()}, nil, layout, "")
				s, err := e.OpenSession(context.Background(), false)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				path := filepath.Join(t.TempDir(), "rec.mkv")
				rec, err := s.Start(Options{OutputPath: path, Format: config.RecorderFormatMkv, Framerate: 30}, func(r string) { t.Errorf("reported %q", r) })
				if err != nil {
					t.Fatal(err)
				}
				r, ok := rec.(*recording)
				if !ok {
					t.Fatalf("the engine started a %T", rec)
				}
				waitRecording(t, "ten frames recorded", func() bool { return r.videoFrames.Load() >= 10 })
				if err := rec.Stop(); err != nil {
					t.Fatal(err)
				}
				frame := lastFrame(t, path, want.Size())
				checked, wrong := 0, 0
				for by := want.Min.Y / blockSize; by*blockSize < want.Max.Y; by++ {
					for bx := want.Min.X / blockSize; bx*blockSize < want.Max.X; bx++ {
						centre := image.Pt(bx*blockSize+blockSize/2, by*blockSize+blockSize/2)
						if !centre.In(want.Inset(4)) {
							continue
						}
						checked++
						got := frame.RGBAAt(centre.X-want.Min.X, centre.Y-want.Min.Y)
						if !colourNear(got, blockColour(bx, by)) {
							wrong++
						}
					}
				}
				if checked < 10 || wrong > 0 {
					t.Errorf("%d of %d block centres read the wrong colour", wrong, checked)
				}
			})
		}
	}
}

// The webcam overlay sits in the upright frame of a turned output: its
// rectangle is measured on the recording, not on the buffer.
func TestWebcamRectOnATurnedOutputIsUpright(t *testing.T) {
	s := &screen{source: image.Rect(0, 0, 1280, 720)}
	s.output.Transform = 1
	size := s.frameSize()
	if size != image.Pt(720, 1280) {
		t.Fatalf("frame size %v, want the upright 720x1280", size)
	}
	if r := webcamRect(Webcam{X: 100, Y: 100, Size: 50}, int32(size.X), int32(size.Y)); !r.In(image.Rectangle{Max: size}) || r.Dx() != 360 {
		t.Errorf("webcam at %v, want half the upright width inside it", r)
	}
}
