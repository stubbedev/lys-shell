package apptheme

import (
	"sync"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// WidgetTheme is lys-ui's theme built from the resolved palette, so
// every lys-ui widget and named color (@window_bg_color,
// @accent_bg_color, @borders, ...) paints from the same colors as the
// shell's stylesheet: the palette's own slots where lys-ui has one, the
// design tokens where the shell derives the role (hover and pressed
// fills, elevated surfaces, borders), the status colors, the global
// rounding at the global scale, and the animation switch. What the
// palette does not cover (chart series, shadows) comes from lys-ui's
// dark or light theme, whichever the background is.
func WidgetTheme(p *styling.Palette, cfg *config.Config) *widget.Theme {
	token := func(t config.CssToken) widget.Color {
		c, _ := p.Token(t)
		return c
	}
	t := widget.DarkTheme()
	if isLight(p.Bg) {
		t = widget.LightTheme()
	}
	t.Bg = p.Bg
	t.Surface = p.Surface
	t.SurfaceHover = token(config.TokenBgHover)
	t.SurfacePressed = token(config.TokenBgActive)
	t.Secondary = token(config.TokenBgOverlay)
	t.Text = p.Fg
	t.TextMuted = p.FgMuted
	t.Accent = p.Primary
	t.OnAccent = token(config.TokenFgOnAccent)
	t.Border = token(config.TokenBorderDefault)
	t.Input = token(config.TokenBorderDefault)
	t.Ring = p.Primary
	t.Card = p.Elevated
	t.Popover = p.Elevated
	t.Muted = token(config.TokenBgSurfaceElevated)
	t.Sidebar = p.Surface
	t.Destructive = p.Red
	t.Error = p.Red
	t.Warning = p.Yellow
	t.Success = p.Green
	t.Radius = styling.RoundingRadiusPx(cfg.Styling.Rounding, float64(cfg.Styling.Scale))
	t.Animations = cfg.Animations.Enabled
	return t
}

// isLight reports a background bright enough for dark text: its
// Rec. 709 luma above the midpoint.
func isLight(c widget.Color) bool {
	luma := 0.2126*float64(c.R()) + 0.7152*float64(c.G()) + 0.0722*float64(c.B())
	return luma > 127.5
}

// plainButtonCSS rests a plain button transparent. It sits at lys-ui's
// theme priority, where specificity decides: every variant (.secondary,
// .ghost, ...) and every widget's state rule (a sidebar item while
// checked) outranks it, where the shell's own stylesheet, above the
// theme, would override them all.
const plainButtonCSS = "button { background-color: transparent; }"

var plainButtons sync.Once

// InstallWidgetTheme installs WidgetTheme(p, cfg) and, the first time,
// the shell's theme-level button rule.
func InstallWidgetTheme(p *styling.Palette, cfg *config.Config) {
	widget.SetTheme(WidgetTheme(p, cfg))
	plainButtons.Do(func() { widget.AddStylesheet(plainButtonCSS, widget.StylePriorityTheme) })
}
