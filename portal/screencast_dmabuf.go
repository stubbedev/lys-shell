package portal

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/stubbedev/lys-ui/capture"
	"github.com/stubbedev/lys-ui/dmabuf"
	"github.com/stubbedev/lys-ui/gpu"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/pipewire"
)

// The zero-copy path: a whole output's screencopy frames land straight in
// buffers lys-ui's Vulkan device allocates on a DRM render node, one
// bound to each PipeWire buffer for the stream's life, which the
// consumer imports as dmabufs. No render node, no Vulkan device on it, a
// format the compositor offers only as shm, no modifier both sides
// take, an allocation or import the compositor refuses: the stream
// stays on shared memory (errNoDmabuf, logged by screenStreams.start).

// renderNodes names the DRM render nodes, tried in name order: the
// stream allocates on the first whose Vulkan device exports dmabufs.
const renderNodes = "/dev/dri/renderD*"

// streamModifiers picks the layouts the stream allocates in among the
// ones the device renders a format in: linear alone when it has it, the
// layout every consumer imports, else the device's own tilings, which a
// consumer on the same GPU imports and any other declines for shared
// memory.
func streamModifiers(device []uint64) []uint64 {
	if slices.Contains(device, dmabuf.ModifierLinear) {
		return []uint64{dmabuf.ModifierLinear}
	}
	return device
}

// gpuAllocator is the device the source allocates from.
type gpuAllocator interface {
	Modifiers(fourcc uint32) ([]uint64, error)
	NewTarget(width, height int, fourcc uint32, modifiers []uint64) (gpuTarget, error)
	Close()
}

// gpuTarget is one allocated buffer; its plane fds are open until Close.
type gpuTarget interface {
	Dmabuf() dmabuf.Buffer
	Close()
}

// vulkanAllocator is a gpu.Device on a render node. A device belongs to
// one goroutine at a time, and the PipeWire loop allocates and releases
// buffers on its own thread while the stream opens and closes it on
// another: mu serializes every call.
type vulkanAllocator struct {
	mu   sync.Mutex
	dev  *gpu.Device
	node string
}

// openVulkanAllocator opens the Vulkan device behind the first render
// node matching glob that has one exporting dmabufs.
func openVulkanAllocator(glob string) (*vulkanAllocator, error) {
	nodes, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("screencast: list render nodes %s: %w", glob, err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("screencast: no DRM render node matches %s", glob)
	}
	reasons := make([]error, 0, len(nodes))
	for _, node := range nodes {
		dev, err := openRenderNode(node)
		if err == nil {
			return &vulkanAllocator{dev: dev, node: node}, nil
		}
		reasons = append(reasons, err)
	}
	return nil, fmt.Errorf("screencast: no render node has a Vulkan device that exports dmabufs: %w", errors.Join(reasons...))
}

func openRenderNode(node string) (*gpu.Device, error) {
	var st unix.Stat_t
	if err := unix.Stat(node, &st); err != nil {
		return nil, fmt.Errorf("stat %s: %w", node, err)
	}
	dev, err := gpu.OpenNode(st.Rdev)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", node, err)
	}
	return dev, nil
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

func (a *vulkanAllocator) NewTarget(width, height int, fourcc uint32, modifiers []uint64) (gpuTarget, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, err := a.dev.NewTarget(width, height, fourcc, modifiers)
	if err != nil {
		return nil, fmt.Errorf("screencast: allocate a %dx%d %v buffer on %s: %w", width, height, capture.FormatFromFourcc(fourcc), a.node, err)
	}
	return &vulkanTarget{a: a, t: t}, nil
}

// Close frees the device and any buffer still open on it.
func (a *vulkanAllocator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dev.Close()
}

// vulkanTarget is a gpu.Target, used under its device's lock.
type vulkanTarget struct {
	a *vulkanAllocator
	t *gpu.Target
}

func (v *vulkanTarget) Dmabuf() dmabuf.Buffer {
	v.a.mu.Lock()
	defer v.a.mu.Unlock()
	return v.t.Dmabuf()
}

func (v *vulkanTarget) Close() {
	v.a.mu.Lock()
	defer v.a.mu.Unlock()
	v.t.Close()
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
	// watch ends the stream when a failed copy finds the output gone.
	watch *vanishWatch
}

// gpuBuffer is one bound buffer: the allocation, its single plane and
// modifier, and the compositor's import of it.
type gpuBuffer struct {
	target   gpuTarget
	plane    dmabuf.Plane
	modifier uint64
	imported importedBuffer
}

func (b *gpuBuffer) Fd() int        { return b.plane.FD }
func (b *gpuBuffer) Offset() uint32 { return b.plane.Offset }
func (b *gpuBuffer) Stride() uint32 { return b.plane.Stride }

func (b *gpuBuffer) Release() {
	b.imported.Destroy()
	b.target.Close()
}

func (s *gpuSource) Modifier() uint64 { return s.modifier }

func (s *gpuSource) DmabufVideoFormat() pipewire.VideoFormat { return s.video }

// AllocDmabuf allocates and imports one buffer at the probed modifier.
func (s *gpuSource) AllocDmabuf() (pipewire.Dmabuf, error) {
	return s.alloc([]uint64{s.modifier})
}

func (s *gpuSource) alloc(modifiers []uint64) (*gpuBuffer, error) {
	t, err := s.dev.NewTarget(int(s.width), int(s.height), s.fourcc, modifiers)
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
	return &gpuBuffer{target: t, plane: d.Planes[0], modifier: d.Modifier, imported: imported}, nil
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
// dmabuf, and one buffer in one of modifiers must allocate, import and
// take a frame; its modifier is the one the stream offers.
func probeGPUSource(shm pipewire.Source, dev gpuAllocator, c gpuCapturer, offered capture.DmabufFormat, modifiers []uint64, o capture.Output, cursor bool, first *capture.Frame) (*gpuSource, error) {
	// The size must match the stream's; the format may differ (a GPU
	// compositor's shm is 24-bit, its dmabufs 32-bit) but must be one
	// packed 32-bit layout.
	format := capture.FormatFromFourcc(offered.Fourcc)
	if offered.Width != first.Width || offered.Height != first.Height || format.BytesPerPixel() != 4 {
		return nil, fmt.Errorf("%w: a %v %dx%d dmabuf for a %dx%d stream", errNoDmabuf, format, offered.Width, offered.Height, first.Width, first.Height)
	}
	s := &gpuSource{
		Source: shm, dev: dev, cap: c, output: o, cursor: cursor,
		fourcc: offered.Fourcc, video: videoFormat(format), width: uint32(offered.Width), height: uint32(offered.Height),
	}
	probe, err := s.alloc(modifiers)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoDmabuf, err)
	}
	defer probe.Release()
	if err := c.CopyInto(o, cursor, probe.imported); err != nil {
		return nil, fmt.Errorf("%w: screencopy into the probe buffer: %w", errNoDmabuf, err)
	}
	s.modifier = probe.modifier
	return s, nil
}
