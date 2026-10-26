// Package gpuhost finds the host GPU the shell's GPU tests run on,
// under lys-lib's gputest convention: with LYS_TEST_GPU set (a check
// run on purpose, such as `just recorder-check`), a missing GPU fails
// the test; unset, the test skips where there is none (CI).
package gpuhost

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/gputest"
	"golang.org/x/sys/unix"
)

// renderNodes are the host's DRM render nodes.
const renderNodes = "/dev/dri/renderD*"

// held is the Vulkan device behind the host's first render node, open
// for the rest of the test process as the shell's renderer holds its
// own for the shell's life. The Vulkan loader unloads the drivers when
// the last device closes and loads them again with the next, and a
// driver whose libraries take static TLS (NVIDIA's libnvidia-tls)
// strands a block of it on each reload until the driver no longer
// loads and only llvmpipe is left.
var held struct {
	once sync.Once
	dev  *gpu.Device
	err  error
}

// Node is the host's first render node, its device number, and the
// Vulkan device behind it, with the Vulkan loader set up as LYS_TEST_GPU
// asks. The device is shared by every test in the process: the test
// uses it and never closes it. No render node, or no Vulkan device
// behind it (CI, or Vulkan limited to lavapipe, which has no DRM node),
// is Absent; any other failure to open it fails.
func Node(t testing.TB) (path string, dev uint64, d *gpu.Device) {
	t.Helper()
	if _, err := gputest.SetupTest(t); err != nil {
		t.Fatal(err)
	}
	nodes, err := filepath.Glob(renderNodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		Absent(t, "no DRM render node matches %s", renderNodes)
	}
	var st unix.Stat_t
	if err := unix.Stat(nodes[0], &st); err != nil {
		t.Fatalf("stat %s: %v", nodes[0], err)
	}
	held.once.Do(func() { held.dev, held.err = gpu.OpenNode(st.Rdev) })
	switch {
	case errors.Is(held.err, gpu.ErrNoVulkan), errors.Is(held.err, gpu.ErrNoDevice):
		Absent(t, "no Vulkan device behind %s: %v", nodes[0], held.err)
	case held.err != nil:
		t.Fatalf("%s has a Vulkan device that fails to open: %v", nodes[0], held.err)
	}
	return nodes[0], st.Rdev, held.dev
}

// Absent ends t for want of the GPU it needs: it fails when
// LYS_TEST_GPU asks for the GPU tests and skips otherwise, either way
// with the reason.
func Absent(t testing.TB, format string, args ...any) {
	t.Helper()
	reason := fmt.Sprintf(format, args...)
	if gputest.Enabled() {
		t.Fatalf("LYS_TEST_GPU is set, but %s", reason)
	} else {
		t.Skip(reason)
	}
}
