package gtksettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse("[Settings]\ngtk-theme-name = Adwaita-dark\ngtk-icon-theme-name=\"Papirus\"\ngtk-cursor-theme-name = Vimix\n" +
		"gtk-cursor-theme-size=22\ngtk-font-name = Inter 11\ngtk-xft-dpi=98304\nother = x\n")
	want := Settings{ThemeName: "Adwaita-dark", IconTheme: "Papirus", CursorTheme: "Vimix", CursorSize: 22, FontName: "Inter 11", XftDPI: 98304}
	if got != want {
		t.Errorf("parsed %+v, want %+v", got, want)
	}
	if got := Parse("gtk-cursor-theme-size = big\ngtk-xft-dpi = -1\ngtk-font-name =\n"); got != (Settings{}) {
		t.Errorf("malformed values set %+v", got)
	}
}

// gtk-4.0 wins; gtk-3.0 fills what it leaves unset; missing files set
// nothing.
func TestLoadLayersGTK4OverGTK3(t *testing.T) {
	dir := t.TempDir()
	if got := Load(dir); got != (Settings{}) {
		t.Errorf("no files = %+v", got)
	}
	write := func(version, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, version), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, version, "settings.ini"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("gtk-3.0", "gtk-icon-theme-name=Old\ngtk-font-name=Sans 9\n")
	write("gtk-4.0", "gtk-icon-theme-name=New\n")
	if got := Load(dir); got.IconTheme != "New" || got.FontName != "Sans 9" {
		t.Errorf("layered = %+v", got)
	}
}
