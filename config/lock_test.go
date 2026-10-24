package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockDefaults(t *testing.T) {
	l := DefaultsLock()
	if !l.Enabled || l.LockOnStart || l.Background.Mode != BackgroundColor || l.Background.Color.String() != "#000000" {
		t.Errorf("defaults = %+v", l)
	}
	at := time.Date(2026, 3, 5, 9, 7, 0, 0, time.UTC)
	if got := l.ClockFormat.Layout().Format(at) + " " + l.DateFormat.Layout().Format(at); got != "09:07 Thursday, March 5" {
		t.Errorf("default formats render %q", got)
	}
}

func TestLockSectionLoads(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `lock:
  lock-on-start: true
  background-mode: image
  background-image: /srv/lock.png
  background-color: "#1e1e2e80"
  clock-format: "%I:%M"
  pam-service: login
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Lock
	if !l.LockOnStart || l.Background.Mode != BackgroundImage || l.Background.ImagePath("/wall.png") != "/srv/lock.png" ||
		l.Background.Color.String() != "#1e1e2e80" || l.ClockFormat.String() != "%I:%M" || l.PamService != "login" {
		t.Errorf("lock = %+v", l)
	}
}

// A bad value is a diagnostic for its field alone: the field keeps its
// default and the rest of the section applies (apply_config_layer).
func TestLockBadValuesKeepTheirDefaults(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `lock:
  background-color: "#12345g"
  clock-format: "%Q"
  background-mode: mirror
  pam-service: login
`)
	cfg, err := LoadFile(path)
	if err == nil {
		t.Fatal("bad values: want diagnostics")
	}
	for _, key := range []string{"lock.background-color", "lock.clock-format", "lock.background-mode"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("diagnostics %q miss %s", err, key)
		}
	}
	l := cfg.Lock
	if l.Background.Color.String() != "#000000" || l.ClockFormat.String() != "%H:%M" || l.Background.Mode != BackgroundColor {
		t.Errorf("bad fields did not keep their defaults: %+v", l)
	}
	if l.PamService != "login" {
		t.Errorf("a good field beside bad ones was dropped: %q", l.PamService)
	}
}

func TestBackgroundImagePath(t *testing.T) {
	for _, tc := range []struct {
		mode LockBackground
		want string
	}{{BackgroundColor, ""}, {BackgroundImage, "/img.png"}, {BackgroundWallpaper, "/wall.png"}} {
		b := Background{Mode: tc.mode, Image: "/img.png"}
		if got := b.ImagePath("/wall.png"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestLoadGreeterLayersAndExplicitCursor(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.yaml", "greeter:\n  cursor-theme: Adwaita\n  show-user-list: false\n")
	writeFile(t, dir, "runtime.yaml", "greeter:\n  show-user-list: true\nbar:\n  location: bottom\n")
	cfg, err := LoadGreeter(path, func(Diagnostic) {})
	if err != nil {
		t.Fatal(err)
	}
	g := cfg.Greeter
	if g.CursorTheme != "Adwaita" || !g.CursorThemeExplicit {
		t.Errorf("cursor theme = %q explicit=%v, want the config value, explicit", g.CursorTheme, g.CursorThemeExplicit)
	}
	if g.CursorSize != 24 || g.CursorSizeExplicit {
		t.Errorf("cursor size = %d explicit=%v, want the unset default", g.CursorSize, g.CursorSizeExplicit)
	}
	if !g.ShowUserList {
		t.Error("runtime.yaml did not override config.yaml")
	}
	// The whole runtime.yaml is the runtime layer, as lys-greeter's load.
	if cfg.Bar.Location != LocationBottom {
		t.Errorf("runtime bar was not applied: %s", cfg.Bar.Location)
	}
}

func TestLoadGreeterWithoutFilesRenders(t *testing.T) {
	cfg, err := LoadGreeter(filepath.Join(t.TempDir(), "missing", "config.yaml"), func(Diagnostic) {})
	if cfg == nil || cfg.Greeter.CursorSize != 24 {
		t.Fatalf("no config: cfg %+v err %v", cfg, err)
	}
}

// A system runtime.toml from before the YAML switch is reported, not
// silently dropped.
func TestLoadGreeterReportsALegacyRuntimeTOML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.yaml", "greeter:\n  show-clock: false\n")
	legacy := writeFile(t, dir, "runtime.toml", "[greeter]\nshow-clock = true\n")
	cfg, err := LoadGreeter(path, func(Diagnostic) {})
	if err == nil || !strings.Contains(err.Error(), legacy+" is TOML, which lys-shell no longer reads") {
		t.Errorf("legacy runtime.toml: %v", err)
	}
	if cfg.Greeter.ShowClock {
		t.Error("the TOML runtime layer applied")
	}
}
