package config

import (
	"errors"
	"testing"
)

// popup-stack takes per-app, all or off; anything else is an error
// naming the key and its variants, the default per-app kept rather than
// read as stacking turned off.
func TestPopupStackLoadsItsVariantsOnly(t *testing.T) {
	if got := Defaults().Notification.PopupStack; got != PopupStackPerApp {
		t.Errorf("default popup-stack = %q, want per-app", got)
	}
	for raw, want := range map[string]PopupStack{"per-app": PopupStackPerApp, "all": PopupStackAll, "off": PopupStackOff} {
		cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  notifications:\n    popup-stack: "+raw+"\n"))
		if err != nil || cfg.Notification.PopupStack != want {
			t.Errorf("popup-stack %s: %q, %v", raw, cfg.Notification.PopupStack, err)
		}
	}
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  notifications:\n    popup-stack: sideways\n    popup-opacity: 40\n"))
	var d Diagnostic
	const want = "unknown variant `sideways`, expected one of `per-app`, `all`, `off`"
	if !errors.As(err, &d) || d.Field("Field") != "modules.notifications.popup-stack" || d.Field("Error") != want {
		t.Errorf("popup-stack sideways: error %v, want %q", err, want)
	}
	if cfg.Notification.PopupStack != PopupStackPerApp || cfg.Notification.PopupOpacity != 40 {
		t.Errorf("popup-stack sideways: stack %q opacity %d, want the default kept and the valid sibling applied", cfg.Notification.PopupStack, cfg.Notification.PopupOpacity)
	}
}

// popup-opacity is a percentage: it loads as given and clamps into
// 0-100 like every percentage.
func TestPopupOpacityIsAPercentage(t *testing.T) {
	if got := Defaults().Notification.PopupOpacity; got != 75 {
		t.Errorf("default popup-opacity = %d, want 75", got)
	}
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  notifications:\n    popup-opacity: 150\n"))
	if err != nil || cfg.Notification.PopupOpacity != 100 {
		t.Errorf("popup-opacity 150: %d, %v, want clamped to 100", cfg.Notification.PopupOpacity, err)
	}
}
