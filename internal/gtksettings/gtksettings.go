// Package gtksettings reads the user's GTK settings.ini, the file GTK
// apps on the host take their theme, icons, cursor and font from:
// gtk-4.0 first, gtk-3.0 for what it leaves unset.
package gtksettings

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Settings is what a settings.ini sets; a zero field is unset.
type Settings struct {
	ThemeName   string
	IconTheme   string
	CursorTheme string
	CursorSize  uint32
	FontName    string
	// XftDPI is gtk-xft-dpi: the text DPI in 1024ths.
	XftDPI int
}

// Parse reads one settings.ini. Values may be quoted; unknown keys and
// malformed numbers are skipped.
func Parse(text string) Settings {
	var s Settings
	for line := range strings.SplitSeq(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if value == "" {
			continue
		}
		switch strings.TrimSpace(key) {
		case "gtk-theme-name":
			s.ThemeName = value
		case "gtk-icon-theme-name":
			s.IconTheme = value
		case "gtk-cursor-theme-name":
			s.CursorTheme = value
		case "gtk-cursor-theme-size":
			if n, err := strconv.ParseUint(value, 10, 32); err == nil {
				s.CursorSize = uint32(n)
			}
		case "gtk-font-name":
			s.FontName = value
		case "gtk-xft-dpi":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				s.XftDPI = n
			}
		}
	}
	return s
}

// Or fills s's unset fields from other.
func (s Settings) Or(other Settings) Settings {
	if s.ThemeName == "" {
		s.ThemeName = other.ThemeName
	}
	if s.IconTheme == "" {
		s.IconTheme = other.IconTheme
	}
	if s.CursorTheme == "" {
		s.CursorTheme = other.CursorTheme
	}
	if s.CursorSize == 0 {
		s.CursorSize = other.CursorSize
	}
	if s.FontName == "" {
		s.FontName = other.FontName
	}
	if s.XftDPI == 0 {
		s.XftDPI = other.XftDPI
	}
	return s
}

// Load reads configDir/gtk-4.0/settings.ini, then gtk-3.0's for what it
// leaves unset. A missing or unreadable file sets nothing.
func Load(configDir string) Settings {
	read := func(version string) Settings {
		data, err := os.ReadFile(filepath.Join(configDir, version, "settings.ini")) //nolint:gosec // the user's own GTK config
		if err != nil {
			return Settings{}
		}
		return Parse(string(data))
	}
	return read("gtk-4.0").Or(read("gtk-3.0"))
}
