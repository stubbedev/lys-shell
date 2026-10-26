// Package v4l2 streams a V4L2 capture node in pure Go (mmap buffers over
// golang.org/x/sys/unix ioctls), delivering each frame as an RGBA
// picture letterboxed to a requested aspect, and names the capture
// nodes a system has.
package v4l2

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// V4L2 streaming (videodev2.h): memory-mapped capture buffers.
const (
	vidiocSFmt      = 0xc0d05605
	vidiocReqbufs   = 0xc0145608
	vidiocQuerybuf  = 0xc0585609
	vidiocQbuf      = 0xc058560f
	vidiocDqbuf     = 0xc0585611
	vidiocStreamon  = 0x40045612
	vidiocStreamoff = 0x40045613

	v4l2BufTypeVideoCapture = 1
	v4l2MemoryMmap          = 1
	v4l2FieldNone           = 1
)

// cameraBuffers are the capture buffers the driver cycles.
const cameraBuffers = 4

// MinWidth is the least width asked of a camera, so a small picture
// still gets one worth scaling.
const MinWidth = 640

// v4l2PixFormat mirrors struct v4l2_pix_format (48 bytes): the size,
// pixel format, field and line stride, then six fields left zero.
type v4l2PixFormat struct {
	width, height, pixelformat, field, bytesperline uint32
	_                                               [7]uint32
}

// v4l2Format mirrors struct v4l2_format (208 bytes on LP64): the type,
// then a pointer-aligned 200-byte union.
type v4l2Format struct {
	typ uint32
	_   uint32
	pix v4l2PixFormat
	_   [200 - unsafe.Sizeof(v4l2PixFormat{})]byte
}

// v4l2RequestBuffers mirrors struct v4l2_requestbuffers (20 bytes):
// count, type and memory, then the driver's capabilities and flags.
type v4l2RequestBuffers struct {
	count, typ, memory uint32
	_                  [8]uint8
}

// v4l2Buffer mirrors struct v4l2_buffer (88 bytes on LP64): index,
// type and bytes used, then flags, field, the padded timeval, timecode
// and sequence; memory, the union m, whose mmap offset is its first 32
// bits, and length, then the request fd and padding.
type v4l2Buffer struct {
	index, typ, bytesused uint32
	_                     [48]byte
	memory                uint32
	offset                uint32
	_                     uint32
	length                uint32
	_                     [12]byte
}

// ioctl issues a V4L2 request on the struct arg points at, whose size
// req encodes; an interrupted call is retried.
func ioctl[T any](fd int, req uintptr, arg *T) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(arg))) //nolint:gosec // audited: every request's struct mirrors videodev2.h, sizes pinned by TestV4L2StreamingStructSizes
		switch errno {
		case 0:
			return nil
		case unix.EINTR:
			continue
		}
		return errno
	}
}

// Camera streams a V4L2 capture node, letterboxing each frame on its
// own goroutine.
type Camera struct {
	device string
	fd     int
	wake   int
	maps   [][]byte
	format cameraFrame
	maxFPS int
	at     image.Point

	frames chan *image.RGBA
	free   chan *image.RGBA
	done   chan struct{}
	err    error
	once   sync.Once
}

// Open starts streaming device (YUYV, else MJPEG, about frame's width,
// at least MinWidth), its pictures letterboxed to frame's aspect,
// converting at most maxFPS of them a second.
func Open(device string, frame image.Point, maxFPS int) (*Camera, error) {
	if frame.X <= 0 || frame.Y <= 0 || maxFPS <= 0 {
		return nil, fmt.Errorf("v4l2: camera %s: a %v frame at %d fps", device, frame, maxFPS)
	}
	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("v4l2: open the camera %s: %w", device, err)
	}
	c := &Camera{device: device, fd: fd, wake: -1, maxFPS: maxFPS}
	if err := c.setup(frame); err != nil {
		c.release()
		return nil, fmt.Errorf("v4l2: camera %s: %w", device, err)
	}
	go c.run()
	return c, nil
}

// setup negotiates the format, maps and queues the buffers, and starts
// the stream.
func (c *Camera) setup(frame image.Point) error {
	w := max(frame.X, MinWidth)
	h := w * frame.Y / frame.X
	var f v4l2Format
	for _, pix := range []uint32{pixYUYV, pixMJPG} {
		f = v4l2Format{typ: v4l2BufTypeVideoCapture, pix: v4l2PixFormat{width: uint32(w), height: uint32(h), pixelformat: pix, field: v4l2FieldNone}}
		if err := ioctl(c.fd, vidiocSFmt, &f); err != nil {
			return fmt.Errorf("VIDIOC_S_FMT %s %dx%d: %w", fourccName(pix), w, h, err)
		}
		if f.pix.pixelformat == pix {
			break
		}
	}
	c.format = cameraFrame{width: int(f.pix.width), height: int(f.pix.height), format: f.pix.pixelformat, stride: int(f.pix.bytesperline)}
	if c.format.format != pixYUYV && c.format.format != pixMJPG {
		return fmt.Errorf("it offers %s, neither YUYV nor MJPG", fourccName(c.format.format))
	}
	if c.format.width <= 0 || c.format.height <= 0 {
		return fmt.Errorf("it negotiated a %dx%d frame", c.format.width, c.format.height)
	}
	req := v4l2RequestBuffers{count: cameraBuffers, typ: v4l2BufTypeVideoCapture, memory: v4l2MemoryMmap}
	if err := ioctl(c.fd, vidiocReqbufs, &req); err != nil {
		return fmt.Errorf("VIDIOC_REQBUFS: %w", err)
	}
	if req.count == 0 {
		return errors.New("VIDIOC_REQBUFS granted no buffers")
	}
	for i := range req.count {
		b := v4l2Buffer{index: i, typ: v4l2BufTypeVideoCapture, memory: v4l2MemoryMmap}
		if err := ioctl(c.fd, vidiocQuerybuf, &b); err != nil {
			return fmt.Errorf("VIDIOC_QUERYBUF %d: %w", i, err)
		}
		m, err := unix.Mmap(c.fd, int64(b.offset), int(b.length), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			return fmt.Errorf("map capture buffer %d: %w", i, err)
		}
		c.maps = append(c.maps, m)
		if err := ioctl(c.fd, vidiocQbuf, &b); err != nil {
			return fmt.Errorf("VIDIOC_QBUF %d: %w", i, err)
		}
	}
	typ := uint32(v4l2BufTypeVideoCapture)
	if err := ioctl(c.fd, vidiocStreamon, &typ); err != nil {
		return fmt.Errorf("VIDIOC_STREAMON: %w", err)
	}
	wake, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return fmt.Errorf("eventfd: %w", err)
	}
	c.wake = wake
	// One frame uploading, one waiting, one converting.
	c.frames, c.free, c.done = make(chan *image.RGBA, 1), make(chan *image.RGBA, 3), make(chan struct{})
	for range cap(c.free) {
		img, at := paddedFrame(c.format, frame)
		c.at = at
		c.free <- img
	}
	return nil
}

// Frames delivers the newest pictures; it closes when the camera
// stops, Err then saying why.
func (c *Camera) Frames() <-chan *image.RGBA { return c.frames }

// Recycle hands a picture from Frames back for the next frame.
func (c *Camera) Recycle(img *image.RGBA) { c.free <- img }

// Format names the negotiated stream: size and pixel format.
func (c *Camera) Format() string {
	return fmt.Sprintf("%dx%d %s", c.format.width, c.format.height, fourccName(c.format.format))
}

// Err is why Frames closed.
func (c *Camera) Err() error {
	<-c.done
	return c.err
}

// run dequeues frames until Close wakes it or the device fails,
// converting at most maxFPS of them a second.
func (c *Camera) run() {
	defer close(c.done)
	defer close(c.frames)
	var last time.Time
	fds := []unix.PollFd{{Fd: int32(c.fd), Events: unix.POLLIN}, {Fd: int32(c.wake), Events: unix.POLLIN}}
	for {
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			c.err = fmt.Errorf("poll %s: %w", c.device, err)
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP) != 0 {
			c.err = fmt.Errorf("%s hung up", c.device)
			return
		}
		b := v4l2Buffer{typ: v4l2BufTypeVideoCapture, memory: v4l2MemoryMmap}
		if err := ioctl(c.fd, vidiocDqbuf, &b); err != nil {
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			c.err = fmt.Errorf("VIDIOC_DQBUF on %s: %w", c.device, err)
			return
		}
		if now := time.Now(); now.Sub(last) >= time.Second/time.Duration(c.maxFPS) && int(b.index) < len(c.maps) {
			if err := c.convert(c.maps[b.index][:min(int(b.bytesused), len(c.maps[b.index]))]); err != nil {
				c.err = fmt.Errorf("%s: %w", c.device, err)
				return
			}
			last = now
		}
		if err := ioctl(c.fd, vidiocQbuf, &b); err != nil {
			c.err = fmt.Errorf("VIDIOC_QBUF on %s: %w", c.device, err)
			return
		}
	}
}

// convert decodes data into a free picture and publishes it, replacing
// one not taken yet. With every picture in use the frame is dropped.
func (c *Camera) convert(data []byte) error {
	var img *image.RGBA
	select {
	case img = <-c.free:
	default:
		return nil
	}
	if err := decodeFrame(img, c.at, c.format, data); err != nil {
		c.free <- img
		return err
	}
	select {
	case c.frames <- img:
	default:
		select {
		case old := <-c.frames:
			c.free <- old
		default:
		}
		c.frames <- img
	}
	return nil
}

// Close stops the stream and releases the device.
func (c *Camera) Close() {
	c.once.Do(func() {
		var one [8]byte
		one[0] = 1
		_, _ = unix.Write(c.wake, one[:])
		<-c.done
		c.release()
	})
}

// release stops streaming and frees the maps and descriptors.
func (c *Camera) release() {
	typ := uint32(v4l2BufTypeVideoCapture)
	_ = ioctl(c.fd, vidiocStreamoff, &typ)
	for _, m := range c.maps {
		_ = unix.Munmap(m)
	}
	c.maps = nil
	_ = unix.Close(c.fd)
	if c.wake >= 0 {
		_ = unix.Close(c.wake)
	}
}
