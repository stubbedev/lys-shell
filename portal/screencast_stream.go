package portal

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/pipewire"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// defaultFPS is the advertised capture rate, clamped to the output's
// refresh: 60 keeps a 60 Hz panel smooth without oversampling slower
// ones.
const defaultFPS = 60

// screenStream is one running ScreenCast stream. Ended closes when
// the stream can no longer produce: its source vanished.
type screenStream interface {
	NodeID() uint32
	Serial() uint64
	Size() (width, height int32)
	Ended() <-chan struct{}
	Close()
}

// pipewireStream is a producer fed from a capture source. It ends when
// the source does or when the producer is cut off from PipeWire.
type pipewireStream struct {
	producer      *pipewire.Producer
	source        pipewire.Source
	width, height int32
	ended         chan struct{}
	stop          chan struct{}
	watched       chan struct{}
	closeSource   func()
}

func newPipewireStream(p *pipewire.Producer, src pipewire.Source, width, height int32, sourceEnded <-chan struct{}, closeSource func()) *pipewireStream {
	s := &pipewireStream{
		producer: p, source: src, width: width, height: height, closeSource: closeSource,
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
func (s *pipewireStream) Serial() uint64              { return s.producer.Serial() }
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
		v.end()
	}
}

// end ends the stream, once.
func (v *vanishWatch) end() { v.once.Do(func() { close(v.ended) }) }

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

// start opens the capture for target, the pointer when the cursor mode
// carries it beside the frames, and its PipeWire producer.
func (st screenStreams) start(target sharepicker.Target, mode cursorMode, fps uint32) (screenStream, error) {
	cur := castCursor{paint: mode == cursorEmbedded}
	if mode == cursorMetadata {
		p, err := openCastPointer(target)
		if err != nil {
			return nil, err
		}
		cur.pointer = p
	}
	s, err := st.startCapture(target, cur, fps)
	if err != nil && cur.pointer != nil {
		cur.pointer.close()
	}
	return s, err
}

// castCursor is how a stream shows the pointer: painted into its frames,
// carried beside them by pointer, or neither.
type castCursor struct {
	paint   bool
	pointer *castPointer
}

// startCapture opens the capture for target and its producer. An
// output, a region and a window stream zero-copy when the compositor and
// the GPU allow; else, logged with the reason, an output streams from an
// ext-image-copy session (damage-driven: a static screen sends nothing
// new), and a compositor without it, a region, and a window take one
// screencopy or toplevel capture per frame.
func (st screenStreams) startCapture(target sharepicker.Target, cur castCursor, fps uint32) (screenStream, error) {
	if target.Kind == sharepicker.TargetWindow {
		s, err := st.startWindowGPU(target.Name, cur, fps)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, errNoDmabuf) {
			return nil, err
		}
		warnf("screencast: window %s streams through shared memory: %v", target.Name, err)
	}
	if target.Kind == sharepicker.TargetOutput || target.Kind == sharepicker.TargetRegion {
		s, err := st.startGPU(target, cur, fps)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, errNoDmabuf) {
			return nil, err
		}
		if target.Kind == sharepicker.TargetRegion {
			warnf("screencast: a region of output %s streams through shared memory: %v", target.Name, err)
			return startSnapshotStream(target, cur, fps)
		}
		warnf("screencast: output %s streams through shared memory: %v", target.Name, err)
		if s, err := capture.OpenOutputStream(target.Name, cur.paint); err == nil {
			info := s.Info()
			return startProducer(producerGeometry{
				width: info.Width, height: info.Height, stride: info.Stride, format: info.Format,
				fps: effectiveFPS(fps, info.RefreshMHz), transform: uint32(info.Transform),
			}, &extSource{stream: s}, cur.pointer, s.Done(), func() { _ = s.Close() })
		} else if !errors.Is(err, capture.ErrUnsupported) {
			return nil, err
		}
	}
	return startSnapshotStream(target, cur, fps)
}

// producerGeometry is the stream's fixed layout, from its first frame.
type producerGeometry struct {
	width, height, stride int
	format                capture.Format
	fps, transform        uint32
}

// startProducer streams src, with pointer beside the frames when it is not
// nil. A producer that does not start closes the source, and leaves the
// pointer to the caller; a running stream closes both.
func startProducer(g producerGeometry, src pipewire.Source, pointer *castPointer, ended <-chan struct{}, closeSource func()) (screenStream, error) {
	width, errW := intconv.Exact[int32](g.width)
	height, errH := intconv.Exact[int32](g.height)
	uwidth, errUW := intconv.Exact[uint32](g.width)
	uheight, errUH := intconv.Exact[uint32](g.height)
	stride, errS := intconv.Exact[uint32](g.stride)
	if err := errors.Join(errW, errH, errUW, errUH, errS); err != nil {
		closeSource()
		return nil, fmt.Errorf("screencast: a %dx%d stream of stride %d: %w", g.width, g.height, g.stride, err)
	}
	cfg := pipewire.Config{
		Application: shellName, Name: screencastNode,
		Width: uwidth, Height: uheight, Stride: stride,
		Format: videoFormat(g.format), FPS: g.fps, Transform: g.transform, Source: src,
	}
	closeAll := closeSource
	if pointer != nil {
		pointer.fit(g.width, g.height)
		cfg.Cursor = pointer
		closeAll = func() {
			closeSource()
			pointer.close()
		}
	}
	p, err := pipewire.Start(cfg)
	if err != nil {
		closeSource()
		return nil, err
	}
	return newPipewireStream(p, src, width, height, ended, closeAll), nil
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
	frame  capture.StreamFrame
	seq    uint64
	sent   bool
	at     time.Time
}

// Restart has the next fill send the latest frame again: a consumer's
// new buffers hold nothing, and a still output brings no newer one.
func (e *extSource) Restart() { e.sent = false }

func (e *extSource) Fill(dst []byte) ([]pipewire.Rect, bool) {
	if !e.stream.Latest(&e.frame) {
		return nil, false
	}
	f := &e.frame
	now := time.Now()
	if e.sent && f.Seq == e.seq && now.Sub(e.at) < stillRefresh {
		return nil, false
	}
	if f.ReadInto(dst) == 0 {
		return nil, false
	}
	e.seq, e.sent, e.at = f.Seq, true, now
	return rects(f.Damage), true
}

// castPointer is the pointer a metadata cursor mode stream carries: the
// capture cursor over its output or window, whose frames it is placed in
// as the compositor places it. A region's frames are the region of its
// output (in the output's buffer pixels, the logical region scaled as the
// frames are): the pointer is placed from the region's corner, and off the
// region it is not over the frames.
type castPointer struct {
	cursor *capture.Cursor
	// region is a region target's logical rectangle, nil for a whole
	// output or a window; crop is it in the cursor's coordinates, set by
	// fit.
	region, crop *image.Rectangle
}

// openCastPointer follows the pointer over target's output or window.
func openCastPointer(target sharepicker.Target) (*castPointer, error) {
	open := capture.OpenOutputCursor
	if target.Kind == sharepicker.TargetWindow {
		open = capture.OpenToplevelCursor
	}
	k, err := open(target.Name)
	if err != nil {
		return nil, fmt.Errorf("screencast: follow the pointer over %s: %w", target.Payload(), err)
	}
	return &castPointer{cursor: k, region: regionOf(target)}, nil
}

// fit places a region's pointer in its frames of width x height.
func (p *castPointer) fit(width, height int) {
	r := p.region
	if r == nil || r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	crop := image.Rect(0, 0, width, height).Add(image.Pt(r.Min.X*width/r.Dx(), r.Min.Y*height/r.Dy()))
	p.crop = &crop
}

func (p *castPointer) CursorState() pipewire.CursorState {
	s := p.cursor.State()
	at := image.Pt(s.X, s.Y)
	if !s.Inside {
		return pipewire.CursorState{}
	}
	if c := p.crop; c != nil {
		if !at.In(*c) {
			return pipewire.CursorState{}
		}
		at = at.Sub(c.Min)
	}
	return pipewire.CursorState{
		Visible: true,
		X:       intconv.Clamp[int32](at.X), Y: intconv.Clamp[int32](at.Y),
		HotspotX: intconv.Clamp[int32](s.HotspotX), HotspotY: intconv.Clamp[int32](s.HotspotY),
		Shape: s.Shape,
	}
}

// CursorBitmap reads shape's image, whose layout is the state's while the
// state still numbers it: a shape's number is never reused.
func (p *castPointer) CursorBitmap(shape uint64, dst []byte, side int) (pipewire.CursorBitmap, bool) {
	s := p.cursor.State()
	if s.Shape != shape {
		return pipewire.CursorBitmap{}, false
	}
	w, h, ok := p.cursor.ReadImage(shape, dst, side, side)
	if !ok {
		return pipewire.CursorBitmap{}, false
	}
	return pipewire.CursorBitmap{
		Format: videoFormat(s.Format),
		Width:  intconv.Clamp[uint32](w), Height: intconv.Clamp[uint32](h),
		Stride: intconv.Clamp[uint32](w * s.Format.BytesPerPixel()),
	}, true
}

func (p *castPointer) NotifyCursor(wake func()) { p.cursor.OnChange(wake) }

func (p *castPointer) close() { _ = p.cursor.Close() }

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
	return intconv.Clamp[uint32](s.width), intconv.Clamp[uint32](s.height), intconv.Clamp[uint32](s.stride)
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
func startSnapshotStream(target sharepicker.Target, cur castCursor, fps uint32) (screenStream, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to wayland: %w", err)
	}
	var grab func([]byte) (*capture.Frame, error)
	var gone func() bool
	var refresh int32
	var transform capture.Transform
	switch target.Kind {
	case sharepicker.TargetOutput, sharepicker.TargetRegion:
		o, ok := findOutput(c, target.Name)
		if !ok {
			_ = c.Close()
			return nil, fmt.Errorf("output '%s' is gone", target.Name)
		}
		gone = outputGone(c, target.Name)
		refresh, transform = o.RefreshMHz, o.Transform
		grab = outputGrab(c, o, regionOf(target), cur.paint)
	case sharepicker.TargetWindow:
		t, ok := findToplevel(c, target.Name)
		if !ok {
			_ = c.Close()
			return nil, fmt.Errorf("window '%s' is gone", target.Name)
		}
		grab = toplevelGrab(c, t, cur.paint)
		gone = toplevelGone(c, target.Name)
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
	}, src, cur.pointer, watch.ended, func() { _ = c.Close() })
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

// toplevelGone reports, on each call, whether the window closed.
func toplevelGone(c *capture.Client, id string) func() bool {
	return func() bool { _, ok := findToplevel(c, id); return !ok }
}

// toplevelGrab captures a window per call, into dst when it fits.
func toplevelGrab(c *capture.Client, t capture.Toplevel, cursor bool) func(dst []byte) (*capture.Frame, error) {
	opts := capture.Options{Cursor: cursor}
	return func(dst []byte) (*capture.Frame, error) { opts.Dst = dst; return c.CaptureToplevel(t, opts) }
}

// regionOf is a region target's rectangle of its output; nil for a whole
// output.
func regionOf(t sharepicker.Target) *image.Rectangle {
	if t.Kind != sharepicker.TargetRegion {
		return nil
	}
	r := image.Rect(int(t.X), int(t.Y), int(t.X+t.Width), int(t.Y+t.Height))
	return &r
}

// outputGrab captures an output, or the region of it when one is given, per
// call, into dst when it fits.
func outputGrab(c *capture.Client, o capture.Output, region *image.Rectangle, cursor bool) func(dst []byte) (*capture.Frame, error) {
	opts := capture.Options{Cursor: cursor}
	if region == nil {
		return func(dst []byte) (*capture.Frame, error) { opts.Dst = dst; return c.CaptureOutput(o, opts) }
	}
	return func(dst []byte) (*capture.Frame, error) {
		opts.Dst = dst
		return c.CaptureOutputRegion(o, *region, opts)
	}
}

// windowDmabufFormat is the dmabuf layout a capture session on t
// prefers, read from a session opened for it and closed again: the
// stream opens its own only once a consumer takes the GPU buffers.
func windowDmabufFormat(c *capture.Client, t capture.Toplevel, cursor bool) (capture.DmabufFormat, error) {
	session, err := c.OpenDmabufSession(t, cursor)
	if err != nil {
		return capture.DmabufFormat{}, err
	}
	formats, err := session.Formats()
	if err := errors.Join(err, session.Close()); err != nil {
		return capture.DmabufFormat{}, err
	}
	if len(formats) == 0 {
		return capture.DmabufFormat{}, errors.New("the window's capture session offers no dmabuf format")
	}
	return formats[0], nil
}

// connectForDmabuf connects a zero-copy stream's capture client, and
// its fail closes the client and marks any error errNoDmabuf, so the
// caller takes the shared-memory paths.
func connectForDmabuf() (*capture.Client, func(error) (screenStream, error), error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errNoDmabuf, err)
	}
	return c, func(err error) (screenStream, error) {
		_ = c.Close()
		if errors.Is(err, errNoDmabuf) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errNoDmabuf, err)
	}, nil
}

// deviceLayouts opens the device the compositor names for offered and
// lists the layouts to probe, the compositor's modifiers the device
// renders or exports.
func (st screenStreams) deviceLayouts(offered capture.DmabufFormat) (gpuAllocator, []dmabufprobe.Layout, error) {
	dev, err := st.openDevice(offered.Device)
	if err != nil {
		return nil, nil, err
	}
	rendered, err := dev.Modifiers(offered.Fourcc)
	if err != nil {
		dev.Close()
		return nil, nil, err
	}
	exported, err := dev.ExportModifiers(offered.Fourcc)
	if err != nil {
		dev.Close()
		return nil, nil, err
	}
	return dev, dmabufprobe.Layouts(offered.Modifiers, rendered, exported), nil
}

// startGPU streams an output or a region of one zero-copy when the
// compositor and the GPU allow (screencast_dmabuf.go); errNoDmabuf otherwise,
// and the caller takes the shared-memory paths.
func (st screenStreams) startGPU(target sharepicker.Target, cur castCursor, fps uint32) (screenStream, error) {
	c, fail, err := connectForDmabuf()
	if err != nil {
		return nil, err
	}
	output := target.Name
	o, ok := findOutput(c, output)
	if !ok {
		return fail(fmt.Errorf("output '%s' is gone", output))
	}
	region := regionOf(target)
	var offered capture.DmabufFormat
	if region != nil {
		offered, err = c.DmabufRegionFormat(o, *region, cur.paint)
	} else {
		offered, err = c.DmabufFormat(o, cur.paint)
	}
	if err != nil {
		return fail(err)
	}
	grab := outputGrab(c, o, region, cur.paint)
	first, err := grab(nil)
	if err != nil {
		return fail(err)
	}
	dev, layouts, err := st.deviceLayouts(offered)
	if err != nil {
		return fail(err)
	}
	watch := newVanishWatch(outputGone(c, output))
	shm := newSnapshotSource(grab, first, watch)
	src, err := probeGPUSource(shm, dev, screencopy{c: c, region: region}, offered, layouts, o, cur.paint, first)
	if err != nil {
		dev.Close()
		return fail(err)
	}
	src.watch = watch
	return startProducer(producerGeometry{
		width: first.Width, height: first.Height, stride: first.Stride, format: first.Format,
		fps: effectiveFPS(fps, o.RefreshMHz), transform: uint32(o.Transform),
	}, src, cur.pointer, watch.ended, func() {
		dev.Close()
		_ = c.Close()
	})
}

// startWindowGPU streams a window zero-copy over a capture session of
// its own (screencast_window.go) when the compositor and the GPU allow;
// errNoDmabuf otherwise, and the caller takes the shared-memory path.
func (st screenStreams) startWindowGPU(id string, cur castCursor, fps uint32) (screenStream, error) {
	c, fail, err := connectForDmabuf()
	if err != nil {
		return nil, err
	}
	t, ok := findToplevel(c, id)
	if !ok {
		return fail(fmt.Errorf("window '%s' is gone", id))
	}
	grab := toplevelGrab(c, t, cur.paint)
	first, err := grab(nil)
	if err != nil {
		return fail(err)
	}
	offered, err := windowDmabufFormat(c, t, cur.paint)
	if err != nil {
		return fail(err)
	}
	dev, layouts, err := st.deviceLayouts(offered)
	if err != nil {
		return fail(err)
	}
	watch := newVanishWatch(toplevelGone(c, id))
	ws := &windowSession{c: c, t: t, cursor: cur.paint}
	src, err := newWindowGPUSource(newSnapshotSource(grab, first, watch), dev, ws, offered, layouts)
	if err != nil {
		dev.Close()
		return fail(err)
	}
	return startProducer(producerGeometry{
		width: first.Width, height: first.Height, stride: first.Stride, format: first.Format,
		fps: effectiveFPS(fps, 0),
	}, src, cur.pointer, watch.ended, func() {
		src.Close()
		dev.Close()
		_ = c.Close()
	})
}
