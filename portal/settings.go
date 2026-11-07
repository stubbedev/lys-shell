package portal

import (
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/gtksettings"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/styling"
)

// SettingsIface serves org.freedesktop.appearance from the live styling
// config, so GTK, Qt and Chromium follow lys-shell's theme, and the
// org.gnome.desktop.interface keys a sandboxed GTK app reads its theme,
// icons, cursor and font from instead of the host's settings.ini.
const SettingsIface = "org.freedesktop.impl.portal.Settings"

// AppearanceNS is the cross-desktop namespace every toolkit reads.
const AppearanceNS = "org.freedesktop.appearance"

// GnomeInterfaceNS is the namespace GTK's portal settings backend reads
// its toolkit settings from.
const GnomeInterfaceNS = "org.gnome.desktop.interface"

// errNotFound is the portal's error for an unknown namespace or key.
const errNotFound = "org.freedesktop.portal.Error.NotFound"

// The served appearance keys.
const (
	keyColorScheme   = "color-scheme"
	keyAccentColor   = "accent-color"
	keyContrast      = "contrast"
	keyReducedMotion = "reduced-motion"
)

// desktop is what a setting is read from: the effective config, the
// palette its theme provider resolves to, the host's GTK settings.ini,
// and the session's environment.
type desktop struct {
	cfg     *config.Config
	palette config.Palette
	gtk     gtksettings.Settings
	getenv  func(string) string
}

// setting is one served key; read reports false when there is nothing
// to serve for it right now (the host sets no icon theme).
type setting struct {
	ns, key string
	read    func(desktop) (any, bool)
}

func always(read func(desktop) any) func(desktop) (any, bool) {
	return func(d desktop) (any, bool) { return read(d), true }
}

// servedSettings is every key in ReadAll's order.
var servedSettings = []setting{
	{AppearanceNS, keyColorScheme, always(func(d desktop) any { return colorScheme(d) })},
	{AppearanceNS, keyAccentColor, always(func(d desktop) any { return accentColor(d.palette) })},
	{AppearanceNS, keyContrast, always(func(d desktop) any { return boolU32(d.cfg.Styling.HighContrast) })},
	{AppearanceNS, keyReducedMotion, always(func(d desktop) any { return boolU32(reducedMotion(d)) })},
	{GnomeInterfaceNS, "color-scheme", always(func(d desktop) any { return gnomeColorScheme(colorScheme(d)) })},
	{GnomeInterfaceNS, "enable-animations", always(func(d desktop) any { return !reducedMotion(d) })},
	{GnomeInterfaceNS, "gtk-theme", always(gtkTheme)},
	{GnomeInterfaceNS, "icon-theme", nonEmpty(func(d desktop) string { return d.gtk.IconThemeName })},
	{GnomeInterfaceNS, "cursor-theme", nonEmpty(cursorTheme)},
	{GnomeInterfaceNS, "cursor-size", cursorSize},
	{GnomeInterfaceNS, "font-name", always(func(d desktop) any { return fontName(d) })},
	{GnomeInterfaceNS, "text-scaling-factor", always(func(d desktop) any { return textScale(d.gtk) })},
}

func nonEmpty(read func(desktop) string) func(desktop) (any, bool) {
	return func(d desktop) (any, bool) {
		s := read(d)
		return s, s != ""
	}
}

// reducedMotion is what the compositor says when it owns the setting,
// else the shell's animations off or reduced; and either way the host's
// gtk-enable-animations off, which sandboxed apps cannot read.
func reducedMotion(d desktop) bool {
	a := d.cfg.Animations
	return a.Reduced() || (!a.CompositorOwnsMotion() && !a.Enabled) || d.gtk.Animations == gtksettings.ToggleOff
}

// gnomeColorScheme is the GSettings enum for a color-scheme value.
func gnomeColorScheme(scheme uint32) string {
	switch scheme {
	case 1:
		return "prefer-dark"
	case 2:
		return "prefer-light"
	}
	return "default"
}

// gtkTheme is the host's GTK theme when settings.ini names one, else
// Adwaita in the shell's light or dark, so GTK3 apps (which ignore
// color-scheme) follow the palette.
func gtkTheme(d desktop) any {
	if d.gtk.ThemeName != "" {
		return d.gtk.ThemeName
	}
	if colorScheme(d) == 1 {
		return "Adwaita-dark"
	}
	return "Adwaita"
}

// cursorTheme is the session's XCURSOR_THEME (what the compositor
// draws), else settings.ini's.
func cursorTheme(d desktop) string {
	if theme := d.getenv("XCURSOR_THEME"); theme != "" {
		return theme
	}
	return d.gtk.CursorThemeName
}

func cursorSize(d desktop) (any, bool) {
	if size, err := strconv.ParseInt(strings.TrimSpace(d.getenv("XCURSOR_SIZE")), 10, 32); err == nil && size > 0 {
		return int32(size), true
	}
	if size, err := intconv.Exact[int32](d.gtk.CursorThemeSize); err == nil && size > 0 {
		return size, true
	}
	return nil, false
}

// fontName is settings.ini's gtk-font-name, else the shell's sans font
// at GTK's default size.
func fontName(d desktop) string {
	if d.gtk.FontName != "" {
		return d.gtk.FontName
	}
	return d.cfg.General.FontSans + " 10"
}

// textScale is gtk-xft-dpi over GTK's 96 DPI baseline, 1 without one.
func textScale(s gtksettings.Settings) float64 {
	dpi := s.XftDPI.DotsPerInch()
	if dpi <= 0 {
		return 1
	}
	return dpi / 96
}

// colorScheme is 0 no preference, 1 dark, 2 light. Auto states no
// preference, so the effective scheme is read off the resolved palette's
// background luminance, or apps would stay light under a dark palette.
func colorScheme(d desktop) uint32 {
	switch d.cfg.Styling.Appearance {
	case config.AppearanceDark:
		return 1
	case config.AppearanceLight:
		return 2
	}
	r, g, b := rgb(mustHex(d.palette.Bg))
	if float64(0.2126*r)+float64(0.7152*g)+float64(0.0722*b) < 0.5 {
		return 1
	}
	return 2
}

// accentColor is the palette primary as a (ddd) sRGB tuple in [0, 1].
func accentColor(p config.Palette) colorTuple {
	r, g, b := rgb(mustHex(p.Primary))
	return colorTuple{r, g, b}
}

// colorTuple is an sRGB color as the spec's (ddd) structure.
type colorTuple struct{ R, G, B float64 }

func rgb(c config.HexColor) (r, g, b float64) {
	r8, g8, b8, _ := c.RGBA()
	return float64(r8) / 255, float64(g8) / 255, float64(b8) / 255
}

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// settings carries the interface's D-Bus methods: the live config,
// and the host's GTK settings and environment.
type settings struct {
	cfg    *config.Service
	gtk    func() gtksettings.Settings
	getenv func(string) string
}

// hostGTKSettings reads the settings.ini files GTK 4 reads, logging one
// it could not.
func hostGTKSettings() gtksettings.Settings {
	s, err := gtksettings.Load(xdg.OS)
	if err != nil {
		log.Printf("settings: %v", err)
	}
	return s
}

func (s *settings) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       SettingsIface,
		Methods:    s,
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

func (s *settings) desktop(cfg *config.Config) desktop {
	return desktop{cfg: cfg, palette: resolvedPalette(cfg.Styling), gtk: s.gtk(), getenv: s.getenv}
}

// resolvedPalette is the palette the shell draws with: its theme
// provider's, so apps follow the wallpaper too. A provider palette that
// cannot be read or holds a color that does not parse is logged and the
// configured palette is served, as the shell draws it then.
func resolvedPalette(s config.StylingConfig) config.Palette {
	configured := s.ActivePalette()
	p, err := styling.ResolvePalette(configured, s)
	if err != nil {
		log.Printf("settings: %v", err)
	}
	if _, err := styling.PaletteFromHex(p); err != nil {
		log.Printf("settings: %v", err)
		return configured
	}
	return p
}

// mustHex parses a color resolvedPalette already validated.
func mustHex(s string) config.HexColor {
	c, err := config.ParseHexColor(s)
	if err != nil {
		panic("settings: an unvalidated palette color " + s)
	}
	return c
}

// ReadAll returns every served namespace one of namespaces selects:
// an exact name, a trailing glob ("org.freedesktop.*"), or "" (as an
// empty list does) for all.
func (s *settings) ReadAll(namespaces []string) (map[string]Vardict, *dbus.Error) {
	out := map[string]Vardict{}
	d := s.desktop(s.cfg.Config())
	for _, st := range servedSettings {
		if !namespaceMatches(namespaces, st.ns) {
			continue
		}
		v, ok := st.read(d)
		if !ok {
			continue
		}
		if out[st.ns] == nil {
			out[st.ns] = Vardict{}
		}
		out[st.ns][st.key] = dbus.MakeVariant(v)
	}
	return out, nil
}

// Read reads one key of one namespace; an unknown one is NotFound.
func (s *settings) Read(namespace, key string) (dbus.Variant, *dbus.Error) {
	d := s.desktop(s.cfg.Config())
	for _, st := range servedSettings {
		if st.ns != namespace || st.key != key {
			continue
		}
		if v, ok := st.read(d); ok {
			return dbus.MakeVariant(v), nil
		}
	}
	return dbus.Variant{}, dbus.NewError(errNotFound, []any{"unknown setting " + namespace + " " + key})
}

// watch emits SettingChanged for every served key whose value a config
// change or a finished wallpaper color extraction (the shell's
// ColorsExtracted) moves, and nothing for keys that kept theirs. The
// returned func stops.
func (s *settings) watch(conn *dbus.Conn) (stop func()) {
	var mu sync.Mutex
	last := s.desktop(s.cfg.Config())
	update := func(cfg *config.Config) {
		mu.Lock()
		defer mu.Unlock()
		next := s.desktop(cfg)
		for _, st := range servedSettings {
			was, wasOK := st.read(last)
			now, ok := st.read(next)
			if !ok || wasOK && reflect.DeepEqual(was, now) {
				continue
			}
			_ = conn.Emit(ObjectPath, SettingsIface+".SettingChanged", st.ns, st.key, dbus.MakeVariant(now))
		}
		last = next
	}
	unsubscribe := s.cfg.Subscribe(func(_, cfg *config.Config) { update(cfg) })
	stopExtracted, err := watchColorsExtracted(conn, func() { update(s.cfg.Config()) })
	if err != nil {
		warnf("settings: the accent color will not follow the wallpaper: %v", err)
		stopExtracted = func() {}
	}
	return func() {
		stopExtracted()
		unsubscribe()
	}
}

// watchColorsExtracted runs extracted for every ColorsExtracted the
// shell's wallpaper service emits; the returned func stops.
func watchColorsExtracted(conn *dbus.Conn, extracted func()) (stop func(), err error) {
	match := []dbus.MatchOption{
		dbus.WithMatchInterface(wallpaper.Interface),
		dbus.WithMatchMember("ColorsExtracted"),
		dbus.WithMatchObjectPath(wallpaper.ServicePath),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return nil, fmt.Errorf("subscribing to %s.ColorsExtracted: %w", wallpaper.Interface, err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case sig := <-signals:
				if sig == nil {
					return
				}
				if sig.Name == wallpaper.Interface+".ColorsExtracted" && sig.Path == wallpaper.ServicePath {
					extracted()
				}
			}
		}
	}()
	return func() {
		close(quit)
		<-done
		_ = conn.RemoveMatchSignal(match...)
		conn.RemoveSignal(signals)
	}, nil
}

// namespaceMatches reports whether ns is selected by the requested
// namespaces: all when none (or "") is named, else an exact name or a
// trailing-section glob like "org.freedesktop.*".
func namespaceMatches(namespaces []string, ns string) bool {
	if len(namespaces) == 0 {
		return true
	}
	for _, want := range namespaces {
		if want == "" || want == ns {
			return true
		}
		if prefix, ok := strings.CutSuffix(want, "*"); ok && strings.HasPrefix(ns, prefix) {
			return true
		}
	}
	return false
}
