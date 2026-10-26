package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/widget"
)

// The picker offers every icon the icon theme's search path holds in
// lys-shell's directory, the packaged ones under a data dir's icons
// included; icons elsewhere in a theme are not offered.
func TestIconNamesListTheIconSearchPath(t *testing.T) {
	user, packaged := t.TempDir(), t.TempDir()
	for root, name := range map[string]string{user: "si-gmail-symbolic", packaged: "ld-activity-symbolic"} {
		dir := filepath.Join(root, "hicolor", "scalable", "actions")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".svg"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	apps := filepath.Join(packaged, "hicolor", "scalable", "apps")
	if err := os.MkdirAll(apps, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apps, "firefox.svg"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	widget.SetIconSearchPaths([]string{user, packaged})
	t.Cleanup(func() { widget.SetIconSearchPaths(nil) })
	if got, want := iconNames(), []string{"ld-activity-symbolic", "si-gmail-symbolic"}; !slices.Equal(got, want) {
		t.Errorf("the picker offers %v, want %v", got, want)
	}
}
