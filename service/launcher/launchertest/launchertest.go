// Package launchertest sets up what the launcher's tests run against.
package launchertest

import (
	"os"
	"path/filepath"
	"testing"
)

// OnlyOnPath leaves the test a $PATH holding nothing but programs, each
// an executable that does nothing, and no $TERMINAL: the only terminal
// emulator to detect is one programs names.
func OnlyOnPath(t *testing.T, programs ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range programs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // a fake program must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("TERMINAL", "")
}
