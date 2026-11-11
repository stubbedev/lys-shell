package portal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/wallpaper/extract"
	"github.com/stubbedev/lys-ui/gtksettings"

	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// writeWallpaperPalette writes the palette an extraction leaves, with
// primary as the dark accent.
func writeWallpaperPalette(t *testing.T, primary string) {
	t.Helper()
	palette := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lys-shell", "wallpaper-colors.json")
	data := `{"colors": {` +
		`"background": {"dark": "#101418", "light": "#f8f9ff"},` +
		`"on_background": {"dark": "#e0e2e8", "light": "#191c20"},` +
		`"on_surface_variant": {"dark": "#c2c7cf", "light": "#42474e"},` +
		`"primary": {"dark": "` + primary + `", "light": "#2060cc"},` +
		`"secondary": {"dark": "#b9c8da", "light": "#526070"},` +
		`"tertiary": {"dark": "#d3bfe6", "light": "#6a5778"},` +
		`"error": {"dark": "#ffb4ab", "light": "#ba1a1a"}}}`
	if err := os.MkdirAll(filepath.Dir(palette), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(palette, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The portal takes ColorsExtracted only from the owner of the wallpaper
// service's name, at the service's object: the shell's own wallpaper
// service, started after the portal as a restarted shell is, announces
// a finished extraction that way, and the accent follows.
func TestTheAccentFollowsTheShellsOwnWallpaperService(t *testing.T) {
	r := settingsRig(t, gtksettings.Settings{}, nil)
	writeWallpaperPalette(t, "#4090ff")
	if err := r.cfg.SetByPath("styling.theme-provider", "wallpaper"); err != nil {
		t.Fatal(err)
	}
	if got := r.changed(t); !slices.Contains(got, AppearanceNS+" "+keyAccentColor) {
		t.Fatalf("switching to the wallpaper provider emitted %v, want the accent", got)
	}
	writeWallpaperPalette(t, "#ff9040")
	release, err := wallpaper.Export(r.bus.Conn(t), wallpaper.New(wallpaper.Options{Extractor: extract.Config{Tool: extract.None}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := wallpaper.NewClient(r.bus.Conn(t)).ExtractColors(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.changed(t); !slices.Equal(got, []string{AppearanceNS + " " + keyAccentColor}) {
		t.Fatalf("the service's extraction emitted %v, want the accent alone", got)
	}
	if v, _ := r.read(AppearanceNS, keyAccentColor); !slices.Equal(v.([]any), []any{1.0, 0x90 / 255.0, 0x40 / 255.0}) {
		t.Errorf("accent after the extraction = %v, want the new primary", v)
	}
}
