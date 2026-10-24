package xdg

import "testing"

// Each directory is lys-shell's under its variable, else under HOME's
// default; with neither there is none.
func TestBaseDirs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dir      func() (string, bool)
		env      string
		fallback string
	}{
		{"state", StateDir, "XDG_STATE_HOME", "/home/u/.local/state/lys-shell"},
		{"cache", CacheDir, "XDG_CACHE_HOME", "/home/u/.cache/lys-shell"},
		{"data", DataDir, "XDG_DATA_HOME", "/home/u/.local/share/lys-shell"},
	} {
		t.Setenv(tc.env, "/x")
		t.Setenv("HOME", "/home/u")
		if got, ok := tc.dir(); !ok || got != "/x/lys-shell" {
			t.Errorf("%s with %s = %q, %v", tc.name, tc.env, got, ok)
		}
		t.Setenv(tc.env, "")
		if got, ok := tc.dir(); !ok || got != tc.fallback {
			t.Errorf("%s under HOME = %q, %v", tc.name, got, ok)
		}
		t.Setenv("HOME", "")
		if got, ok := tc.dir(); ok {
			t.Errorf("%s with neither = %q", tc.name, got)
		}
	}
}

// The cache home is the variable's directory itself, else ~/.cache.
func TestCacheHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/c")
	if got, ok := CacheHome(); !ok || got != "/c" {
		t.Errorf("CacheHome = %q, %v", got, ok)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, ok := CacheHome(); !ok || got != "/home/u/.cache" {
		t.Errorf("CacheHome under HOME = %q, %v", got, ok)
	}
	t.Setenv("HOME", "")
	if _, ok := CacheHome(); ok {
		t.Error("a cache home with neither")
	}
}

// The data home is the variable's directory itself, else
// ~/.local/share.
func TestDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/d")
	if got, ok := DataHome(); !ok || got != "/d" {
		t.Errorf("DataHome = %q, %v", got, ok)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, ok := DataHome(); !ok || got != "/home/u/.local/share" {
		t.Errorf("DataHome under HOME = %q, %v", got, ok)
	}
	t.Setenv("HOME", "")
	if _, ok := DataHome(); ok {
		t.Error("a data home with neither")
	}
}
