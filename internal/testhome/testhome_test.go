package testhome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatePointsEveryUserDirUnderHome(t *testing.T) {
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, "/real/"+k)
	}
	home := t.TempDir()
	if err := Isolate(home); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": home + "/config", "XDG_DATA_HOME": home + "/data",
		"XDG_STATE_HOME": home + "/state", "XDG_CACHE_HOME": home + "/cache", "XDG_RUNTIME_DIR": home + "/run",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if info, err := os.Stat(filepath.Join(home, "run")); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("runtime dir = %v, %v; want a 0700 directory", info, err)
	}
}

func TestIsolateFailsWhereTheRuntimeDirCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")
	if err := Isolate(file); err == nil {
		t.Error("a home that is a file isolated without error")
	}
}
