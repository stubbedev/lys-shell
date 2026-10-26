// Package gpuhost finds the host GPU the shell's GPU tests run on,
// under lys-lib's gputest convention: with LYS_TEST_GPU set (a check
// run on purpose, such as `just recorder-check`), a missing GPU fails
// the test; unset, the test skips where there is none (CI).
package gpuhost

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-lib/gputest"
	"golang.org/x/sys/unix"
)

// renderNodes are the host's DRM render nodes.
const renderNodes = "/dev/dri/renderD*"

// Node is the host's first render node and its device number, with the
// Vulkan loader set up as LYS_TEST_GPU asks. No render node is Absent.
func Node(t testing.TB) (path string, dev uint64) {
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
	return nodes[0], st.Rdev
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
