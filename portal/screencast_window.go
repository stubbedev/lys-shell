package portal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabuf"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/fourcc"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/pipewire"
)

// A shared window streams zero-copy over an ext-image-copy capture
// session (capture.DmabufSession), opened once the consumer takes the
// GPU buffers: while one is open, the compositor holds back the
// per-frame captures a shared-memory consumer is fed from. The session
// copies the window into
// the stream's own GPU buffers, allocated on the device and in a
// modifier the session names. The session waits for the window to
// change before it copies, so the copies run on a goroutine of their
// own and never on PipeWire's: a buffer handed to the source starts a
// copy into it, and goes out the next time it is handed over once the
// copy landed. The buffers follow the window's size: a resize ends the
// copy with capture.ErrReconfigured, Size reports the new size, and the
// producer renegotiates and allocates anew. Only a session's first frame
// is copied without waiting, so when a consumer drops its buffers (a
// preview handing over to the real share), the next ones get a new
// session, or a still window would never fill them. A window still for
// stillRefresh gets one too, so its frame reaches a consumer that had no
// use for the first one.

// stillRefresh is how often a still source sends its frame again.
const stillRefresh = time.Second

// windowCapturer is the window's capture session on its connection.
type windowCapturer interface {
	importer
	// Open opens the session unless one is open; Formats and Capture
	// need it.
	Open() error
	Formats() ([]capture.DmabufFormat, error)
	Capture(importedBuffer) (capture.FrameInfo, error)
	// Close ends the session if open, waking a Capture that waits; Open
	// starts a new one, whose first frame is copied at once.
	Close() error
}

// windowSession is the windowCapturer over a capture client. mu guards
// s only: a Capture waits without it, and Close wakes that wait.
type windowSession struct {
	c      *capture.Client
	t      capture.Toplevel
	cursor bool
	mu     sync.Mutex
	s      *capture.DmabufSession
}

func (w *windowSession) Import(d dmabuf.Buffer) (importedBuffer, error) { return w.c.ImportDmabuf(d) }

func (w *windowSession) Open() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.s != nil {
		return nil
	}
	s, err := w.c.OpenDmabufSession(w.t, w.cursor)
	if err != nil {
		return fmt.Errorf("screencast: open the window's capture session: %w", err)
	}
	w.s = s
	return nil
}

func (w *windowSession) session() (*capture.DmabufSession, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.s == nil {
		return nil, fmt.Errorf("screencast: the window's capture session: %w", capture.ErrClosed)
	}
	return w.s, nil
}

func (w *windowSession) Formats() ([]capture.DmabufFormat, error) {
	s, err := w.session()
	if err != nil {
		return nil, err
	}
	return s.Formats()
}

func (w *windowSession) Capture(buf importedBuffer) (capture.FrameInfo, error) {
	b, ok := buf.(*capture.DmabufBuffer)
	if !ok {
		return capture.FrameInfo{}, errors.New("screencast: a buffer the window's client did not import")
	}
	s, err := w.session()
	if err != nil {
		return capture.FrameInfo{}, err
	}
	return s.Capture(b)
}

func (w *windowSession) Close() error {
	w.mu.Lock()
	s := w.s
	w.s = nil
	w.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// windowGPUSource is a pipewire.DmabufSource over a window's capture
// session; a consumer that takes only shared memory is fed by the shm
// source, whose size it then reports.
type windowGPUSource struct {
	shm      *snapshotSource
	dev      gpuAllocator
	cap      windowCapturer
	format   fourcc.Format
	video    pipewire.VideoFormat
	modifier fourcc.Modifier
	render   bool
	// watch ends the stream: the window went away, or its new size
	// takes no buffer of the stream's.
	watch *vanishWatch

	mu sync.Mutex
	// width and height are the session's buffer size; gpu is set once
	// the consumer took the GPU buffers.
	width, height uint32
	gpu           bool
	// copied is whether the session took a copy, so that only a new
	// session would fill new buffers from a still window.
	copied bool
	// refresh is how long a copy waits for the window to change before
	// a new session takes its frame anyway; refreshing is set while one
	// wakes the copy for that.
	refresh    time.Duration
	refreshing bool
	// inflight is the buffer a copy is running into; ready are the
	// buffers whose copy landed, with its damage; orphaned are buffers
	// the producer released while their copy ran, freed when it ends.
	inflight *windowBuffer
	ready    map[*windowBuffer][]pipewire.Rect
	orphaned map[*windowBuffer]bool
	closed   bool
	// failing is whether copies fail, logged when that starts and stops.
	failing bool
	copies  sync.WaitGroup
	// wake tells the producer a copy ended, so it need not poll; nil until
	// it asks.
	wake func()
}

// windowBuffer is one of the stream's GPU buffers.
type windowBuffer struct {
	*gpuBuffer
	src           *windowGPUSource
	width, height uint32
}

// Release frees the buffer, or, while a copy runs into it, has the copy
// free it when it ends.
func (b *windowBuffer) Release() {
	s := b.src
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ready, b)
	if s.inflight == b {
		s.orphaned[b] = true
		return
	}
	b.gpuBuffer.Release()
}

// newWindowGPUSource readies the zero-copy path for a window whose shm
// frames are shm's: the session's preferred packed 32-bit format must
// have the shm frames' size, and the first of layouts that allocates
// and imports sets the modifier the stream offers. The first copy is
// not probed: the session copies once the window next paints, which a
// still window may not do for a long time.
func newWindowGPUSource(shm *snapshotSource, dev gpuAllocator, c windowCapturer, offered capture.DmabufFormat, layouts []dmabufprobe.Layout) (*windowGPUSource, error) {
	format := capture.FormatFromFourcc(offered.Fourcc)
	if offered.Width != shm.width || offered.Height != shm.height || format.BytesPerPixel() != 4 {
		return nil, fmt.Errorf("%w: a %v %dx%d dmabuf for a %dx%d window", errNoDmabuf, format, offered.Width, offered.Height, shm.width, shm.height)
	}
	width, errW := intconv.Exact[uint32](offered.Width)
	height, errH := intconv.Exact[uint32](offered.Height)
	if err := errors.Join(errW, errH); err != nil {
		return nil, fmt.Errorf("%w: a %dx%d dmabuf: %w", errNoDmabuf, offered.Width, offered.Height, err)
	}
	s := &windowGPUSource{
		shm: shm, dev: dev, cap: c, format: offered.Fourcc, video: videoFormat(format),
		width: width, height: height, watch: shm.watch,
		ready: map[*windowBuffer][]pipewire.Rect{}, orphaned: map[*windowBuffer]bool{},
		refresh: stillRefresh,
	}
	modifier, layout, err := dmabufprobe.First(layouts, func(l dmabufprobe.Layout) (fourcc.Modifier, error) {
		b, err := allocImported(dev, c, width, height, s.format, l)
		if err != nil {
			return 0, err
		}
		b.Release()
		return b.modifier, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v: %w", errNoDmabuf, format, err)
	}
	s.modifier, s.render = modifier, layout.Render
	return s, nil
}

func (s *windowGPUSource) Fill(dst []byte) ([]pipewire.Rect, bool) { return s.shm.Fill(dst) }

func (s *windowGPUSource) Modifier() fourcc.Modifier { return s.modifier }

func (s *windowGPUSource) DmabufVideoFormat() pipewire.VideoFormat { return s.video }

// Size is the layout of the frames the consumer takes: the session's
// buffer size once it took the GPU buffers, else the shm frames'.
func (s *windowGPUSource) Size() (width, height, stride uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.gpu {
		return s.shm.Size()
	}
	return s.width, s.height, s.width * 4
}

// AllocDmabuf opens the session if this is the first GPU buffer, and
// allocates and imports one buffer of the session's current size at the
// probed modifier.
func (s *windowGPUSource) AllocDmabuf() (pipewire.Dmabuf, error) {
	s.mu.Lock()
	if !s.gpu {
		if err := s.cap.Open(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		s.gpu = true
		s.resize()
	}
	width, height := s.width, s.height
	s.mu.Unlock()
	b, err := allocImported(s.dev, s.cap, width, height, s.format, dmabufprobe.Layout{Modifier: s.modifier, Render: s.render})
	if err != nil {
		return nil, err
	}
	return &windowBuffer{gpuBuffer: b, src: s, width: width, height: height}, nil
}

// FillDmabuf sends d once a copy into it landed, and otherwise starts
// one into it when none runs.
func (s *windowGPUSource) FillDmabuf(d pipewire.Dmabuf) ([]pipewire.Rect, bool) {
	b, ok := d.(*windowBuffer)
	if !ok || b.src != s {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if damage, ok := s.ready[b]; ok {
		delete(s.ready, b)
		return damage, true
	}
	s.startCopy(b)
	return nil, false
}

// PrepareDmabuf starts the next copy as the last frame goes out, so a
// changing window has a frame ready every cycle.
func (s *windowGPUSource) PrepareDmabuf(d pipewire.Dmabuf) {
	b, ok := d.(*windowBuffer)
	if !ok || b.src != s {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCopy(b)
}

// startCopy runs a copy into b unless one runs, the source closed, or b
// is of a size the session left behind. s.mu is held.
func (s *windowGPUSource) startCopy(b *windowBuffer) {
	if s.inflight != nil || s.closed || b.width != s.width || b.height != s.height {
		return
	}
	s.inflight = b
	s.copied = true
	s.copies.Add(1)
	go s.copyInto(b)
}

// NotifyFrames has each ended copy wake the producer.
func (s *windowGPUSource) NotifyFrames(wake func()) { s.wake = wake }

// Restart gives the buffers that follow dropped ones a session of their
// own, once the session in use has taken a copy; it wakes a copy waiting
// on the old one.
func (s *windowGPUSource) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.gpu || !s.copied || s.closed {
		return
	}
	s.copied = false
	if err := s.cap.Close(); err != nil {
		warnf("screencast: close the window's capture session to restart it: %v", err)
	}
	if err := s.cap.Open(); err != nil {
		warnf("screencast: restart the window's capture session: %v", err)
		s.watch.end()
		return
	}
	s.resize()
}

// wakeForRefresh ends the wait of a copy into b that is still running,
// for copyInto to take the window's frame from a new session.
func (s *windowGPUSource) wakeForRefresh(b *windowBuffer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight != b || s.closed || s.orphaned[b] {
		return
	}
	s.refreshing = true
	if err := s.cap.Close(); err != nil {
		warnf("screencast: close the still window's capture session: %v", err)
	}
}

// capture copies the window into b, refreshing while it does not change.
func (s *windowGPUSource) capture(b *windowBuffer) (capture.FrameInfo, error) {
	for {
		s.mu.Lock()
		refresh := s.refresh
		s.mu.Unlock()
		wake := time.AfterFunc(refresh, func() { s.wakeForRefresh(b) })
		info, err := s.cap.Capture(b.imported)
		wake.Stop()
		s.mu.Lock()
		refreshing := s.refreshing && !s.closed && !s.orphaned[b]
		s.refreshing = false
		if !refreshing {
			s.mu.Unlock()
			return info, err
		}
		openErr := s.cap.Open()
		s.mu.Unlock()
		if openErr != nil {
			return capture.FrameInfo{}, fmt.Errorf("reopen the still window's capture session: %w", openErr)
		}
		if err == nil {
			return info, nil
		}
	}
}

// copyInto runs one copy into b and files its outcome.
func (s *windowGPUSource) copyInto(b *windowBuffer) {
	defer s.copies.Done()
	defer func() {
		if s.wake != nil {
			s.wake()
		}
	}()
	info, err := s.capture(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = nil
	if s.orphaned[b] {
		delete(s.orphaned, b)
		b.gpuBuffer.Release()
		return
	}
	switch {
	case err == nil:
		if s.failing {
			warnf("screencast: window capture recovered")
			s.failing = false
		}
		s.ready[b] = rects(info.Damage)
	case errors.Is(err, capture.ErrClosed):
	case errors.Is(err, capture.ErrSessionStopped):
		s.watch.end()
	case errors.Is(err, capture.ErrReconfigured):
		s.resize()
	default:
		if !s.failing {
			warnf("screencast: window capture failing: %v", err)
			s.failing = true
		}
	}
}

// resize takes the session's buffer size; a session whose constraints
// take no dmabuf of the stream's format ends the stream, as nothing can
// be copied into it any more.
func (s *windowGPUSource) resize() {
	formats, err := s.cap.Formats()
	if err == nil {
		for _, f := range formats {
			if f.Fourcc != s.format {
				continue
			}
			width, errW := intconv.Exact[uint32](f.Width)
			height, errH := intconv.Exact[uint32](f.Height)
			if err = errors.Join(errW, errH); err == nil {
				s.width, s.height = width, height
				return
			}
		}
		if err == nil {
			err = fmt.Errorf("no %v format among %d", s.format, len(formats))
		}
	}
	warnf("screencast: the resized window takes no buffer of the stream's: %v", err)
	s.watch.end()
}

// Close ends the session, waits for the copy it wakes, and frees what
// that copy held. The producer must have released its buffers first.
func (s *windowGPUSource) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if err := s.cap.Close(); err != nil {
		warnf("screencast: close the window's capture session: %v", err)
	}
	s.copies.Wait()
}
