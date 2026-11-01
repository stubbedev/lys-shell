package nightlight

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// overrideFile is the record's name inside the lys-shell state dir.
const overrideFile = "nightlight-override"

// locationFile is the last GeoClue fix's name inside the state dir.
const locationFile = "nightlight-location"

// overrideMaxAge discards records old enough to span a whole day/night
// cycle (persist.rs MAX_AGE).
const overrideMaxAge = 12 * time.Hour

// Override is a manual toggle remembered across restarts: the filter
// state it chose and, when the auto-schedule ran, the solar phase it
// was made in (zero Phase for none).
type Override struct {
	Phase   Phase
	Enabled bool
}

// SaveOverride records a toggle made at now (persist.rs save); write
// failures are quiet, as the record is a convenience.
func SaveOverride(dir string, o Override, now time.Time) {
	enabled := "off"
	if o.Enabled {
		enabled = "on"
	}
	body := o.Phase.String() + " " + enabled + " " + now.UTC().Format(time.RFC3339) + "\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, overrideFile), []byte(body), 0o600)
}

// ClearOverride drops the record (a phase change ends it).
func ClearOverride(dir string) {
	_ = os.Remove(filepath.Join(dir, overrideFile))
}

// LoadOverride reads the record; false when absent, malformed, or
// stale.
func LoadOverride(dir string, now time.Time) (Override, bool) {
	body, err := os.ReadFile(filepath.Join(dir, overrideFile)) //nolint:gosec // lys-shell's own state dir
	if err != nil {
		return Override{}, false
	}
	return parseOverride(string(body), now)
}

// parseOverride is persist.rs parse: "<phase> <on|off> <rfc3339>".
func parseOverride(body string, now time.Time) (Override, bool) {
	fields := strings.Fields(body)
	if len(fields) < 3 {
		return Override{}, false
	}
	saved, err := time.Parse(time.RFC3339, fields[2])
	if err != nil {
		return Override{}, false
	}
	if now.Sub(saved) > overrideMaxAge {
		return Override{}, false
	}
	var phase Phase
	switch fields[0] {
	case "day":
		phase = PhaseDay
	case "night":
		phase = PhaseNight
	case "none":
	default:
		return Override{}, false
	}
	return Override{Phase: phase, Enabled: fields[1] == "on"}, true
}

// SaveLocation remembers a fix for the next start, atomically, so a
// restart at night tints the display at once rather than after GeoClue
// answers.
func SaveLocation(dir string, loc Location) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("nightlight: create the state dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, locationFile+".*")
	if err != nil {
		return fmt.Errorf("nightlight: save the location in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	body := strconv.FormatFloat(loc.latitude, 'f', -1, 64) + " " + strconv.FormatFloat(loc.longitude, 'f', -1, 64) + "\n"
	_, err = tmp.WriteString(body)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, locationFile))
	}
	if err != nil {
		return fmt.Errorf("nightlight: save the location in %s: %w", dir, err)
	}
	return nil
}

// LoadLocation reads the remembered fix; the error wraps fs.ErrNotExist
// when there is none, and names the file when it is unusable.
func LoadLocation(dir string) (Location, error) {
	path := filepath.Join(dir, locationFile)
	body, err := os.ReadFile(path) //nolint:gosec // lys-shell's own state dir
	if err != nil {
		return Location{}, fmt.Errorf("nightlight: read the location: %w", err)
	}
	fields := strings.Fields(string(body))
	if len(fields) != 2 {
		return Location{}, fmt.Errorf("nightlight: %s holds %d fields, want latitude and longitude", path, len(fields))
	}
	latitude, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Location{}, fmt.Errorf("nightlight: latitude in %s: %w", path, err)
	}
	longitude, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return Location{}, fmt.Errorf("nightlight: longitude in %s: %w", path, err)
	}
	loc, err := NewLocation(latitude, longitude)
	if err != nil {
		return Location{}, fmt.Errorf("%s: %w", path, err)
	}
	return loc, nil
}
