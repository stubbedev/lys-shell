package config

import (
	"strings"
	"testing"
)

func TestBatteryAlertDefaults(t *testing.T) {
	cfg := Defaults()
	if !cfg.Battery.NotifyLow || cfg.Battery.NotifyChargedAt != 0 {
		t.Errorf("battery alerts = notify-low %v, charged-at %d; want on and off", cfg.Battery.NotifyLow, cfg.Battery.NotifyChargedAt)
	}
	if cfg.PowerProfiles.AutoSaverAt != AutoSaverNever {
		t.Errorf("auto-saver-at = %q, want never", cfg.PowerProfiles.AutoSaverAt)
	}
}

func TestBatteryAlertFieldsParse(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml",
		"modules:\n  battery:\n    notify-low: false\n    notify-charged-at: 80\n  power-profiles:\n    auto-saver-at: critical\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Battery.NotifyLow || cfg.Battery.NotifyChargedAt != 80 || cfg.PowerProfiles.AutoSaverAt != AutoSaverCritical {
		t.Errorf("parsed notify-low %v, charged-at %d, auto-saver-at %q", cfg.Battery.NotifyLow, cfg.Battery.NotifyChargedAt, cfg.PowerProfiles.AutoSaverAt)
	}
}

func TestNotifyChargedAtClampsToAPercentage(t *testing.T) {
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  battery:\n    notify-charged-at: 150\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Battery.NotifyChargedAt != 100 {
		t.Errorf("notify-charged-at 150 = %d, want clamped to 100", cfg.Battery.NotifyChargedAt)
	}
}

func TestBatteryAlertFieldsRefuseBadValues(t *testing.T) {
	for _, tc := range []struct{ yaml, field string }{
		{"modules:\n  power-profiles:\n    auto-saver-at: sometimes\n", "Field: modules.power-profiles.auto-saver-at"},
	} {
		cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%q: err = %v, want a diagnostic naming %s", tc.yaml, err, tc.field)
		}
		if cfg.PowerProfiles.AutoSaverAt != AutoSaverNever || cfg.Battery.NotifyChargedAt != 0 {
			t.Errorf("%q: a refused value was kept", tc.yaml)
		}
	}
}
