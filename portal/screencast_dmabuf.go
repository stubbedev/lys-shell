package portal

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabuf"
	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/pipewire"
	"golang.org/x/sys/unix"
)

// The zero-copy path: a whole output's screencopy frames land straight in
// buffers lys-lib's Vulkan device allocates on the DRM device the
// compositor names, in a modifier it imports, one bound to each PipeWire
// buffer for the stream's life, which the consumer imports as dmabufs.
// A compositor that names no device or modifier, no Vulkan device behind
// it, a format offered only as shm, no modifier both sides take, an
// allocation or copy the compositor refuses: the stream stays on shared
// memory (errNoDmabuf, logged by screenStreams.start).

// streamLayout is one modifier the stream probes and how its buffers
// are allocated: for rendering into, when the device renders the
// modifier, or only for transfer and sampling, when it only exports it
// (linear on NVIDIA).
type streamLayout struct {
	modifier uint64
	render   bool
}

// streamLayouts orders the modifiers the stream probes, one at a time,
// among the ones the compositor imports, in its order: linear first, the
// layout every consumer imports, then the ones the device renders in,
// then the ones it only exports. One at a time, because a compositor may
// advertise a tiling it then fails to import (sway's Vulkan renderer on
// NVIDIA), and a set would leave the pick to the driver. A consumer on
// the same GPU imports the tilings; any other declines them for shared
// memory.
func streamLayouts(compositor, rendered, exported []uint64) []streamLayout {
	var linear, drawn, exportOnly []streamLayout
	for _, m := range compositor {
		l := streamLayout{modifier: m, render: slices.Contains(rendered, m)}
		switch {
		case !l.render && !slices.Contains(exported, m):
		case m == dmabuf.ModifierLinear:
			linear = append(linear, l)
		case l.render:
			drawn = append(drawn, l)
		default:
			exportOnly = append(exportOnly, l)
		}
	}
	return slices.Concat(linear, drawn, exportOnly)
}

// gpuAllocator is the device the source allocates from.
type gpuAllocator interface {
	Modifiers(fourcc uint32) ([]uint64, error)
	ExportModifiers(fourcc uint32) ([]uint64, error)
	NewTarget(width, height int, fourcc uint32, modifiers []uint64) (gpuAllocation, error)
	NewBuffer(width, height int, fourcc uint32, modifiers []uint64) (gpuAllocation, error)
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

func (a *vulkanAllocator) Modifiers(fourcc uint32) ([]uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mods, err := a.dev.Modifiers(fourcc)
	if err != nil {
		return nil, fmt.Errorf("screencast: the modifiers %s renders %v in: %w", a.node, capture.FormatFromFourcc(fourcc), err)
	}
	return mods, nil
}

func (a *vulkanAllocator) ExportModifiers(fourcc uint32) ([]uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mods, err := a.dev.ExportModifiers(fourcc)
	if err != nil {
		return nil, fmt.Errorf("screencast: the modifiers %s exports %v in: %w", a.node, capture.FormatFromFourcc(fourcc), err)
	}
	return mods, nil
}

func (a *vulkanAllocator) NewTarget(width, height int, fourcc uint32, modifiers []uint64) (gpuAllocation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.dev.NewTarget(width, height, fourcc, modifiers)
	if err != nil {
		return nil, fmt.Errorf("screencast: allocate a %dx%d %v render target on %s: %w", width, height, capture.FormatFromFourcc(fourcc), a.node, err)
	}
	return &vulkanAllocation{a: a, b: t}, nil
}

func (a *vulkanAllocator) NewBuffer(width, height int, fourcc uint32, modifiers []uint64) (gpuAllocation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := a.dev.NewBuffer(width, height, fourcc, modifiers)
	if err != nil {
		return nil, fmt.Errorf("screencast: allocate a %dx%d %v buffer on %s: %w", width, height, capture.FormatFromFourcc(fourcc), a.node, err)
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
	fourcc          uint32
	video           pipewire.VideoFormat
	width, height   uint32
	modifier        uint64
	render          bool
	// watch ends the stream when a failed copy finds the output gone.
	watch *vanishWatch
}

// gpuBuffer is one bound buffer: the allocation, its single plane and
// modifier, and the compositor's import of it.
type gpuBuffer struct {
	alloc    gpuAllocation
	plane    dmabuf.Plane
	modifier uint64
	imported importedBuffer
}

func (b *gpuBuffer) Fd() int        { return b.plane.FD }
func (b *gpuBuffer) Offset() uint32 { return b.plane.Offset }
func (b *gpuBuffer) Stride() uint32 { return b.plane.Stride }

func (b *gpuBuffer) Release() {
	b.imported.Destroy()
	b.alloc.Close()
}

func (s *gpuSource) Modifier() uint64 { return s.modifier }

func (s *gpuSource) DmabufVideoFormat() pipewire.VideoFormat { return s.video }

// AllocDmabuf allocates and imports one buffer at the probed modifier.
func (s *gpuSource) AllocDmabuf() (pipewire.Dmabuf, error) {
	return s.alloc(streamLayout{modifier: s.modifier, render: s.render})
}

func (s *gpuSource) alloc(layout streamLayout) (*gpuBuffer, error) {
	allocate := s.dev.NewBuffer
	if layout.render {
		allocate = s.dev.NewTarget
	}
	t, err := allocate(int(s.width), int(s.height), s.fourcc, []uint64{layout.modifier})
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
		return nil, fmt.Errorf("screencast: import a %dx%d buffer of modifier %#x: %w", d.Width, d.Height, d.Modifier, err)
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
func probeGPUSource(shm pipewire.Source, dev gpuAllocator, c gpuCapturer, offered capture.DmabufFormat, layouts []streamLayout, o capture.Output, cursor bool, first *capture.Frame) (*gpuSource, error) {
	// The size must match the stream's; the format may differ (a GPU
	// compositor's shm is 24-bit, its dmabufs 32-bit) but must be one
	// packed 32-bit layout.
	format := capture.FormatFromFourcc(offered.Fourcc)
	if offered.Width != first.Width || offered.Height != first.Height || format.BytesPerPixel() != 4 {
		return nil, fmt.Errorf("%w: a %v %dx%d dmabuf for a %dx%d stream", errNoDmabuf, format, offered.Width, offered.Height, first.Width, first.Height)
	}
	if len(layouts) == 0 {
		return nil, fmt.Errorf("%w: the compositor and the device share no modifier for %v", errNoDmabuf, format)
	}
	s := &gpuSource{
		Source: shm, dev: dev, cap: c, output: o, cursor: cursor,
		fourcc: offered.Fourcc, video: videoFormat(format), width: uint32(offered.Width), height: uint32(offered.Height),
	}
	declined := make([]error, 0, len(layouts))
	for _, layout := range layouts {
		modifier, err := s.probe(layout)
		if err == nil {
			s.modifier, s.render = modifier, layout.render
			return s, nil
		}
		declined = append(declined, fmt.Errorf("modifier %#x: %w", layout.modifier, err))
	}
	return nil, fmt.Errorf("%w: %w", errNoDmabuf, errors.Join(declined...))
}

// probe allocates a buffer in layout and screencopies into it,
// returning the modifier it was allocated in when the copy lands.
func (s *gpuSource) probe(layout streamLayout) (uint64, error) {
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
