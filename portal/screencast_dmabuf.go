package portal

import (
	"errors"
	"fmt"
	"sync"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabuf"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/fourcc"
	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/pipewire"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/intconv"
)

// The zero-copy path: a whole output's screencopy frames land straight in
// buffers lys-lib's Vulkan device allocates on the DRM device the
// compositor names, in a modifier it imports, one bound to each PipeWire
// buffer for the stream's life, which the consumer imports as dmabufs.
// A compositor that names no device or modifier, no Vulkan device behind
// it, a format offered only as shm, no modifier both sides take, an
// allocation or copy the compositor refuses: the stream stays on shared
// memory (errNoDmabuf, logged by screenStreams.start).

// gpuAllocator is the device the source allocates from.
type gpuAllocator interface {
	Modifiers(format fourcc.Format) ([]fourcc.Modifier, error)
	ExportModifiers(format fourcc.Format) ([]fourcc.Modifier, error)
	NewTarget(width, height int, format fourcc.Format, modifiers []fourcc.Modifier) (gpuAllocation, error)
	NewBuffer(width, height int, format fourcc.Format, modifiers []fourcc.Modifier) (gpuAllocation, error)
	Close()
}

// gpuAllocation is one allocated buffer; its plane fds are open until Close.
type gpuAllocation interface {
	Dmabuf() dmabuf.Buffer
	Close()
}

// vulkanAllocator is a gpu.Device on the compositor's DRM device. A
// device belongs to one goroutine at a time, and the PipeWire loop
// allocates and releases buffers on its own thread while the stream
// opens and closes it on another: mu serializes every call.
type vulkanAllocator struct {
	mu   sync.Mutex
	dev  *gpu.Device
	node string
}

// openVulkanAllocator opens the Vulkan device behind the DRM device
// numbered dev, the one the compositor imports buffers on.
func openVulkanAllocator(dev uint64) (gpuAllocator, error) {
	node := fmt.Sprintf("DRM device %d:%d", unix.Major(dev), unix.Minor(dev))
	d, err := gpu.OpenNode(dev)
	if err != nil {
		return nil, fmt.Errorf("screencast: open the Vulkan device behind %s: %w", node, err)
	}
	return &vulkanAllocator{dev: d, node: node}, nil
}

func (a *vulkanAllocator) Modifiers(format fourcc.Format) ([]fourcc.Modifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mods, err := a.dev.Modifiers(format)
	if err != nil {
		return nil, fmt.Errorf("screencast: the modifiers %s renders %v in: %w", a.node, format, err)
	}
	return mods, nil
}

func (a *vulkanAllocator) ExportModifiers(format fourcc.Format) ([]fourcc.Modifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mods, err := a.dev.ExportModifiers(format)
	if err != nil {
		return nil, fmt.Errorf("screencast: the modifiers %s exports %v in: %w", a.node, format, err)
	}
	return mods, nil
}

func (a *vulkanAllocator) NewTarget(width, height int, format fourcc.Format, modifiers []fourcc.Modifier) (gpuAllocation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.dev.NewTarget(width, height, format, modifiers)
	if err != nil {
		return nil, fmt.Errorf("screencast: allocate a %dx%d %v render target on %s: %w", width, height, format, a.node, err)
	}
	return &vulkanAllocation{a: a, b: t}, nil
}

func (a *vulkanAllocator) NewBuffer(width, height int, format fourcc.Format, modifiers []fourcc.Modifier) (gpuAllocation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := a.dev.NewBuffer(width, height, format, modifiers)
	if err != nil {
		return nil, fmt.Errorf("screencast: allocate a %dx%d %v buffer on %s: %w", width, height, format, a.node, err)
	}
	return &vulkanAllocation{a: a, b: b}, nil
}

// Close frees the device and any buffer still open on it.
func (a *vulkanAllocator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dev.Close()
}

// vulkanAllocation is a gpu.Target or gpu.Buffer, used under its
// device's lock.
type vulkanAllocation struct {
	a *vulkanAllocator
	b gpuAllocation
}

func (v *vulkanAllocation) Dmabuf() dmabuf.Buffer {
	v.a.mu.Lock()
	defer v.a.mu.Unlock()
	return v.b.Dmabuf()
}

func (v *vulkanAllocation) Close() {
	v.a.mu.Lock()
	defer v.a.mu.Unlock()
	v.b.Close()
}

// gpuCapturer is the capture connection the source copies with.
type gpuCapturer interface {
	Import(dmabuf.Buffer) (importedBuffer, error)
	CopyInto(o capture.Output, cursor bool, buf importedBuffer) error
}

// importedBuffer is the compositor's import of one buffer.
type importedBuffer interface{ Destroy() }

// screencopy is the gpuCapturer over a capture client.
type screencopy struct{ c *capture.Client }

func (s screencopy) Import(d dmabuf.Buffer) (importedBuffer, error) { return s.c.ImportDmabuf(d) }

// CopyInto takes the next frame without waiting for damage: the stream
// fills at its own rate, and an idle output must not stall the loop.
func (s screencopy) CopyInto(o capture.Output, cursor bool, buf importedBuffer) error {
	b, ok := buf.(*capture.DmabufBuffer)
	if !ok {
		return errors.New("screencast: a buffer this client did not import")
	}
	return s.c.CopyOutputDmabuf(o, cursor, b)
}

// gpuSource is a pipewire.DmabufSource over screencopy into GPU
// buffers; a consumer that takes only shared memory is fed by the shm
// source.
type gpuSource struct {
	pipewire.Source // the shm fallback
	dev             gpuAllocator
	cap             gpuCapturer
	output          capture.Output
	cursor          bool
	format          fourcc.Format
	video           pipewire.VideoFormat
	width, height   uint32
	modifier        fourcc.Modifier
	render          bool
	// watch ends the stream when a failed copy finds the output gone.
	watch *vanishWatch
}

// gpuBuffer is one bound buffer: the allocation, its single plane and
// modifier, and the compositor's import of it.
type gpuBuffer struct {
	alloc    gpuAllocation
	plane    dmabuf.Plane
	modifier fourcc.Modifier
	imported importedBuffer
}

func (b *gpuBuffer) Fd() int        { return b.plane.FD }
func (b *gpuBuffer) Offset() uint32 { return b.plane.Offset }
func (b *gpuBuffer) Stride() uint32 { return b.plane.Stride }

func (b *gpuBuffer) Release() {
	b.imported.Destroy()
	b.alloc.Close()
}

func (s *gpuSource) Modifier() fourcc.Modifier { return s.modifier }

func (s *gpuSource) DmabufVideoFormat() pipewire.VideoFormat { return s.video }

// AllocDmabuf allocates and imports one buffer at the probed modifier.
func (s *gpuSource) AllocDmabuf() (pipewire.Dmabuf, error) {
	return s.alloc(dmabufprobe.Layout{Modifier: s.modifier, Render: s.render})
}

func (s *gpuSource) alloc(layout dmabufprobe.Layout) (*gpuBuffer, error) {
	allocate := s.dev.NewBuffer
	if layout.Render {
		allocate = s.dev.NewTarget
	}
	t, err := allocate(int(s.width), int(s.height), s.format, []fourcc.Modifier{layout.Modifier})
	if err != nil {
		return nil, err
	}
	d := t.Dmabuf()
	// Packed formats only: one plane is what the stream describes.
	if len(d.Planes) != 1 {
		t.Close()
		return nil, fmt.Errorf("screencast: a %d-plane buffer", len(d.Planes))
	}
	imported, err := s.cap.Import(d)
	if err != nil {
		t.Close()
		return nil, fmt.Errorf("screencast: import a %dx%d buffer of modifier %s: %w", d.Width, d.Height, d.Modifier, err)
	}
	return &gpuBuffer{alloc: t, plane: d.Planes[0], modifier: d.Modifier, imported: imported}, nil
}

// FillDmabuf screencopies the output into the bound buffer.
func (s *gpuSource) FillDmabuf(d pipewire.Dmabuf) ([]pipewire.Rect, bool) {
	b, ok := d.(*gpuBuffer)
	if !ok {
		return nil, false
	}
	// A plain copy reports no damage: the whole frame changed.
	if err := s.cap.CopyInto(s.output, s.cursor, b.imported); err != nil {
		if s.watch != nil {
			s.watch.failed()
		}
		return nil, false
	}
	return nil, true
}

// errNoDmabuf is a stream the zero-copy path does not take.
var errNoDmabuf = errors.New("screencast: no dmabuf path")

// probeGPUSource readies the zero-copy path for an output whose shm
// frames are first's: the compositor must offer the same size as a
// dmabuf, and a buffer in the first of layouts that allocates, imports
// and takes a frame sets the modifier the stream offers. Declining every
// layout names why each failed.
func probeGPUSource(shm pipewire.Source, dev gpuAllocator, c gpuCapturer, offered capture.DmabufFormat, layouts []dmabufprobe.Layout, o capture.Output, cursor bool, first *capture.Frame) (*gpuSource, error) {
	// The size must match the stream's; the format may differ (a GPU
	// compositor's shm is 24-bit, its dmabufs 32-bit) but must be one
	// packed 32-bit layout.
	format := capture.FormatFromFourcc(offered.Fourcc)
	if offered.Width != first.Width || offered.Height != first.Height || format.BytesPerPixel() != 4 {
		return nil, fmt.Errorf("%w: a %v %dx%d dmabuf for a %dx%d stream", errNoDmabuf, format, offered.Width, offered.Height, first.Width, first.Height)
	}
	width, errW := intconv.Exact[uint32](offered.Width)
	height, errH := intconv.Exact[uint32](offered.Height)
	if err := errors.Join(errW, errH); err != nil {
		return nil, fmt.Errorf("%w: a %dx%d dmabuf: %w", errNoDmabuf, offered.Width, offered.Height, err)
	}
	s := &gpuSource{
		Source: shm, dev: dev, cap: c, output: o, cursor: cursor,
		format: offered.Fourcc, video: videoFormat(format), width: width, height: height,
	}
	modifier, layout, err := dmabufprobe.First(layouts, s.probe)
	if err != nil {
		return nil, fmt.Errorf("%w: %v: %w", errNoDmabuf, format, err)
	}
	s.modifier, s.render = modifier, layout.Render
	return s, nil
}

// probe allocates a buffer in layout and screencopies into it,
// returning the modifier it was allocated in when the copy lands.
func (s *gpuSource) probe(layout dmabufprobe.Layout) (fourcc.Modifier, error) {
	probe, err := s.alloc(layout)
	if err != nil {
		return 0, err
	}
	defer probe.Release()
	if err := s.cap.CopyInto(s.output, s.cursor, probe.imported); err != nil {
		return 0, fmt.Errorf("screencopy into the probe buffer: %w", err)
	}
	return probe.modifier, nil
}
