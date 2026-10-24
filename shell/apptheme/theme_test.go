package apptheme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

func isolateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "lys-shell")
}

func TestBundleCarriesStaticThemeAndUserCSS(t *testing.T) {
	dir := isolateConfigDir(t)
	cfg := config.Defaults()
	theme := New(cfg)
	bundle := theme.Bundle()
	if !strings.HasPrefix(bundle, styling.StaticCSS) {
		t.Error("the bundle does not open with the compiled SCSS")
	}
	if !strings.Contains(bundle, ":root {\n    --palette-bg: ") {
		t.Error("the bundle lacks the theme :root block")
	}
	// The animation overrides follow the theme, before the user part.
	anim := cfg.Animations.CSSOverrides()
	if i, j := strings.Index(bundle, ":root {\n    --palette-bg: "), strings.Index(bundle, anim); j < 0 || j < i {
		t.Error("the [animations] overrides are missing or precede the theme")
	}
	cfg.Animations.Indicators = false
	if !strings.Contains(theme.Bundle(), "--cfg-anim-spin: 0s;") {
		t.Error("indicators off did not freeze the looping animations")
	}
	cfg.Animations.Indicators = true
	// The scaffold was created, and a user rule lands last.
	if _, err := os.Stat(filepath.Join(dir, "styles", "index.scss")); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "styles", "index.scss"), []byte(".bar { .x { color: red; } }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := theme.Bundle(); !strings.HasSuffix(strings.TrimSpace(b), "}") || !strings.Contains(b, ".bar .x") {
		t.Errorf("user SCSS did not compile into the bundle tail: %q", b[max(0, len(b)-80):])
	}
	// A broken user stylesheet drops only the user part.
	if err := os.WriteFile(filepath.Join(dir, "styles", "index.scss"), []byte(".bar { color: red"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := theme.Bundle(); !strings.HasPrefix(b, styling.StaticCSS) || strings.Contains(b, ".bar .x") {
		t.Error("a broken user stylesheet broke the bundle")
	}
}

func TestUserStylesStampFollowsEdits(t *testing.T) {
	dir := isolateConfigDir(t)
	if userStylesStamp() != "" {
		t.Error("stamp without a styles dir")
	}
	styles := filepath.Join(dir, "styles")
	if err := os.MkdirAll(styles, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(styles, "index.scss"), []byte("a {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := userStylesStamp()
	if err := os.WriteFile(filepath.Join(styles, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if userStylesStamp() != first {
		t.Error("a non-stylesheet file moved the stamp")
	}
	if err := os.WriteFile(filepath.Join(styles, "_part.scss"), []byte("b { }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if userStylesStamp() == first {
		t.Error("a new partial did not move the stamp")
	}
}

func TestUserStylesReloadOnAWatchedChange(t *testing.T) {
	dir := isolateConfigDir(t)
	styles := filepath.Join(dir, "styles")
	if err := os.MkdirAll(styles, 0o700); err != nil {
		t.Fatal(err)
	}
	theme := New(config.Defaults())
	var changed func()
	var watched []string
	polled := false
	theme.watchStyles(func(paths []string, recursive bool, fn func()) (func(), error) {
		watched, changed = paths, fn
		if !recursive {
			t.Error("the styles tree is watched flat")
		}
		return func() {}, nil
	}, func(func()) { polled = true })
	if len(watched) != 1 || watched[0] != styles || polled {
		t.Fatalf("watched %v polled %v, want the styles tree watched", watched, polled)
	}
	if err := os.WriteFile(filepath.Join(styles, "index.scss"), []byte(".bar .x { color: red; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := theme.userStamp
	changed()
	if theme.userStamp == before || theme.userStamp != userStylesStamp() {
		t.Error("a watched edit did not reload the stylesheet")
	}

	// No watcher: the styles poll instead.
	theme.watchStyles(func([]string, bool, func()) (func(), error) { return nil, errors.New("no inotify") },
		func(func()) { polled = true })
	if !polled {
		t.Error("a failed watch did not fall back to polling")
	}
}

// spacedWidth is the natural width of a .list-control-button box of two
// 10px spacers, which the bundle pads, under whatever stylesheets reach
// it.
func spacedWidth(attach func(*widget.Box)) int {
	box := widget.NewBox(widget.Row, 0, 0)
	box.AddClass("list-control-button")
	box.Append(widget.NewSpacer(10, 10), false)
	box.Append(widget.NewSpacer(10, 10), false)
	attach(box)
	widget.CollectDamage(box)
	return box.Measure(widget.Constraints{Max: widget.Size{W: 1000, H: 1000}}).W
}

// The bundle styles only the roots it is attached to: reloading a theme
// no root took yet never installs it process-wide over every surface.
func TestReloadStylesOnlyAttachedRoots(t *testing.T) {
	isolateConfigDir(t)
	bare := spacedWidth(func(*widget.Box) {})
	theme := New(config.Defaults())
	theme.Reload()
	if got := spacedWidth(func(*widget.Box) {}); got != bare {
		t.Errorf("an unattached root measures %d after a reload, want its unstyled %d: the bundle went global", got, bare)
	}
	styled := spacedWidth(func(b *widget.Box) { theme.Attach(b) })
	if styled == bare {
		t.Fatalf("an attached root measures its unstyled %d: the bundle does not reach it", bare)
	}
	theme.Reload()
	if got := spacedWidth(func(b *widget.Box) { theme.Attach(b) }); got != styled {
		t.Errorf("an attached root measures %d after a reload, want %d", got, styled)
	}
}
