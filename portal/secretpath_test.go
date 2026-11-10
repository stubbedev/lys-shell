package portal

import (
	"path/filepath"
	"testing"
)

// The legacy secret file is under the XDG data directory: XDG_DATA_HOME
// where it is an absolute path, else ~/.local/share; with neither
// absolute there is no path, rather than one relative to the working
// directory.
func TestSecretPathFollowsXDG(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("HOME", home)
	if got, err := secretPath("org.app"); err != nil || got != filepath.Join(data, "lys-shell", "portal", "secrets", "org.app") {
		t.Errorf("with XDG_DATA_HOME: %q (%v)", got, err)
	}
	t.Setenv("XDG_DATA_HOME", "relative")
	if got, err := secretPath("org.app"); err != nil || got != filepath.Join(home, ".local", "share", "lys-shell", "portal", "secrets", "org.app") {
		t.Errorf("with a relative XDG_DATA_HOME: %q (%v), want it under the home", got, err)
	}
	for _, home := range []string{"", "relative/home"} {
		t.Setenv("HOME", home)
		if got, err := secretPath("org.app"); err == nil {
			t.Errorf("HOME %q and no data home: %q, want an error", home, got)
		}
	}
}
