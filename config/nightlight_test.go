package config

import (
	"strings"
	"testing"
)

// The section's former key still loads, with a warning naming the
// replacement.
func TestNightlightReadsTheHyprsunsetKey(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  hyprsunset:\n    temperature: 3300\n")
	var cfg *Config
	out := logged(t, func() {
		var err error
		if cfg, err = LoadFile(path); err != nil {
			t.Fatal(err)
		}
	})
	if cfg.Nightlight.Temperature != 3300 {
		t.Errorf("temperature = %d, want 3300 from modules.hyprsunset", cfg.Nightlight.Temperature)
	}
	if !strings.Contains(out, "modules.hyprsunset") || !strings.Contains(out, "nightlight") {
		t.Errorf("log = %q, want a deprecation naming both keys", out)
	}

	path = writeFile(t, t.TempDir(), "config.yaml", "modules:\n  nightlight:\n    temperature: 3400\n")
	out = logged(t, func() {
		var err error
		if cfg, err = LoadFile(path); err != nil {
			t.Fatal(err)
		}
	})
	if cfg.Nightlight.Temperature != 3400 || strings.Contains(out, "deprecated") {
		t.Errorf("canonical key: temperature %d, log %q", cfg.Nightlight.Temperature, out)
	}
}

// A layout naming the module by its former name gets the night light
// and a warning; an unknown name still fails.
func TestBarLayoutRenamesHyprsunset(t *testing.T) {
	var m BarModule
	out := logged(t, func() {
		if err := m.UnmarshalConfig("hyprsunset"); err != nil {
			t.Fatal(err)
		}
	})
	if m != ModuleNightlight {
		t.Errorf("hyprsunset decoded to %q, want %q", m, ModuleNightlight)
	}
	if !strings.Contains(out, `"hyprsunset" was renamed to "nightlight"`) {
		t.Errorf("log = %q, want the rename", out)
	}
	if err := m.UnmarshalConfig("nightlight"); err != nil || m != ModuleNightlight {
		t.Errorf("nightlight = %q, %v", m, err)
	}
	if err := m.UnmarshalConfig("sunset"); err == nil || !strings.Contains(err.Error(), "unknown variant `sunset`") {
		t.Errorf("sunset = %v, want an unknown-variant error", err)
	}
}
