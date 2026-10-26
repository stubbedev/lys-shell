package recorder

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
}

// A node that is no camera, or none at all, is an error naming it and
// the step that failed.
func TestOpenCameraRejectsANonCamera(t *testing.T) {
	rect := image.Rect(0, 0, 384, 216)
	if _, err := openCamera("/dev/null", rect); err == nil || !strings.Contains(err.Error(), "camera /dev/null: VIDIOC_S_FMT") {
		t.Errorf("/dev/null: %v, want the format negotiation to fail", err)
	}
	if _, err := openCamera("/dev/lys-shell-no-camera", rect); err == nil || !strings.Contains(err.Error(), "open the camera /dev/lys-shell-no-camera") {
		t.Errorf("a missing node: %v", err)
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
	rect := image.Rect(0, 0, 384, 216)
	c, err := openCamera(device, rect)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s streams %dx%d %s", device, c.format.width, c.format.height, fourccName(c.format.format))
	for i := range 3 {
		select {
		case img := <-c.Frames():
			b := img.Bounds()
			if b.Dx()*rect.Dy() > (b.Dy()+1)*rect.Dx() || (b.Dx()+1)*rect.Dy() < b.Dy()*rect.Dx() {
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
