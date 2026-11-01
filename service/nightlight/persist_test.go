package nightlight

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func persistNow() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }

// The persist.rs test cases.
func TestParseOverride(t *testing.T) {
	if rec, ok := parseOverride("day on 2026-09-05T11:59:00Z", persistNow()); !ok || rec.Phase != PhaseDay || !rec.Enabled {
		t.Errorf("fresh record = %+v, %v", rec, ok)
	}
	if rec, ok := parseOverride("none off 2026-09-05T11:00:00Z", persistNow()); !ok || rec.Phase != 0 || rec.Enabled {
		t.Errorf("phase-less off record = %+v, %v", rec, ok)
	}
	if _, ok := parseOverride("night on 2026-09-04T12:00:00Z", persistNow()); ok {
		t.Error("a stale record was honoured")
	}
	for _, bad := range []string{"night on", "dusk on 2026-09-05T11:59:00Z", "", "day on yesterday"} {
		if _, ok := parseOverride(bad, persistNow()); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestOverrideRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadOverride(dir, persistNow()); ok {
		t.Fatal("an empty dir has a record")
	}
	SaveOverride(dir, Override{Phase: PhaseNight, Enabled: true}, persistNow())
	rec, ok := LoadOverride(dir, persistNow().Add(time.Hour))
	if !ok || rec.Phase != PhaseNight || !rec.Enabled {
		t.Fatalf("round trip = %+v, %v", rec, ok)
	}
	if _, ok := LoadOverride(dir, persistNow().Add(13*time.Hour)); ok {
		t.Error("the record outlived its 12h age limit")
	}
	ClearOverride(dir)
	if _, ok := LoadOverride(dir, persistNow()); ok {
		t.Error("the record survived ClearOverride")
	}
}

func TestLocationRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	want := mustLocation(t, -33.8688, 151.2093)
	if err := SaveLocation(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLocation(dir); err != nil || got != want {
		t.Errorf("loaded %+v, %v; want %+v", got, err, want)
	}
	next := mustLocation(t, 55.6, 12.5)
	if err := SaveLocation(dir, next); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLocation(dir); err != nil || got != next {
		t.Errorf("a second save loaded %+v, %v; want %+v", got, err, next)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("state dir = %v, %v; want the record and no temp file", entries, err)
	}
}

func TestLoadLocationSaysWhyThereIsNone(t *testing.T) {
	if _, err := LoadLocation(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no record: %v, want ErrNotExist", err)
	}
	for _, bad := range []string{"", "55.6", "55.6 12.5 3", "north 12.5", "55.6 east", "95 12.5", "0 0"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, locationFile), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadLocation(dir)
		if err == nil || errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), locationFile) {
			t.Errorf("%q: err = %v, want one naming the record", bad, err)
		}
	}
}
