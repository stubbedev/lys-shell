package gpuhost

import (
	"fmt"
	"testing"
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
