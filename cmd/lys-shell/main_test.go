package main

import (
	"os"
	"testing"

	"github.com/stubbedev/lys-shell/testhome"
)

// TestMain points every per-user location at a scratch home so no
// test (or a daemon it starts, such as the notification service's
// persisted do-not-disturb flag) touches the real user's files.
func TestMain(m *testing.M) {
	// The launcher session tests re-exec the test binary as the CLI.
	if os.Getenv("LYS_SHELL_TEST_CLI") == "1" {
		main()
		os.Exit(0)
	}
	testhome.Run(m)
}
