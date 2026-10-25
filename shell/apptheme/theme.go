// Package apptheme is what every lys-shell GTK-era surface loads at start
// (bootstrap.rs, lys-settings app/css.rs): the Rust stylesheet
// bundle with its hot reload, the stylesheet font resolver, and the
// lys-shell icon registry on the icon search path.
package apptheme

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// Priority is the bundle's provider priority: bootstrap.rs loads it at
// STYLE_PROVIDER_PRIORITY_USER + 100, above the per-widget variable
// providers (STYLE_PROVIDER_PRIORITY_USER).
const Priority = widget.StylePriorityUser + 100

// ComponentPriority is a widget's own dynamic CSS - an album art or
// avatar background - above the bundle, so no bundle
// rule overrides it (wayle-styling's COMPONENT_CSS_PRIORITY, GTK
// priority 1000). A widget's plain inline
// style sits at user priority, under the bundle, as a GTK per-widget
// provider does.
const ComponentPriority = widget.StylePriorityUser + 200

// Theme owns the stylesheet: the bundle — the built-in CSS, the
// theme's :root variables, and the user's styles/index.css — attached
// to the roots that take it (the Go-painted surfaces keep their
// programmatic styling), plus the resolved palette the Go-painted
// surfaces read.
type Theme struct {
	cfg     *config.Config
	Sheet   *widget.Stylesheet
	palette config.Palette
	// scope keeps Sheet attached from the first reload on: lys-ui
	// installs a stylesheet loaded with no attachment process-wide, so
	// a reload before any root took the bundle, or after every root
	// went away, would style the Go-painted surfaces too.
	scope *widget.Box
	// userStamp fingerprints the user styles tree for the hot-reload
	// poll.
	userStamp string
}

// New resolves the palette, compiles the bundle and installs lys-ui's
// theme from the palette (WidgetTheme). A provider or user-stylesheet
// failure logs and falls back (the configured palette, no user CSS),
// the Rust shell's behavior.
func New(cfg *config.Config) *Theme {
	t := &Theme{cfg: cfg}
	t.Sheet = widget.NewStylesheet(t.Bundle(), Priority)
	t.installWidgetTheme()
	t.userStamp = userStylesStamp()
	return t
}

// installWidgetTheme gives lys-ui's widgets the resolved palette.
func (t *Theme) installWidgetTheme() {
	InstallWidgetTheme(t.RenderPalette(), t.cfg)
}

// Bundle builds init_css_provider's CSS (bootstrap.rs): static, theme,
// the [animations] overrides (the --duration-* tokens and, with
// indicators off, the frozen looping animations), then user styles,
// resolving the palette on the way.
func (t *Theme) Bundle() string {
	palette, err := styling.ResolvePalette(t.cfg.Styling.ActivePalette(), t.cfg.Styling)
	if err != nil {
		log.Printf("styling: %v", err)
	}
	t.palette = palette
	user := ""
	if dir, err := config.Dir(); err == nil {
		user = styling.UserCSS(dir)
	} else {
		log.Printf("styling: cannot resolve config dir; user styles disabled: %v", err)
	}
	return styling.BundleCSS(t.cfg, palette, t.RenderPalette()) + "\n" + t.cfg.Animations.CSSOverrides() + "\n" + user
}

// Reload recompiles the bundle into the attached stylesheet and
// reinstalls lys-ui's theme: every root restyles on the next frame.
func (t *Theme) Reload() {
	if t.scope == nil {
		t.scope = widget.NewBox(widget.Row, 0, 0)
		t.scope.AttachStylesheet(t.Sheet)
	}
	t.Sheet.Load(t.Bundle())
	t.installWidgetTheme()
}

// SetConfig recompiles the bundle for a new config snapshot.
func (t *Theme) SetConfig(cfg *config.Config) {
	t.cfg = cfg
	t.Reload()
}

// RenderPalette is the resolved palette as render colors, for the
// surfaces the stylesheet does not reach (OSD, popups, dropdowns).
func (t *Theme) RenderPalette() *styling.Palette {
	p, err := styling.PaletteFromHex(t.palette)
	if err != nil {
		log.Printf("styling: palette: %v", err)
		return styling.Default()
	}
	return p
}

// FollowDesktopFontSize makes 1rem what it is in a GTK app on this
// desktop - the desktop font's size, GTK's "Sans 10" without one - now
// and on every change: the bundle sizes its text in rem, and every
// Rust surface resolved it through GTK.
func FollowDesktopFontSize(a *app.Application) {
	widget.SetRootFontSize(a.DesktopFontSize())
	a.OnDesktopFontSizeChange(widget.SetRootFontSize)
}

// Attach styles one root.
func (t *Theme) Attach(root interface{ AttachStylesheet(*widget.Stylesheet) }) {
	root.AttachStylesheet(t.Sheet)
}

// WatchUserStyles reloads the bundle when the user stylesheet changed:
// the styles directory is watched on the loop (app.WatchFiles), and the
// stamp filters out changes to other files. Without inotify it polls
// once a second. A failed read keeps the stylesheet running: UserCSS
// logs and contributes nothing.
func (t *Theme) WatchUserStyles(application *app.Application) {
	t.watchStyles(application.WatchFiles, func(fn func()) { application.Every(time.Second, fn) })
}

// watchStyles is WatchUserStyles over its two mechanisms.
func (t *Theme) watchStyles(watch func(paths []string, recursive bool, fn func()) (func(), error), poll func(fn func())) {
	check := func() {
		if stamp := userStylesStamp(); stamp != t.userStamp {
			t.userStamp = stamp
			t.Reload()
		}
	}
	if dir, err := config.Dir(); err == nil {
		if styles, ok := styling.UserStylesDir(dir); ok {
			if _, err := watch([]string{styles}, false, check); err == nil {
				return
			}
		}
	}
	poll(check)
}

// userStylesStamp fingerprints the files UserCSS reads, the stylesheet
// and the legacy SCSS entry it rejects, by path, size, and modification
// time; empty when there is neither.
func userStylesStamp() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	styles, ok := styling.UserStylesDir(dir)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, name := range []string{styling.UserStylesFile, styling.LegacyUserStylesFile} {
		info, err := os.Stat(filepath.Join(styles, name))
		if err != nil {
			continue
		}
		b.WriteString(name)
		b.WriteByte(0)
		b.WriteString(info.ModTime().String())
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte(0)
	}
	return b.String()
}

// faceKey keys the resolved-face cache.
type faceKey struct {
	family string
	weight int
}

// faceCache memoizes FontResolver: a label compares faces by identity,
// so one family and weight must resolve to one face (loop goroutine
// only, like every style read).
var faceCache = map[faceKey]render.Font{}

// FontResolver resolves the stylesheet's font-family and font-weight
// declarations through the system font store, so the bundle's
// --weight-* tokens and a user's font-family take effect. A family the
// store lacks declines, keeping the label's own face.
func FontResolver(family string, weight int) (render.Font, bool) {
	if weight == 0 {
		weight = 400
	}
	key := faceKey{family, weight}
	if f, ok := faceCache[key]; ok {
		return f, f != nil
	}
	face, err := app.FontWeighted(family, 14, weight, false)
	if err != nil {
		faceCache[key] = nil
		return nil, false
	}
	f := render.Font(app.FontFallback(face))
	faceCache[key] = f
	return f, true
}
