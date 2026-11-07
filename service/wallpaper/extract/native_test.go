package extract

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling/material"
)

func solid(c material.ARGB, n int) []material.ARGB {
	out := make([]material.ARGB, n)
	for i := range out {
		out[i] = c
	}
	return out
}

func nativeConfig() Config {
	cfg := DefaultConfig()
	cfg.Tool = Material
	return cfg
}

func readPalette(t *testing.T) map[string]map[string]string {
	t.Helper()
	path, err := WallpaperColorsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Colors map[string]map[string]string `json:"colors"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out.Colors
}

func TestNativeWritesTheSchemeWithoutAnyTool(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", "")
	var n Native
	sample := solid(material.RGB(0x20, 0x60, 0xd0), 64)
	if err := n.Extract(nativeConfig(), sample); err != nil {
		t.Fatal(err)
	}
	colors := readPalette(t)
	seed := material.Score(material.Quantize(sample, 128))[0]
	light := material.NewScheme(seed, material.TonalSpot, false, 0)
	dark := material.NewScheme(seed, material.TonalSpot, true, 0)
	for r := range material.RoleCount {
		got := colors[r.String()]
		if got["light"] != light.Color(r).Hex() || got["dark"] != dark.Color(r).Hex() {
			t.Errorf("%s = %v, want light %s dark %s", r, got, light.Color(r).Hex(), dark.Color(r).Hex())
		}
	}
	if colors["source_color"]["dark"] != seed.Hex() {
		t.Errorf("source_color = %v, want %s", colors["source_color"], seed.Hex())
	}
}

func TestNativeDoesNotExtractAWallpaperSeenLately(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var n Native
	a, b := solid(material.RGB(0xd0, 0x40, 0x30), 64), solid(material.RGB(0x30, 0xa0, 0x40), 64)
	for _, sample := range [][]material.ARGB{a, a, b, a, b} {
		if err := n.Extract(nativeConfig(), sample); err != nil {
			t.Fatal(err)
		}
	}
	if n.computed != 2 {
		t.Errorf("five extractions of two wallpapers computed %d palettes, want 2", n.computed)
	}
	if got, want := readPalette(t)["source_color"]["dark"], material.Score(material.Quantize(b, 128))[0].Hex(); got != want {
		t.Errorf("palette seed %s, want the last wallpaper's %s", got, want)
	}
	cfg := nativeConfig()
	cfg.MatugenScheme = config.MatugenSchemeVibrant
	if err := n.Extract(cfg, b); err != nil {
		t.Fatal(err)
	}
	if n.computed != 3 {
		t.Errorf("a new scheme computed %d palettes in all, want 3", n.computed)
	}
}

func TestNativeClearRemovesThePalette(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var n Native
	sample := solid(material.RGB(0xd0, 0x40, 0x30), 64)
	if err := n.Extract(nativeConfig(), sample); err != nil {
		t.Fatal(err)
	}
	if err := n.Clear(); err != nil {
		t.Fatal(err)
	}
	path, _ := WallpaperColorsPath()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("palette after Clear: %v", err)
	}
	if err := n.Clear(); err != nil {
		t.Errorf("a second Clear: %v", err)
	}
	if err := n.Extract(nativeConfig(), sample); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the same wallpaper after Clear is not written again: %v", err)
	}
}

func TestEveryMatugenSchemeHasAVariant(t *testing.T) {
	for _, s := range []config.MatugenScheme{
		config.MatugenSchemeContent, config.MatugenSchemeExpressive, config.MatugenSchemeFidelity,
		config.MatugenSchemeFruitSalad, config.MatugenSchemeMonochrome, config.MatugenSchemeNeutral,
		config.MatugenSchemeRainbow, config.MatugenSchemeTonalSpot, config.MatugenSchemeVibrant,
	} {
		v, err := Variant(s)
		if err != nil || v.String() != string(s) {
			t.Errorf("Variant(%s) = %v, %v", s, v, err)
		}
	}
	if _, err := Variant("plaid"); err == nil {
		t.Error("an unknown scheme has a variant")
	}
}

func TestExtractRefusesTheMaterialTool(t *testing.T) {
	if err := Extract(t.Context(), nativeConfig(), "/any.png"); err == nil {
		t.Error("Extract ran the material tool on a path")
	}
}
