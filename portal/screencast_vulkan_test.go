package portal

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabuf"
	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/swaytest"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// hostRenderNodes are the host's DRM render nodes.
const hostRenderNodes = "/dev/dri/renderD*"

// firstRenderNode is the host's first render node; a host without one
// (CI, which renders on lavapipe) skips the test.
func firstRenderNode(t *testing.T) string {
	t.Helper()
	nodes, err := filepath.Glob(hostRenderNodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Skipf("no DRM render node matches %s: the zero-copy path needs a GPU (CI renders on lavapipe, which has none)", hostRenderNodes)
	}
	return nodes[0]
}

// deviceOf is the DRM device number of node.
func deviceOf(t *testing.T, node string) uint64 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Stat(node, &st); err != nil {
		t.Fatal(err)
	}
	return st.Rdev
}

// vulkanNode is the host's first render node and the Vulkan allocator
// behind it. A host with no render node, or whose Vulkan drivers have no
// device on it (CI, or Vulkan limited to lavapipe, which has no DRM
// node), skips the test with the reason; any other failure to open it
// fails.
func vulkanNode(t *testing.T) (string, gpuAllocator) {
	t.Helper()
	node := firstRenderNode(t)
	a, err := openVulkanAllocator(deviceOf(t, node))
	switch {
	case errors.Is(err, gpu.ErrNoVulkan), errors.Is(err, gpu.ErrNoDevice):
		t.Skipf("no Vulkan device behind %s: %v", node, err)
	case err != nil:
		t.Fatalf("%s has a Vulkan device that fails to open: %v", node, err)
	}
	t.Cleanup(a.Close)
	return node, a
}

// realDevice opens the Vulkan allocator on the host's first render node.
func realDevice(t *testing.T) gpuAllocator {
	t.Helper()
	_, a := vulkanNode(t)
	return a
}

// isDmabuf reports whether fd is an open dma-buf.
func isDmabuf(fd int) bool {
	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	return err == nil && strings.HasPrefix(target, "/dmabuf:")
}

// On the host's GPU the stream's buffers are linear XRGB8888 dmabufs,
// the layout every consumer imports, even where the device renders XR24
// only tiled (NVIDIA): the compositor's screencopy imports the probe,
// the PipeWire side gets one of the same layout, and releasing either
// closes its fd.
func TestVulkanBuffersAreLinearXRGBDmabufs(t *testing.T) {
	dev, cp := realDevice(t), &fakeCopy{}
	mods, err := dev.ExportModifiers(dmabuf.FormatXRGB8888)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := dev.Modifiers(dmabuf.FormatXRGB8888)
	if err != nil {
		t.Fatal(err)
	}
	src, err := probeGPUSource(nil, dev, cp, offer(), streamLayouts(mods, rendered, mods), capture.Output{}, false, shmFrame)
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
	if probe.Modifier != dmabuf.ModifierLinear {
		t.Errorf("the probe is in modifier %#x, want linear among the %#x the device exports XR24 in", probe.Modifier, mods)
	}
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
	_, err := probeGPUSource(nil, dev, cp, offer(), []streamLayout{{modifier: dmabuf.ModifierInvalid, render: true}}, capture.Output{}, false, shmFrame)
	if !errors.Is(err, errNoDmabuf) || !strings.Contains(err.Error(), "none of the modifiers") {
		t.Fatalf("err = %v, want errNoDmabuf naming the modifier lists", err)
	}
	if len(cp.got) != 0 {
		t.Errorf("screencopy imported %d buffers", len(cp.got))
	}
}

// A DRM device no Vulkan device is behind is an error naming it.
func TestADeviceWithoutVulkanOpensNoAllocator(t *testing.T) {
	_, err := openVulkanAllocator(unix.Mkdev(1, 3))
	if err == nil || !strings.Contains(err.Error(), "DRM device 1:3") {
		t.Errorf("/dev/null's device number: %v, want an error naming DRM device 1:3", err)
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
// render node: the compositor names that device and the modifiers it
// imports, and an output streams zero-copy without a word; with no
// Vulkan device behind the named one it streams through shared memory,
// and says so once, with the reason.
func TestOutputStreamTakesTheGPUOrSaysWhyNot(t *testing.T) {
	node, _ := vulkanNode(t)
	swaytest.Start(t, "WLR_RENDERER=vulkan", "WLR_RENDER_DRM_DEVICE="+node)
	pwtest.Start(t)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	outs := c.Outputs()
	if len(outs) == 0 {
		t.Fatal("the headless sway has no output")
	}
	output := sharepicker.Target{Kind: sharepicker.TargetOutput, Name: outs[0].Name}
	offered, err := c.DmabufFormat(outs[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if offered.Device != deviceOf(t, node) || len(offered.Modifiers) == 0 {
		t.Errorf("sway offers device %#x and modifiers %#x, want %s's and at least one", offered.Device, offered.Modifiers, node)
	}
	_ = c.Close()

	logged := captureLog(t)
	streams := screenStreams{openDevice: openVulkanAllocator}
	s, err := streams.startGPU(output.Name, false, 30)
	if err != nil {
		t.Fatalf("the zero-copy path declined: %v", err)
	}
	s.Close()
	s, err = streams.start(output, false, 30)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := logged.String(); got != "" {
		t.Errorf("a zero-copy stream logged %q", got)
	}

	noVulkan := errors.New("no Vulkan device")
	s, err = screenStreams{openDevice: func(uint64) (gpuAllocator, error) { return nil, noVulkan }}.start(output, false, 30)
	if err != nil {
		t.Fatalf("no Vulkan device failed the stream instead of falling back: %v", err)
	}
	defer s.Close()
	if w, h := s.Size(); w <= 0 || h <= 0 || s.NodeID() == 0 {
		t.Errorf("the shm stream is %dx%d on node %d", w, h, s.NodeID())
	}
	want := "portal: screencast: output " + output.Name + " streams through shared memory: screencast: no dmabuf path: no Vulkan device\n"
	if got := logged.String(); got != want {
		t.Errorf("logged %q, want once %q", got, want)
	}
}
