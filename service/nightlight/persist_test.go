package nightlight

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	if err := SaveOverride(dir, Override{Phase: PhaseNight, Enabled: true}, persistNow()); err != nil {
		t.Fatal(err)
	}
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

// A save replaces the record whole: a reader holding the old file keeps
// the old file, and nothing but the record is left behind.
func TestSaveOverrideReplacesTheRecordAtomically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := SaveOverride(dir, Override{Phase: PhaseDay, Enabled: true}, persistNow()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, overrideFile)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := before.Mode().Perm(); perm != 0o600 {
		t.Errorf("record mode = %o, want 600", perm)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := SaveOverride(dir, Override{Phase: PhaseNight}, persistNow()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the save wrote into the existing file instead of replacing it")
	}
	held, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(held), "day on ") {
		t.Errorf("a reader of the old file sees %q", held)
	}
	if rec, ok := LoadOverride(dir, persistNow()); !ok || rec.Phase != PhaseNight || rec.Enabled {
		t.Errorf("after the second save = %+v, %v", rec, ok)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("state dir = %v, %v; want the record and no temp file", entries, err)
	}
}

func TestSaveOverrideReportsWhyItFailed(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "state")
	err := SaveOverride(dir, Override{Enabled: true}, persistNow())
	if !errors.Is(err, syscall.ENOTDIR) || !strings.Contains(err.Error(), dir) {
		t.Errorf("a state dir under a file: err = %v, want ENOTDIR naming %s", err, dir)
	}

	// The record's own path taken by a directory: the rename fails, and the
	// temporary does not stay behind.
	dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, overrideFile), 0o700); err != nil {
		t.Fatal(err)
	}
	err = SaveOverride(dir, Override{Enabled: true}, persistNow())
	if err == nil || !strings.Contains(err.Error(), overrideFile) {
		t.Errorf("a directory in the record's place: err = %v, want one naming %s", err, overrideFile)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("state dir = %v, %v; want no temp file left", entries, err)
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
