package portal

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/capture"
	"github.com/stubbedev/lys-ui/dmabuf"

	"github.com/stubbedev/lys-shell/internal/pipewire/pwtest"
	"github.com/stubbedev/lys-shell/internal/swaytest"
)

// firstRenderNode is the render node the stream allocates on; a host
// without one (CI, which renders on lavapipe) skips the test.
func firstRenderNode(t *testing.T) string {
	t.Helper()
	nodes, err := filepath.Glob(renderNodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Skipf("no DRM render node matches %s: the zero-copy path needs a GPU (CI renders on lavapipe, which has none)", renderNodes)
	}
	return nodes[0]
}

// realDevice opens the Vulkan allocator on the host's render node.
func realDevice(t *testing.T) *vulkanAllocator {
	t.Helper()
	firstRenderNode(t)
	a, err := openVulkanAllocator(renderNodes)
	if err != nil {
		t.Fatalf("a render node exists but opens no Vulkan device: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

// isDmabuf reports whether fd is an open dma-buf.
func isDmabuf(fd int) bool {
	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	return err == nil && strings.HasPrefix(target, "/dmabuf:")
}

// On the host's GPU the stream's buffers are XRGB8888 dmabufs, linear
// when the device renders XR24 linear and in its own tiling otherwise
// (NVIDIA renders XR24 only block-linear): the compositor's screencopy
// imports the probe, the PipeWire side gets one of the same layout, and
// releasing either closes its fd.
func TestVulkanBuffersAreXRGBDmabufsInTheStreamsLayout(t *testing.T) {
	dev, cp := realDevice(t), &fakeCopy{}
	mods, err := dev.Modifiers(dmabuf.FormatXRGB8888)
	if err != nil {
		t.Fatal(err)
	}
	src, err := probeGPUSource(nil, dev, cp, offer(), streamModifiers(mods), capture.Output{}, false, shmFrame)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.got) != 1 {
		t.Fatalf("screencopy imported %d buffers, want the probe", len(cp.got))
	}
	probe := cp.got[0]
	if probe.Format != dmabuf.FormatXRGB8888 || probe.Width != 64 || probe.Height != 32 || len(probe.Planes) != 1 || probe.Planes[0].Stride < 64*4 {
		t.Errorf("screencopy imported %+v, want one XR24 64x32 plane", probe)
	}
	if slices.Contains(mods, dmabuf.ModifierLinear) && probe.Modifier != dmabuf.ModifierLinear || !slices.Contains(mods, probe.Modifier) {
		t.Errorf("the probe is in modifier %#x, want linear if the device has it among %#x", probe.Modifier, mods)
	}
	t.Logf("%s renders XR24 in %#x; the stream takes %#x", dev.node, mods, probe.Modifier)
	if isDmabuf(probe.Planes[0].FD) || cp.destroyed != 1 {
		t.Errorf("the probe outlived the probe: fd still a dmabuf %v, imports destroyed %d", isDmabuf(probe.Planes[0].FD), cp.destroyed)
	}
	if src.Modifier() != probe.Modifier {
		t.Errorf("the stream offers modifier %#x, want the probe's %#x", src.Modifier(), probe.Modifier)
	}
	d, err := src.AllocDmabuf()
	if err != nil {
		t.Fatal(err)
	}
	fd := d.Fd()
	if !isDmabuf(fd) || d.Stride() != probe.Planes[0].Stride {
		t.Errorf("the PipeWire buffer: fd %d dmabuf %v stride %d, want a dmabuf of stride %d", fd, isDmabuf(fd), d.Stride(), probe.Planes[0].Stride)
	}
	if _, ok := src.FillDmabuf(d); !ok {
		t.Error("the bound buffer took no frame")
	}
	d.Release()
	if isDmabuf(fd) {
		t.Error("releasing the PipeWire buffer left its dmabuf open")
	}
}

// A device that renders XR24 in none of the offered modifiers (only the
// implicit layout, which Vulkan never allocates) declines the stream,
// naming both lists, with nothing left imported.
func TestVulkanDeclinesWithoutASharedModifier(t *testing.T) {
	dev, cp := realDevice(t), &fakeCopy{}
	_, err := probeGPUSource(nil, dev, cp, offer(), []uint64{dmabuf.ModifierInvalid}, capture.Output{}, false, shmFrame)
	if !errors.Is(err, errNoDmabuf) || !strings.Contains(err.Error(), "none of the modifiers") {
		t.Fatalf("err = %v, want errNoDmabuf naming the modifier lists", err)
	}
	if len(cp.got) != 0 {
		t.Errorf("screencopy imported %d buffers", len(cp.got))
	}
}

// No render node, or a node no Vulkan device is behind, is an error
// naming what was tried.
func TestNoRenderNodeOpensNoAllocator(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "renderD*")
	if _, err := openVulkanAllocator(empty); err == nil || !strings.Contains(err.Error(), "no DRM render node matches "+empty) {
		t.Errorf("an empty glob: %v", err)
	}
	if _, err := openVulkanAllocator("/dev/null"); err == nil || !strings.Contains(err.Error(), "/dev/null") {
		t.Errorf("a node no device is behind: %v", err)
	}
	if _, err := openVulkanAllocator("["); err == nil {
		t.Error("a malformed glob opened a device")
	}
}

// captureLog collects the process log for the test; the PipeWire
// thread may write to it.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return buf
}

// Against a private headless sway rendering with Vulkan on the host's
// render node: an output streams zero-copy without a word; with no
// render node to allocate on it streams through shared memory, and says
// so once, with the reason.
func TestOutputStreamTakesTheGPUOrSaysWhyNot(t *testing.T) {
	node := firstRenderNode(t)
	swaytest.Start(t, "WLR_RENDERER=vulkan", "WLR_RENDER_DRM_DEVICE="+node)
	pwtest.Start(t)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	outs := c.Outputs()
	_ = c.Close()
	if len(outs) == 0 {
		t.Fatal("the headless sway has no output")
	}
	output := captureTarget{kind: targetOutput, name: outs[0].Name}

	logged := captureLog(t)
	gpu := screenStreams{renderNodes: renderNodes}
	s, err := gpu.startGPU(output.name, false, 30)
	if err != nil {
		t.Fatalf("the zero-copy path declined: %v", err)
	}
	s.Close()
	s, err = gpu.start(output, false, 30)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := logged.String(); got != "" {
		t.Errorf("a zero-copy stream logged %q", got)
	}

	empty := filepath.Join(t.TempDir(), "renderD*")
	s, err = screenStreams{renderNodes: empty}.start(output, false, 30)
	if err != nil {
		t.Fatalf("no render node failed the stream instead of falling back: %v", err)
	}
	defer s.Close()
	if w, h := s.Size(); w <= 0 || h <= 0 || s.NodeID() == 0 {
		t.Errorf("the shm stream is %dx%d on node %d", w, h, s.NodeID())
	}
	want := "portal: screencast: output " + output.name + " streams through shared memory: screencast: no dmabuf path: screencast: no DRM render node matches " + empty + "\n"
	if got := logged.String(); got != want {
		t.Errorf("logged %q, want once %q", got, want)
	}
}
