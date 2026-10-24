// Package sockdir gives tests a directory short enough to hold Unix
// sockets. sun_path holds 108 bytes, and t.TempDir() nests the test's
// name under TMPDIR, which the flake dev shell puts at
// $RUNNER_TEMP/nix-shell.XXXXXX on CI: a socket bound there overflows
// and the bind fails with EINVAL.
package sockdir

import (
	"os"
	"testing"
)

// Dir creates a fresh directory directly under TMPDIR and removes it
// when the test ends.
func Dir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ls")
	if err != nil {
		t.Fatalf("sockdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("sockdir: remove %s: %v", dir, err)
		}
	})
	return dir
}
