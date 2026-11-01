package config

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/nightlight"
)

func TestNightlightTransitionDefaultsToAQuarterHour(t *testing.T) {
	if got := DefaultsNightlight().TransitionDuration.Transition().Minutes(); got != 15 {
		t.Errorf("default = %d min, want 15", got)
	}
}

func TestNightlightTransitionHoldsItsRange(t *testing.T) {
	for _, minutes := range []int64{0, 5, nightlight.MaxTransitionMinutes} {
		path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  nightlight:\n    transition-duration: "+strconv.FormatInt(minutes, 10)+"\n")
		cfg, err := LoadFile(path)
		if err != nil {
			t.Fatalf("%d minutes: %v", minutes, err)
		}
		if got := cfg.Nightlight.TransitionDuration.Transition().Minutes(); int64(got) != minutes {
			t.Errorf("%d minutes loaded as %d", minutes, got)
		}
	}
	for _, minutes := range []int64{nightlight.MaxTransitionMinutes + 1, -1} {
		path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  nightlight:\n    transition-duration: "+strconv.FormatInt(minutes, 10)+"\n")
		_, err := LoadFile(path)
		if err == nil || !strings.Contains(err.Error(), "Field: modules.nightlight.transition-duration") {
			t.Errorf("%d minutes: err = %v, want the field's range diagnostic", minutes, err)
		}
	}
}

func TestNightlightTransitionIsABoundedSpin(t *testing.T) {
	m, ok := Field("modules.nightlight.transition-duration")
	if !ok || m.Kind != FieldInt || m.Min != 0 || m.Max != nightlight.MaxTransitionMinutes {
		t.Errorf("meta = %+v, want an int field in 0-%d", m, nightlight.MaxTransitionMinutes)
	}
}

// Coordinates are an explicit choice: absent by default, and a config
// that names them, the way existing ones do, keeps them.
func TestNightlightCoordinatesAreOptional(t *testing.T) {
	if _, ok, err := DefaultsNightlight().PinnedLocation(); ok || err != nil {
		t.Errorf("defaults = %v, %v; want no location and no error", ok, err)
	}
	for _, field := range []string{"latitude", "longitude"} {
		if m, ok := Field("modules.nightlight." + field); !ok || !m.Optional {
			t.Errorf("%s meta = %+v, want optional", field, m)
		}
	}
	path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  nightlight:\n    latitude: 55.6\n    longitude: 12.5\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loc, ok, err := cfg.Nightlight.PinnedLocation()
	if err != nil || !ok || loc.Latitude() != 55.6 || loc.Longitude() != 12.5 {
		t.Errorf("pinned = %+v, %v, %v", loc, ok, err)
	}
}

func TestNightlightPinnedLocationRefusesAHalfOrBadPair(t *testing.T) {
	coordinate := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name     string
		lat, lng *float64
		want     string
	}{
		{"latitude alone", coordinate(55.6), nil, "both latitude and longitude"},
		{"longitude alone", nil, coordinate(12.5), "both latitude and longitude"},
		{"off the globe", coordinate(95), coordinate(12.5), "latitude 95"},
	} {
		cfg := DefaultsNightlight()
		cfg.Latitude, cfg.Longitude = tc.lat, tc.lng
		_, ok, err := cfg.PinnedLocation()
		if ok || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, %v; want an error naming %q", tc.name, ok, err, tc.want)
		}
	}
	cfg := DefaultsNightlight()
	cfg.Latitude, cfg.Longitude = coordinate(0), coordinate(0)
	if _, ok, err := cfg.PinnedLocation(); ok || !errors.Is(err, nightlight.ErrNullIsland) {
		t.Errorf("0, 0 = %v, %v; want ErrNullIsland", ok, err)
	}
}

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
