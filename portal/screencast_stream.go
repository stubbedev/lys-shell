package portal

import (
	"errors"
	"fmt"
	"image"
	"sync"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/pipewire"
)

// defaultFPS is the advertised capture rate, clamped to the output's
// refresh: 60 keeps a 60 Hz panel smooth without oversampling slower
// ones.
const defaultFPS = 60

// screenStream is one running ScreenCast stream. Ended closes when
// the stream can no longer produce: its source vanished.
type screenStream interface {
	NodeID() uint32
	Size() (width, height int32)
	Ended() <-chan struct{}
	Close()
}

// pipewireStream is a producer fed from a capture source. It ends when
// the source does or when the producer is cut off from PipeWire.
type pipewireStream struct {
	producer      *pipewire.Producer
	width, height int32
	ended         chan struct{}
	stop          chan struct{}
	watched       chan struct{}
	closeSource   func()
}

func newPipewireStream(p *pipewire.Producer, width, height int32, sourceEnded <-chan struct{}, closeSource func()) *pipewireStream {
	s := &pipewireStream{
		producer: p, width: width, height: height, closeSource: closeSource,
		ended: make(chan struct{}), stop: make(chan struct{}), watched: make(chan struct{}),
	}
	go s.watch(sourceEnded)
	return s
}

// watch ends the stream on the first of its source ending and its
// producer losing PipeWire, until Close stops it.
func (s *pipewireStream) watch(sourceEnded <-chan struct{}) {
	defer close(s.watched)
	select {
	case <-sourceEnded:
	case <-s.producer.Done():
		warnf("screencast: the PipeWire stream of node %d ended: %v", s.producer.NodeID(), s.producer.Err())
	case <-s.stop:
		return
	}
	close(s.ended)
}

func (s *pipewireStream) NodeID() uint32              { return s.producer.NodeID() }
func (s *pipewireStream) Size() (width, height int32) { return s.width, s.height }
func (s *pipewireStream) Ended() <-chan struct{}      { return s.ended }

// vanishWatch closes ended the first time a failed capture finds its
// target gone: a closed window or an unplugged output ends the stream,
// a transient copy failure does not.
type vanishWatch struct {
	gone  func() bool
	once  sync.Once
	ended chan struct{}
}

func newVanishWatch(gone func() bool) *vanishWatch {
	return &vanishWatch{gone: gone, ended: make(chan struct{})}
}

// failed checks the target after a failed capture; a source without a
// watch never ends by itself.
func (v *vanishWatch) failed() {
	if v != nil && v.gone() {
		v.once.Do(func() { close(v.ended) })
	}
}

func (s *pipewireStream) Close() {
	close(s.stop)
	<-s.watched
	s.producer.Close()
	s.closeSource()
}

// screenStreams starts ScreenCast streams, allocating a whole output's
// zero-copy buffers through openDevice on the DRM device the compositor
// imports them on.
type screenStreams struct {
	openDevice func(dev uint64) (gpuAllocator, error)
}

// start opens the capture for target and its PipeWire producer. A
// whole output streams zero-copy when the compositor and the GPU allow,
// else, logged with the reason, from an ext-image-copy session
// (damage-driven: a static screen sends nothing new); a compositor
// without it, a region, and a window take one screencopy or toplevel
// capture per frame.
func (st screenStreams) start(target captureTarget, cursor bool, fps uint32) (screenStream, error) {
	if target.kind == targetOutput {
		s, err := st.startGPU(target.name, cursor, fps)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, errNoDmabuf) {
			return nil, err
		}
		warnf("screencast: output %s streams through shared memory: %v", target.name, err)
		if s, err := capture.OpenOutputStream(target.name, cursor); err == nil {
			info := s.Info()
			return startProducer(producerGeometry{
				width: info.Width, height: info.Height, stride: info.Stride, format: info.Format,
				fps: effectiveFPS(fps, info.RefreshMHz), transform: uint32(info.Transform),
			}, &extSource{stream: s}, s.Done(), func() { _ = s.Close() })
		} else if !errors.Is(err, capture.ErrUnsupported) {
			return nil, err
		}
	}
	return startSnapshotStream(target, cursor, fps)
}

// producerGeometry is the stream's fixed layout, from its first frame.
type producerGeometry struct {
	width, height, stride int
	format                capture.Format
	fps, transform        uint32
}

func startProducer(g producerGeometry, src pipewire.Source, ended <-chan struct{}, closeSource func()) (screenStream, error) {
	p, err := pipewire.Start(pipewire.Config{
		Width: uint32(g.width), Height: uint32(g.height), Stride: uint32(g.stride),
		Format: videoFormat(g.format), FPS: g.fps, Transform: g.transform, Source: src,
	})
	if err != nil {
		closeSource()
		return nil, err
	}
	return newPipewireStream(p, int32(g.width), int32(g.height), ended, closeSource), nil
}

// videoFormat maps the wl_shm format the compositor returned; an
// exotic one is offered as BGRx.
func videoFormat(f capture.Format) pipewire.VideoFormat {
	switch f {
	case capture.FormatARGB8888:
		return pipewire.VideoBGRA
	case capture.FormatXBGR8888:
		return pipewire.VideoRGBx
	case capture.FormatABGR8888:
		return pipewire.VideoRGBA
	// 24-bit: DRM BGR888 lays out R, G, B in memory, spa's RGB.
	case capture.FormatBGR888:
		return pipewire.VideoRGB
	case capture.FormatRGB888:
		return pipewire.VideoBGR
	}
	return pipewire.VideoBGRx
}

// extSource hands the producer the ext-image-copy stream's newest
// frame, once each.
type extSource struct {
	stream *capture.Stream
	seq    uint64
	sent   bool
}

func (e *extSource) Fill(dst []byte) ([]pipewire.Rect, bool) {
	f := e.stream.Latest()
	if f == nil || (e.sent && f.Seq == e.seq) {
		return nil, false
	}
	if f.ReadInto(dst) == 0 {
		return nil, false
	}
	e.seq, e.sent = f.Seq, true
	return rects(f.Damage), true
}

func rects(damage []capture.Rect) []pipewire.Rect {
	out := make([]pipewire.Rect, len(damage))
	for i, r := range damage {
		out[i] = pipewire.Rect{X: r.X, Y: r.Y, Width: r.Width, Height: r.Height}
	}
	return out
}

// snapshotSource captures a fresh frame on every fill. Its frames may
// change size (a resized window): Size reports the newest layout, and
// the producer renegotiates the stream to it.
type snapshotSource struct {
	grab                  func(dst []byte) (*capture.Frame, error)
	width, height, stride int
	buf                   []byte
	watch                 *vanishWatch
	// failing is whether the captures fail, logged when that starts
	// and when it stops rather than per frame.
	failing bool
}

func newSnapshotSource(grab func([]byte) (*capture.Frame, error), first *capture.Frame, watch *vanishWatch) *snapshotSource {
	return &snapshotSource{grab: grab, width: first.Width, height: first.Height, stride: first.Stride, buf: first.Data, watch: watch}
}

// Size is the newest frame's layout.
func (s *snapshotSource) Size() (width, height, stride uint32) {
	return uint32(s.width), uint32(s.height), uint32(s.stride)
}

func (s *snapshotSource) Fill(dst []byte) ([]pipewire.Rect, bool) {
	f, err := s.grab(s.buf)
	if err != nil {
		if !s.failing {
			warnf("screencast: capture failing: %v", err)
			s.failing = true
		}
		s.watch.failed()
		return nil, false
	}
	if s.failing {
		warnf("screencast: capture recovered")
		s.failing = false
	}
	s.buf = f.Data
	// A frame of a new layout does not fit the negotiated buffers: it
	// is skipped, and Size makes the producer renegotiate.
	if f.Width != s.width || f.Height != s.height || f.Stride != s.stride {
		s.width, s.height, s.stride = f.Width, f.Height, f.Stride
		return nil, false
	}
	if len(dst) < s.stride*s.height {
		return nil, false
	}
	copyRows(dst, f)
	return rects(f.Damage), true
}

// copyRows copies the frame top-down, undoing a y-inverted screencopy.
func copyRows(dst []byte, f *capture.Frame) {
	if !f.YInvert {
		copy(dst, f.Data[:f.Stride*f.Height])
		return
	}
	for row := range f.Height {
		src := f.Data[(f.Height-1-row)*f.Stride:]
		copy(dst[row*f.Stride:(row+1)*f.Stride], src[:f.Stride])
	}
}

// startSnapshotStream captures per frame on a connection of its own.
func startSnapshotStream(target captureTarget, cursor bool, fps uint32) (screenStream, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to wayland: %w", err)
	}
	opts := capture.Options{Cursor: cursor}
	var grab func([]byte) (*capture.Frame, error)
	var gone func() bool
	var refresh int32
	var transform capture.Transform
	switch target.kind {
	case targetOutput, targetRegion:
		o, ok := findOutput(c, target.name)
		if !ok {
			_ = c.Close()
			return nil, fmt.Errorf("output '%s' is gone", target.name)
		}
		gone = outputGone(c, target.name)
		refresh, transform = o.RefreshMHz, o.Transform
		if target.kind == targetOutput {
			grab = outputGrab(c, o, cursor)
		} else {
			region := image.Rect(int(target.x), int(target.y), int(target.x+target.width), int(target.y+target.height))
			grab = func(dst []byte) (*capture.Frame, error) {
				opts.Dst = dst
				return c.CaptureOutputRegion(o, region, opts)
			}
		}
	case targetWindow:
		t, ok := findToplevel(c, target.name)
		if !ok {
			_ = c.Close()
			return nil, fmt.Errorf("window '%s' is gone", target.name)
		}
		grab = func(dst []byte) (*capture.Frame, error) { opts.Dst = dst; return c.CaptureToplevel(t, opts) }
		gone = func() bool { _, ok := findToplevel(c, target.name); return !ok }
	}
	first, err := grab(nil)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initial capture failed: %w", err)
	}
	watch := newVanishWatch(gone)
	src := newSnapshotSource(grab, first, watch)
	return startProducer(producerGeometry{
		width: first.Width, height: first.Height, stride: first.Stride, format: first.Format,
		fps: effectiveFPS(fps, refresh), transform: uint32(transform),
	}, src, watch.ended, func() { _ = c.Close() })
}

// outputGone reports, on each call, whether the output left.
func outputGone(c *capture.Client, name string) func() bool {
	return func() bool { _, ok := findOutput(c, name); return !ok }
}

func findOutput(c *capture.Client, name string) (capture.Output, bool) {
	for _, o := range c.Outputs() {
		if o.Name == name {
			return o, true
		}
	}
	return capture.Output{}, false
}

func findToplevel(c *capture.Client, id string) (capture.Toplevel, bool) {
	list, err := c.Toplevels()
	if err != nil {
		return capture.Toplevel{}, false
	}
	for _, t := range list {
		if t.Identifier == id {
			return t, true
		}
	}
	return capture.Toplevel{}, false
}

// outputGrab captures a whole output per call, into dst when it fits.
func outputGrab(c *capture.Client, o capture.Output, cursor bool) func(dst []byte) (*capture.Frame, error) {
	opts := capture.Options{Cursor: cursor}
	return func(dst []byte) (*capture.Frame, error) { opts.Dst = dst; return c.CaptureOutput(o, opts) }
}

// startGPU streams a whole output zero-copy when the compositor and
// the GPU allow (screencast_dmabuf.go); errNoDmabuf otherwise, and the
// caller takes the shared-memory paths.
func (st screenStreams) startGPU(output string, cursor bool, fps uint32) (screenStream, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoDmabuf, err)
	}
	fail := func(err error) (screenStream, error) {
		_ = c.Close()
		if errors.Is(err, errNoDmabuf) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errNoDmabuf, err)
	}
	o, ok := findOutput(c, output)
	if !ok {
		return fail(fmt.Errorf("output '%s' is gone", output))
	}
	offered, err := c.DmabufFormat(o, cursor)
	if err != nil {
		return fail(err)
	}
	grab := outputGrab(c, o, cursor)
	first, err := grab(nil)
	if err != nil {
		return fail(err)
	}
	dev, err := st.openDevice(offered.Device)
	if err != nil {
		return fail(err)
	}
	rendered, err := dev.Modifiers(offered.Fourcc)
	if err != nil {
		dev.Close()
		return fail(err)
	}
	exported, err := dev.ExportModifiers(offered.Fourcc)
	if err != nil {
		dev.Close()
		return fail(err)
	}
	watch := newVanishWatch(outputGone(c, output))
	shm := newSnapshotSource(grab, first, watch)
	src, err := probeGPUSource(shm, dev, screencopy{c}, offered, streamLayouts(offered.Modifiers, rendered, exported), o, cursor, first)
	if err != nil {
		dev.Close()
		return fail(err)
	}
	src.watch = watch
	return startProducer(producerGeometry{
		width: first.Width, height: first.Height, stride: first.Stride, format: first.Format,
		fps: effectiveFPS(fps, o.RefreshMHz), transform: uint32(o.Transform),
	}, src, watch.ended, func() {
		dev.Close()
		_ = c.Close()
	})
}
