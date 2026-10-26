package portal

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/gtksettings"
	"github.com/stubbedev/lys-shell/internal/intconv"
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
// host's GTK settings.ini, and the session's environment.
type desktop struct {
	cfg    *config.Config
	gtk    gtksettings.Settings
	getenv func(string) string
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
	{AppearanceNS, keyColorScheme, always(func(d desktop) any { return colorScheme(d.cfg.Styling) })},
	{AppearanceNS, keyAccentColor, always(func(d desktop) any { return accentColor(d.cfg.Styling.Palette.Primary) })},
	{AppearanceNS, keyContrast, always(func(d desktop) any { return boolU32(d.cfg.Styling.HighContrast) })},
	{AppearanceNS, keyReducedMotion, always(func(d desktop) any { return boolU32(!d.cfg.Animations.Enabled) })},
	{GnomeInterfaceNS, "color-scheme", always(func(d desktop) any { return gnomeColorScheme(colorScheme(d.cfg.Styling)) })},
	{GnomeInterfaceNS, "gtk-theme", always(gtkTheme)},
	{GnomeInterfaceNS, "icon-theme", nonEmpty(func(d desktop) string { return d.gtk.IconTheme })},
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
	if colorScheme(d.cfg.Styling) == 1 {
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
	return d.gtk.CursorTheme
}

func cursorSize(d desktop) (any, bool) {
	if size, err := strconv.ParseInt(strings.TrimSpace(d.getenv("XCURSOR_SIZE")), 10, 32); err == nil && size > 0 {
		return int32(size), true
	}
	if size, err := intconv.Exact[int32](d.gtk.CursorSize); err == nil && size > 0 {
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
	if s.XftDPI <= 0 {
		return 1
	}
	return float64(s.XftDPI) / 1024 / 96
}

// colorScheme is 0 no preference, 1 dark, 2 light. Auto states no
// preference, so the effective scheme is read off the active palette's
// background luminance, or apps would stay light under a dark palette.
func colorScheme(s config.StylingConfig) uint32 {
	switch s.Appearance {
	case config.AppearanceDark:
		return 1
	case config.AppearanceLight:
		return 2
	}
	bg, err := config.ParseHexColor(s.ActivePalette().Bg)
	if err != nil {
		return 0
	}
	r, g, b := rgb(bg)
	if 0.2126*r+0.7152*g+0.0722*b < 0.5 {
		return 1
	}
	return 2
}

// accentColor is the palette primary as a (ddd) sRGB tuple in [0, 1].
func accentColor(primary config.HexColor) colorTuple {
	r, g, b := rgb(primary)
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

// hostGTKSettings reads the user's settings.ini under XDG_CONFIG_HOME.
func hostGTKSettings() gtksettings.Settings {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return gtksettings.Settings{}
		}
		dir = filepath.Join(home, ".config")
	}
	return gtksettings.Load(dir)
}

func (s *settings) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       SettingsIface,
		Methods:    s,
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

func (s *settings) desktop(cfg *config.Config) desktop {
	return desktop{cfg: cfg, gtk: s.gtk(), getenv: s.getenv}
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
// change moves, and nothing for keys that kept theirs. The returned
// func stops.
func (s *settings) watch(conn *dbus.Conn) (stop func()) {
	return s.cfg.Subscribe(func(old, new *config.Config) {
		before, after := s.desktop(old), s.desktop(new)
		for _, st := range servedSettings {
			was, wasOK := st.read(before)
			now, ok := st.read(after)
			if !ok || wasOK && reflect.DeepEqual(was, now) {
				continue
			}
			_ = conn.Emit(ObjectPath, SettingsIface+".SettingChanged", st.ns, st.key, dbus.MakeVariant(now))
		}
	})
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
