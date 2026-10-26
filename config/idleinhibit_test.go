package config

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

func TestIdleInhibitStartupDurationTakesTheLongestTimer(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  idle-inhibit:\n    startup-duration: "+strconv.Itoa(idleinhibit.MaxMinutes)+"\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("the longest duration was refused: %v", err)
	}
	if got := cfg.IdleInhibit.StartupDuration.Duration(); got.Minutes() != idleinhibit.MaxMinutes || got.Seconds() != idleinhibit.MaxMinutes*60 {
		t.Errorf("duration = %d min, %d s; want %d min, %d s", got.Minutes(), got.Seconds(), idleinhibit.MaxMinutes, idleinhibit.MaxMinutes*60)
	}
}

func TestIdleInhibitStartupDurationRefusesWhatTheTimerCannotCount(t *testing.T) {
	for _, minutes := range []int64{idleinhibit.MaxMinutes + 1, 1 << 32, -1} {
		path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  idle-inhibit:\n    startup-duration: "+strconv.FormatInt(minutes, 10)+"\n")
		cfg, err := LoadFile(path)
		want := "Error: idle inhibit duration out of range: " + strconv.FormatInt(minutes, 10) + " minutes, valid 0-71582788"
		if err == nil || !strings.Contains(err.Error(), "Field: modules.idle-inhibit.startup-duration") || !strings.Contains(err.Error(), want) {
			t.Errorf("%d minutes: err = %v, want the field's range diagnostic", minutes, err)
		}
		if got := cfg.IdleInhibit.StartupDuration; got != DefaultsIdleInhibit().StartupDuration {
			t.Errorf("%d minutes: kept %d, want the default", minutes, got.Duration().Minutes())
		}
	}
}

func TestIdleInhibitStartupDurationIsABoundedSpin(t *testing.T) {
	m, ok := Field("modules.idle-inhibit.startup-duration")
	if !ok || m.Kind != FieldInt || m.Min != 0 || m.Max != idleinhibit.MaxMinutes {
		t.Errorf("meta = %+v, want an int field in 0-%d", m, idleinhibit.MaxMinutes)
	}
}
