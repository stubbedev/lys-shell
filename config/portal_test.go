package config

import (
	"strings"
	"testing"
)

// The screen-share reminder defaults to 30 days, 0 turns it off, and a
// negative count is refused with the field named, keeping the default.
func TestPortalScreenCastReminderDays(t *testing.T) {
	if got := DefaultsPortal().ScreenCast.ReminderDays; got != 30 {
		t.Errorf("default = %d, want 30", got)
	}
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "portal:\n  screen-cast:\n    reminder-days: 0\n"))
	if err != nil || cfg.Portal.ScreenCast.ReminderDays != 0 {
		t.Errorf("0 = %d, %v", cfg.Portal.ScreenCast.ReminderDays, err)
	}
	cfg, err = LoadFile(writeFile(t, t.TempDir(), "config.yaml", "portal:\n  screen-cast:\n    reminder-days: -1\n"))
	if err == nil || !strings.Contains(err.Error(), "portal.screen-cast.reminder-days") || cfg.Portal.ScreenCast.ReminderDays != 30 {
		t.Errorf("-1 = %d, %v; want the default and the field's diagnostic", cfg.Portal.ScreenCast.ReminderDays, err)
	}
}
