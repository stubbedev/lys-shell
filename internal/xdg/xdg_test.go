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
