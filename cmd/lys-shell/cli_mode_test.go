package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIModeLogsToFileAndEnsuresConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	// A handler run (it fails to reach a shell, which is fine) sets up
	// the file log and the config directory.
	_, _, code := runCaptured(t, false, "widget", "update", "a", "b")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if info, err := os.Stat(filepath.Join(home, "config", "lys-shell")); err != nil || !info.IsDir() {
		t.Errorf("config dir: %v", err)
	}
	logs, _ := filepath.Glob(filepath.Join(home, "state", "lys-shell", cliLogPrefix+".*.log"))
	if len(logs) != 1 {
		t.Errorf("CLI log files = %v", logs)
	}
	if shell, _ := filepath.Glob(filepath.Join(home, "state", "lys-shell", shellLogPrefix+".*.log")); len(shell) != 0 {
		t.Errorf("the CLI wrote the shell's log %v: the two would prune each other", shell)
	}

	// Parse errors, help, and completions run no handler setup.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "other"))
	runCaptured(t, false, "widget", "--help")
	runCaptured(t, false, "completions", "bash")
	if _, err := os.Stat(filepath.Join(home, "other")); !os.IsNotExist(err) {
		t.Errorf("setup ran without a CLI handler: %v", err)
	}
}
