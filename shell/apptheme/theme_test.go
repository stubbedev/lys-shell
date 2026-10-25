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
		t.Error("the bundle does not open with the built-in CSS")
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
	// Building a theme writes nothing (the shell scaffolds at startup),
	// and a user rule lands last.
	if _, err := os.Stat(filepath.Join(dir, "styles")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("building a theme touched the config dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "styles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "styles", "index.css"), []byte(".bar .x { color: red; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := theme.Bundle(); !strings.HasSuffix(b, "\n.bar .x { color: red; }") {
		t.Errorf("the user CSS is not the bundle tail: %q", b[max(0, len(b)-80):])
	}
	// A failing user stylesheet drops only the user part.
	if err := os.WriteFile(filepath.Join(dir, "styles", "index.scss"), []byte(".bar { .x { color: red; } }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := theme.Bundle(); !strings.HasPrefix(b, styling.StaticCSS) || strings.Contains(b, ".bar .x") {
		t.Error("a rejected user stylesheet broke the bundle or still applied")
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
	if err := os.WriteFile(filepath.Join(styles, "index.css"), []byte("a {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := userStylesStamp()
	for _, other := range []string{"notes.txt", "other.css"} {
		if err := os.WriteFile(filepath.Join(styles, other), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if userStylesStamp() != first {
		t.Error("a file UserCSS never reads moved the stamp")
	}
	if err := os.WriteFile(filepath.Join(styles, "index.css"), []byte("a { b: c; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := userStylesStamp()
	if edited == first {
		t.Error("an index.css edit did not move the stamp")
	}
	if err := os.WriteFile(filepath.Join(styles, "index.scss"), []byte("a {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if userStylesStamp() == edited {
		t.Error("a legacy index.scss appearing did not move the stamp")
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
		if recursive {
			t.Error("the styles directory is watched recursively; only its top-level files load")
		}
		return func() {}, nil
	}, func(func()) { polled = true })
	if len(watched) != 1 || watched[0] != styles || polled {
		t.Fatalf("watched %v polled %v, want the styles directory watched", watched, polled)
	}
	if err := os.WriteFile(filepath.Join(styles, "index.css"), []byte(".bar .x { color: red; }"), 0o600); err != nil {
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

// spacedWidth is the natural width of a .yaml-editor box of two 10px
// spacers, which the bundle pads, under whatever stylesheets
// reach it.
func spacedWidth(attach func(*widget.Box)) int {
	box := widget.NewBox(widget.Row, 0, 0)
	box.AddClass("yaml-editor")
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
