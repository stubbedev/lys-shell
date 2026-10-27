package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginDefaults(t *testing.T) {
	l := DefaultsLogin()
	if !l.Lock.Enabled || l.Lock.LockOnStart || l.Background.Mode != BackgroundColor || l.Background.Color.String() != "#000000" ||
		l.Lock.PamService != "system-auth" || !l.Greeter.ShowUserList || l.Greeter.CursorSize != 24 {
		t.Errorf("defaults = %+v", l)
	}
	at := time.Date(2026, 3, 5, 9, 7, 0, 0, time.UTC)
	if got := l.ClockFormat.Layout().Format(at) + " " + l.DateFormat.Layout().Format(at); got != "09:07 Thursday, March 5" {
		t.Errorf("default formats render %q", got)
	}
}

func TestLoginSectionLoads(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `login:
  background-mode: image
  background-image: /srv/lock.png
  background-color: "#1e1e2e80"
  clock-format: "%I:%M"
  lock:
    lock-on-start: true
    pam-service: login
  greeter:
    show-power-buttons: false
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Login
	if !l.Lock.LockOnStart || l.Background.Mode != BackgroundImage || l.Background.ImagePath("/wall.png") != "/srv/lock.png" ||
		l.Background.Color.String() != "#1e1e2e80" || l.ClockFormat.String() != "%I:%M" || l.Lock.PamService != "login" ||
		l.Greeter.ShowPowerButtons {
		t.Errorf("login = %+v", l)
	}
}

// The former lock and greeter sections still load, each key moved to its
// place in login with a warning naming both; where both set a shared key
// the lock screen's value wins, login's own keys beat both, and a key the
// old sections never had is dropped with a warning.
func TestLockAndGreeterSectionsMoveIntoLogin(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `lock:
  lock-on-start: true
  blur: 8
  show-clock: false
  pam-service: login
greeter:
  show-clock: true
  clock-format: "%I:%M"
  show-user-list: false
  cursor-size: 32
  bogus: 1
login:
  date-format: "%d"
  lock:
    grace-period-ms: 500
`)
	var cfg *Config
	out := logged(t, func() {
		var err error
		if cfg, err = LoadFile(path); err != nil {
			t.Fatal(err)
		}
	})
	l := cfg.Login
	if !l.Lock.LockOnStart || l.Blur.Sigma() != 8 || l.ShowClock || l.Lock.PamService != "login" ||
		l.ClockFormat.String() != "%I:%M" || l.Greeter.ShowUserList || l.Greeter.CursorSize != 32 ||
		l.DateFormat.String() != "%d" || l.Lock.GracePeriodMS != 500 {
		t.Errorf("migrated login = %+v", l)
	}
	for _, want := range []string{
		"lock.lock-on-start moved to login.lock.lock-on-start",
		"greeter.show-user-list moved to login.greeter.show-user-list",
		"greeter.show-clock is ignored: login.show-clock is set",
		"greeter.bogus is not a login screen setting",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log misses %q:\n%s", want, out)
		}
	}
	// A config already in the new shape loads without a word.
	path = writeFile(t, t.TempDir(), "config.yaml", "login:\n  show-clock: false\n")
	if out := logged(t, func() { _, _ = LoadFile(path) }); strings.Contains(out, "login") {
		t.Errorf("the new shape warned: %s", out)
	}
}

// The migration copies: the parsed tree it was handed is left as it was.
func TestMigrateLoginLeavesItsInputAlone(t *testing.T) {
	inner := map[string]any{"show-clock": false}
	tree := map[string]any{"lock": map[string]any{"pam-service": "login"}, "login": map[string]any{"lock": inner}}
	got := migrateLogin(tree).(map[string]any)
	if _, ok := tree["lock"]; !ok || len(inner) != 1 {
		t.Errorf("the input changed: %v", tree)
	}
	if !hasPath(got, "login.lock.pam-service") || !hasPath(got, "login.lock.show-clock") || hasPath(got, "lock") {
		t.Errorf("migrated = %v", got)
	}
}

// A bad value is a diagnostic for its field alone: the field keeps its
// default and the rest of the section applies (apply_config_layer).
func TestLoginBadValuesKeepTheirDefaults(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `login:
  background-color: "#12345g"
  clock-format: "%Q"
  background-mode: mirror
  lock:
    pam-service: login
`)
	cfg, err := LoadFile(path)
	if err == nil {
		t.Fatal("bad values: want diagnostics")
	}
	for _, key := range []string{"login.background-color", "login.clock-format", "login.background-mode"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("diagnostics %q miss %s", err, key)
		}
	}
	l := cfg.Login
	if l.Background.Color.String() != "#000000" || l.ClockFormat.String() != "%H:%M" || l.Background.Mode != BackgroundColor {
		t.Errorf("bad fields did not keep their defaults: %+v", l)
	}
	if l.Lock.PamService != "login" {
		t.Errorf("a good field beside bad ones was dropped: %q", l.Lock.PamService)
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
	path := writeFile(t, dir, "config.yaml", "login:\n  greeter:\n    cursor-theme: Adwaita\n    show-user-list: false\n")
	writeFile(t, dir, "runtime.yaml", "login:\n  greeter:\n    show-user-list: true\nbar:\n  location: bottom\n")
	cfg, err := LoadGreeter(path, func(Diagnostic) {})
	if err != nil {
		t.Fatal(err)
	}
	g := cfg.Login.Greeter
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
	if cfg == nil || cfg.Login.Greeter.CursorSize != 24 {
		t.Fatalf("no config: cfg %+v err %v", cfg, err)
	}
}

// A system runtime.toml from before the YAML switch is reported, not
// silently dropped.
func TestLoadGreeterReportsALegacyRuntimeTOML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.yaml", "login:\n  show-clock: false\n")
	legacy := writeFile(t, dir, "runtime.toml", "[login]\nshow-clock = true\n")
	cfg, err := LoadGreeter(path, func(Diagnostic) {})
	if err == nil || !strings.Contains(err.Error(), legacy+" is TOML, which lys-shell no longer reads") {
		t.Errorf("legacy runtime.toml: %v", err)
	}
	if cfg.Login.ShowClock {
		t.Error("the TOML runtime layer applied")
	}
}

// The former greeter section's cursor keys count as set: a system
// config written for the old greeter keeps its explicit cursor.
func TestLoadGreeterReadsTheFormerCursorKeys(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "greeter:\n  cursor-size: 48\n")
	cfg, _ := LoadGreeter(path, func(Diagnostic) {})
	if g := cfg.Login.Greeter; g.CursorSize != 48 || !g.CursorSizeExplicit {
		t.Errorf("cursor size = %d explicit=%v", g.CursorSize, g.CursorSizeExplicit)
	}
}
