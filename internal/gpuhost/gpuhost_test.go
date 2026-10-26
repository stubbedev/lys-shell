package gpuhost

import (
	"fmt"
	"testing"

	"github.com/stubbedev/lys-lib/gpu"
)

// recorder is a testing.TB that records how a test ended instead of
// ending it.
type recorder struct {
	testing.TB
	failed, skipped string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) { r.failed = fmt.Sprintf(format, args...) }

func (r *recorder) Skip(args ...any) { r.skipped = fmt.Sprint(args...) }

// Asked for the GPU tests, a missing GPU fails with the reason; not
// asked, it skips with the reason and fails nothing.
func TestAbsentFailsOnlyWhenTheGPUIsAskedFor(t *testing.T) {
	t.Setenv("LYS_TEST_GPU", "host")
	asked := &recorder{TB: t}
	Absent(asked, "no device behind %s", "/dev/dri/renderD128")
	if asked.failed != "LYS_TEST_GPU is set, but no device behind /dev/dri/renderD128" || asked.skipped != "" {
		t.Errorf("asked for the GPU: failed %q, skipped %q", asked.failed, asked.skipped)
	}

	t.Setenv("LYS_TEST_GPU", "")
	unasked := &recorder{TB: t}
	Absent(unasked, "no device behind %s", "/dev/dri/renderD128")
	if unasked.skipped != "no device behind /dev/dri/renderD128" || unasked.failed != "" {
		t.Errorf("not asked: failed %q, skipped %q", unasked.failed, unasked.skipped)
	}
}

// reopens is how many devices a test process opens and closes on the
// node: more than NVIDIA's driver survives being unloaded and loaded
// again between them (17 in a container with no display server).
const reopens = 40

// With no device left open, every device opened and closed on the node
// one after another is the GPU behind it, however many come and go: the
// drivers stay loaded between them.
func TestDevicesReopenedOnTheNodeStayOnTheGPU(t *testing.T) {
	var dev uint64
	var gpuName string
	t.Run("node", func(t *testing.T) {
		var d *gpu.Device
		_, dev, d = Node(t)
		gpuName = d.Name()
	})
	if gpuName == "" {
		t.Skip("no GPU behind the render node")
	}
	for i := range reopens {
		d, err := gpu.OpenNode(dev)
		if err != nil {
			t.Fatalf("open %d of %d: %v", i+1, reopens, err)
		}
		name := d.Name()
		d.Close()
		if name != gpuName {
			t.Fatalf("open %d of %d landed on %s, want %s", i+1, reopens, name, gpuName)
		}
	}
}
