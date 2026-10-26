package v4l2

import (
	"os"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// v4l2Capability mirrors struct v4l2_capability (104 bytes).
type v4l2Capability struct {
	driver       [16]byte
	card         [32]byte
	busInfo      [32]byte
	version      uint32
	capabilities uint32
	deviceCaps   uint32
	reserved     [3]uint32
}

// V4L2 constants (videodev2.h).
const (
	vidiocQuerycap      = 0x80685600
	v4l2CapVideoCapture = 0x00000001
	v4l2CapDeviceCaps   = 0x80000000
)

// CaptureCard is the card name of a node that captures video, else
// false.
func CaptureCard(path string) (string, bool) {
	f, err := os.Open(path) //nolint:gosec // a /dev/videoN node
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	var c v4l2Capability
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), vidiocQuerycap, uintptr(unsafe.Pointer(&c))); errno != 0 { //nolint:gosec // audited: VIDIOC_QUERYCAP fills the 104-byte struct
		return "", false
	}
	caps := c.capabilities
	if caps&v4l2CapDeviceCaps != 0 {
		caps = c.deviceCaps
	}
	if caps&v4l2CapVideoCapture == 0 {
		return "", false
	}
	end := slices.Index(c.card[:], 0)
	if end < 0 {
		end = len(c.card)
	}
	card := strings.TrimSpace(string(c.card[:end]))
	return card, card != ""
}
