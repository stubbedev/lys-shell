package v4l2

import (
	"image"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// The ioctl structs have the kernel's LP64 sizes, which the request
// numbers encode.
func TestV4L2StreamingStructSizes(t *testing.T) {
	for name, c := range map[string]struct{ got, want uintptr }{
		"v4l2_format":         {unsafe.Sizeof(v4l2Format{}), 208},
		"v4l2_requestbuffers": {unsafe.Sizeof(v4l2RequestBuffers{}), 20},
		"v4l2_buffer":         {unsafe.Sizeof(v4l2Buffer{}), 88},
		"v4l2_pix_format":     {unsafe.Sizeof(v4l2PixFormat{}), 48},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d bytes, want %d", name, c.got, c.want)
		}
	}
	if off := unsafe.Offsetof(v4l2Buffer{}.offset); off != 64 {
		t.Errorf("v4l2_buffer.m is at %d, want 64", off)
	}
	if off := unsafe.Offsetof(v4l2Buffer{}.length); off != 72 {
		t.Errorf("v4l2_buffer.length is at %d, want 72", off)
	}
	if got := unsafe.Sizeof(v4l2Capability{}); got != 104 {
		t.Errorf("v4l2_capability is %d bytes, want 104", got)
	}
}

// A node that is no camera, or none at all, is an error naming it and
// the step that failed; so is an empty frame or no frame rate.
func TestOpenRejectsANonCamera(t *testing.T) {
	for _, bad := range []struct {
		frame image.Point
		fps   int
	}{{image.Pt(0, 9), 30}, {image.Pt(16, 9), 0}} {
		if _, err := Open("/dev/video0", bad.frame, bad.fps); err == nil {
			t.Errorf("a %v frame at %d fps opened", bad.frame, bad.fps)
		}
	}
	frame := image.Pt(384, 216)
	if _, err := Open("/dev/null", frame, 30); err == nil || !strings.Contains(err.Error(), "v4l2: camera /dev/null: VIDIOC_S_FMT") {
		t.Errorf("/dev/null: %v, want the format negotiation to fail", err)
	}
	if _, err := Open("/dev/lys-shell-no-camera", frame, 30); err == nil || !strings.Contains(err.Error(), "v4l2: open the camera /dev/lys-shell-no-camera") {
		t.Errorf("a missing node: %v", err)
	}
}

// A node that does not exist, or is no V4L2 device, has no capture
// card.
func TestCaptureCardOfANonCamera(t *testing.T) {
	for _, path := range []string{"/dev/lys-shell-no-camera", "/dev/null"} {
		if card, ok := CaptureCard(path); ok {
			t.Errorf("%s probed as the camera %q", path, card)
		}
	}
}

// A real camera streams letterboxed frames until Close, which ends
// Frames without an error. Opt-in, as it switches the camera on:
// LYS_SHELL_TEST_CAMERA names the node.
func TestRealCameraStreams(t *testing.T) {
	device := os.Getenv("LYS_SHELL_TEST_CAMERA")
	if device == "" {
		t.Skip("LYS_SHELL_TEST_CAMERA names no camera node; this test switches one on")
	}
	frame := image.Pt(384, 216)
	c, err := Open(device, frame, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s streams %s", device, c.Format())
	for i := range 3 {
		select {
		case img := <-c.Frames():
			b := img.Bounds()
			if b.Dx()*frame.Y > (b.Dy()+1)*frame.X || (b.Dx()+1)*frame.Y < b.Dy()*frame.X {
				t.Errorf("frame %d is %v, not 16:9", i, b)
			}
			c.Recycle(img)
		case <-time.After(5 * time.Second):
			t.Fatalf("no frame %d within 5s", i)
		}
	}
	c.Close()
	for range c.Frames() {
	}
	if err := c.Err(); err != nil {
		t.Errorf("a closed camera reports %v", err)
	}
}
