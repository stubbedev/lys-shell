package portal

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dmabufprobe"
	"github.com/stubbedev/lys-lib/fourcc"
	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/gpuhost"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
	"github.com/stubbedev/lys-shell/swaytest"
)

// vulkanNode is the host's first render node, its device number, and
// the Vulkan allocator behind it. No device there is gpuhost.Absent
// (gpuhost.Node); an allocator that then fails to open fails.
func vulkanNode(t *testing.T) (string, uint64, gpuAllocator) {
	t.Helper()
	node, dev, _ := gpuhost.Node(t)
	a, err := openVulkanAllocator(dev)
	if err != nil {
		t.Fatalf("%s has a Vulkan device that fails to open: %v", node, err)
	}
	t.Cleanup(a.Close)
	return node, dev, a
}

// realDevice opens the Vulkan allocator on the host's first render node.
func realDevice(t *testing.T) gpuAllocator {
	t.Helper()
	_, _, a := vulkanNode(t)
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
	mods, err := dev.ExportModifiers(fourcc.XRGB8888)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := dev.Modifiers(fourcc.XRGB8888)
	if err != nil {
		t.Fatal(err)
	}
	src, err := probeGPUSource(nil, dev, cp, offer(), dmabufprobe.Layouts(mods, rendered, mods), capture.Output{}, false, shmFrame)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.got) != 1 {
		t.Fatalf("screencopy imported %d buffers, want the probe", len(cp.got))
	}
	probe := cp.got[0]
	if probe.Format != fourcc.XRGB8888 || probe.Width != 64 || probe.Height != 32 || len(probe.Planes) != 1 || probe.Planes[0].Stride < 64*4 {
		t.Errorf("screencopy imported %+v, want one XR24 64x32 plane", probe)
	}
	if probe.Modifier != fourcc.Linear {
		t.Errorf("the probe is in modifier %s, want linear among the %v the device exports XR24 in", probe.Modifier, mods)
	}
	if isDmabuf(probe.Planes[0].FD) || cp.destroyed != 1 {
		t.Errorf("the probe outlived the probe: fd still a dmabuf %v, imports destroyed %d", isDmabuf(probe.Planes[0].FD), cp.destroyed)
	}
	if src.Modifier() != probe.Modifier {
		t.Errorf("the stream offers modifier %s, want the probe's %s", src.Modifier(), probe.Modifier)
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
	_, err := probeGPUSource(nil, dev, cp, offer(), []dmabufprobe.Layout{{Modifier: fourcc.Invalid, Render: true}}, capture.Output{}, false, shmFrame)
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
	node, dev, _ := vulkanNode(t)
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
	if offered.Device != dev || len(offered.Modifiers) == 0 {
		t.Errorf("sway offers device %#x and modifiers %v, want %s's and at least one", offered.Device, offered.Modifiers, node)
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
