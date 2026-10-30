package config

import (
	"strings"
	"testing"
)

// unlock-with-session is opt-in: off by default and in a config that
// leaves it out, on when set.
func TestUnlockWithSessionIsOptIn(t *testing.T) {
	if DefaultsBitwarden().UnlockWithSession {
		t.Error("unlock-with-session defaults on")
	}
	cfg, err := loadBitwarden(t, "    lock-with-session: true\n")
	if err != nil || cfg.Bitwarden.UnlockWithSession {
		t.Errorf("unset: %v, err %v, want off", cfg.Bitwarden.UnlockWithSession, err)
	}
	cfg, err = loadBitwarden(t, "    unlock-with-session: true\n")
	if err != nil || !cfg.Bitwarden.UnlockWithSession {
		t.Errorf("set: %v, err %v, want on", cfg.Bitwarden.UnlockWithSession, err)
	}
}

// A value that is not a bool is an error naming the field, and the
// default stays.
func TestUnlockWithSessionRefusesANonBool(t *testing.T) {
	cfg, err := loadBitwarden(t, "    unlock-with-session: sometimes\n")
	if err == nil || !strings.Contains(err.Error(), "Field: modules.bitwarden.unlock-with-session\n") {
		t.Fatalf("err = %v, want one naming modules.bitwarden.unlock-with-session", err)
	}
	if cfg.Bitwarden.UnlockWithSession {
		t.Error("a bad value turned it on")
	}
}
