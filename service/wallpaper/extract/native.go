package extract

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/stubbedev/lys-lib/atomicfile"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling/material"
)

// SampleSide is the longest side, in samples, of the grid a wallpaper is
// read at for native extraction: the quantizer's input is bounded by it
// whatever the wallpaper's resolution.
const SampleSide = 128

// nativeCacheEntries is how many recent palettes Native keeps, so going
// back to a recent wallpaper re-themes without quantizing it again.
const nativeCacheEntries = 8

var variants = map[config.MatugenScheme]material.Variant{
	config.MatugenSchemeContent:    material.Content,
	config.MatugenSchemeExpressive: material.Expressive,
	config.MatugenSchemeFidelity:   material.Fidelity,
	config.MatugenSchemeFruitSalad: material.FruitSalad,
	config.MatugenSchemeMonochrome: material.Monochrome,
	config.MatugenSchemeNeutral:    material.Neutral,
	config.MatugenSchemeRainbow:    material.Rainbow,
	config.MatugenSchemeTonalSpot:  material.TonalSpot,
	config.MatugenSchemeVibrant:    material.Vibrant,
}

// Variant is the Material variant a matugen scheme name stands for.
func Variant(s config.MatugenScheme) (material.Variant, error) {
	v, ok := variants[s]
	if !ok {
		return 0, fmt.Errorf("extract: scheme %q has no Material variant", s)
	}
	return v, nil
}

type nativeKey struct {
	sample   uint64
	scheme   config.MatugenScheme
	contrast float64
	source   uint8
}

type nativeEntry struct {
	key  nativeKey
	json []byte
}

// Native computes Material You palettes from wallpaper samples in
// process and writes them, in matugen's JSON shape, to
// WallpaperColorsPath. It keeps the last few palettes by the sample's
// content hash, so a wallpaper set again is not quantized again. It is
// not safe for concurrent use; the wallpaper service serializes it.
type Native struct {
	recent   []nativeEntry
	written  nativeKey
	wrote    bool
	computed int
}

// Extract themes from sample, the wallpaper read at SampleSide: the
// palette is taken from the cache when this sample and config were seen
// lately, and the file is rewritten only when it would change.
func (n *Native) Extract(cfg Config, sample []material.ARGB) error {
	key := nativeKey{sample: sampleHash(sample), scheme: cfg.MatugenScheme, contrast: cfg.MatugenContrast, source: cfg.MatugenSourceColor}
	if n.wrote && n.written == key {
		return nil
	}
	data, err := n.palette(key, cfg, sample)
	if err != nil {
		return err
	}
	path, err := WallpaperColorsPath()
	if err != nil {
		return &PathError{Context: "wallpaper palette path", Err: err}
	}
	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return &PathError{Context: "writing the wallpaper palette", Err: err}
	}
	n.written, n.wrote = key, true
	return nil
}

// Clear removes the wallpaper palette: no wallpaper is set, so the
// provider falls back to the configured palette.
func (n *Native) Clear() error {
	n.wrote = false
	path, err := WallpaperColorsPath()
	if err != nil {
		return &PathError{Context: "wallpaper palette path", Err: err}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &PathError{Context: "removing the wallpaper palette", Err: err}
	}
	return nil
}

func (n *Native) palette(key nativeKey, cfg Config, sample []material.ARGB) ([]byte, error) {
	if i := slices.IndexFunc(n.recent, func(e nativeEntry) bool { return e.key == key }); i >= 0 {
		return n.recent[i].json, nil
	}
	variant, err := Variant(cfg.MatugenScheme)
	if err != nil {
		return nil, err
	}
	n.computed++
	data := NativePalette(sample, variant, cfg.MatugenContrast, int(min(cfg.MatugenSourceColor, matugenMaxSourceColor)))
	if len(n.recent) == nativeCacheEntries {
		n.recent = slices.Delete(n.recent, 0, 1)
	}
	n.recent = append(n.recent, nativeEntry{key: key, json: data})
	return data, nil
}

func sampleHash(sample []material.ARGB) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	for _, c := range sample {
		for shift := 0; shift < 32; shift += 8 {
			h ^= uint64(c>>shift) & 0xff
			h *= prime
		}
	}
	return h
}

// NativePalette is the matugen-shaped JSON of the light and dark schemes
// of the sample's seed: the source-th best seed the scorer ranks, or its
// last when it ranks fewer.
func NativePalette(sample []material.ARGB, variant material.Variant, contrast float64, source int) []byte {
	seeds := material.Score(material.Quantize(sample, 128))
	seed := seeds[min(source, len(seeds)-1)]
	light := material.NewScheme(seed, variant, false, contrast)
	dark := material.NewScheme(seed, variant, true, contrast)
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"colors\": {\n    \"source_color\": {\"dark\": %q, \"light\": %q},\n", seed.Hex(), seed.Hex())
	for r := range material.RoleCount {
		fmt.Fprintf(&b, "    %q: {\"dark\": %q, \"light\": %q}", r.String(), dark.Color(r).Hex(), light.Color(r).Hex())
		if r+1 < material.RoleCount {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  }\n}\n")
	return b.Bytes()
}

// WallpaperColorsPath is where the wallpaper provider leaves its palette
// ($XDG_CACHE_HOME/lys-shell/wallpaper-colors.json); the lys-shell cache
// dir is created on demand.
func WallpaperColorsPath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wallpaper-colors.json"), nil
}
