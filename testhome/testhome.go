// Package testhome runs a test binary against a scratch home, so no
// test (or a daemon it starts) reads or writes the real user's files:
// the config the shell scaffolds and loads, the notification history,
// the persisted state.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Run points HOME and every XDG per-user directory at a fresh temp
// directory, runs the tests, removes it, and exits with their code.
// Call it from TestMain.
func Run(m *testing.M) {
	home, err := os.MkdirTemp("", "lys-shell-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		os.Exit(1)
	}
	code := run(m, home)
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func run(m *testing.M, home string) int {
	if err := Isolate(home); err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		return 1
	}
	return m.Run()
}

// Isolate points HOME and the XDG per-user directories under home, pins
// the sound server to the scratch runtime directory's socket, which only
// a test's own fake serves, and creates the runtime directory, which must
// exist with mode 0700.
func Isolate(home string) error {
	for key, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"),
		"XDG_DATA_HOME":   filepath.Join(home, "data"),
		"XDG_STATE_HOME":  filepath.Join(home, "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, "cache"),
		"XDG_RUNTIME_DIR": filepath.Join(home, "run"),
		"PULSE_SERVER":    "unix:" + filepath.Join(home, "run", "pulse", "native"),
	} {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		return fmt.Errorf("create the runtime dir: %w", err)
	}
	return nil
}
