package styling

import (
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// highContrastDark is the dark counterpart of lys-ui's high-contrast
// theme: white on black, a yellow accent, and status colors that keep
// WCAG AA against every surface the tokens derive.
var highContrastDark = config.Palette{
	Bg: "#000000", Surface: "#000000", Elevated: "#000000",
	Fg: "#ffffff", FgMuted: "#ffffff", Primary: "#ffd60a",
	Red: "#ff8a80", Yellow: "#ffe066", Green: "#69f0ae", Blue: "#8ab4ff",
}

// HighContrastPalette is the shell's high-contrast palette: lys-ui's
// HighContrastTheme for light, its inverse for dark.
func HighContrastPalette(dark bool) config.Palette {
	if dark {
		return highContrastDark
	}
	t := widget.HighContrastTheme()
	hex := widget.FormatColor
	return config.Palette{
		Bg: hex(t.Bg), Surface: hex(t.Surface), Elevated: hex(t.Surface),
		Fg: hex(t.Text), FgMuted: hex(t.TextMuted), Primary: hex(t.Accent),
		Red: "#b00020", Yellow: "#6b4e00", Green: "#00632c", Blue: hex(t.Accent),
	}
}

// ShellPalette is the palette the shell draws with, in hex and as render
// colors: the provider's (ResolvePalette over the active palette), or
// with high contrast on the high-contrast palette in the provider's
// lightness (or the forced appearance). A provider failure keeps the
// configured palette and an unparsable palette the compiled Default;
// either failure is returned for the caller to log, and the palette is
// always usable.
func ShellPalette(s config.StylingConfig) (config.Palette, *Palette, error) {
	hex, resolveErr := ResolvePalette(s.ActivePalette(), s)
	if s.HighContrast {
		light, forced := s.Appearance.ForcedLight()
		if !forced {
			light = isLightHex(hex.Bg)
		}
		hex = HighContrastPalette(!light)
	}
	p, err := PaletteFromHex(hex)
	if err != nil {
		return hex, Default(), err
	}
	p.HighContrast = s.HighContrast
	return hex, p, resolveErr
}

// isLightHex reports a background bright enough for dark text; an
// unparsable one counts as dark.
func isLightHex(bg string) bool {
	c, ok := parseHex(bg)
	if !ok {
		return false
	}
	luma := float64(0.2126*float64(c.R())) + float64(0.7152*float64(c.G())) + float64(0.0722*float64(c.B()))
	return luma > 127.5
}
