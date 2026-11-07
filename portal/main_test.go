package portal

import (
	"os"
	"testing"
	"time"
)

// TestMain shortens the wait for an absent shell, which most tests that
// run without one only pass through.
func TestMain(m *testing.M) {
	shellAppearBound = 200 * time.Millisecond
	os.Exit(m.Run())
}

// withShellAppearBound sets the wait for an absent shell for one test.
func withShellAppearBound(t *testing.T, bound time.Duration) {
	t.Helper()
	old := shellAppearBound
	shellAppearBound = bound
	t.Cleanup(func() { shellAppearBound = old })
}
