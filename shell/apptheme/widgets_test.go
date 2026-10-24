package apptheme

import (
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// lys-ui's theme takes the palette's colors and the shell's derived
// roles, so its widgets and named colors match the stylesheet.
func TestWidgetThemeIsThePalette(t *testing.T) {
	cfg := config.Defaults()
	p := styling.Default()
	th := WidgetTheme(p, cfg)
	token := func(tok config.CssToken) widget.Color {
		c, _ := p.Token(tok)
		return c
	}
	for name, got := range map[string][2]widget.Color{
		"Bg":             {th.Bg, p.Bg},
		"Surface":        {th.Surface, p.Surface},
		"SurfaceHover":   {th.SurfaceHover, token(config.TokenBgHover)},
		"SurfacePressed": {th.SurfacePressed, token(config.TokenBgActive)},
		"Text":           {th.Text, p.Fg},
		"TextMuted":      {th.TextMuted, p.FgMuted},
		"Accent":         {th.Accent, p.Primary},
		"OnAccent":       {th.OnAccent, token(config.TokenFgOnAccent)},
		"Border":         {th.Border, token(config.TokenBorderDefault)},
		"Card":           {th.Card, p.Elevated},
		"Popover":        {th.Popover, p.Elevated},
		"Muted":          {th.Muted, token(config.TokenBgSurfaceElevated)},
		"Destructive":    {th.Destructive, p.Red},
		"Warning":        {th.Warning, p.Yellow},
		"Success":        {th.Success, p.Green},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %#08x, want the palette's %#08x", name, uint32(got[0]), uint32(got[1]))
		}
	}
	if want := styling.RoundingRadiusPx(cfg.Styling.Rounding, float64(cfg.Styling.Scale)); th.Radius != want {
		t.Errorf("Radius = %d, want the global rounding %d", th.Radius, want)
	}
	if !th.Animations {
		t.Error("animations off with the config's on")
	}
	cfg.Animations.Enabled = false
	if WidgetTheme(p, cfg).Animations {
		t.Error("animations on with the config's off")
	}
}

// The fields the palette does not set follow the background's
// polarity: a light palette takes lys-ui's light chart and shadow
// colors, a dark one the dark.
func TestWidgetThemeBaseFollowsTheBackground(t *testing.T) {
	cfg := config.Defaults()
	dark := styling.Default()
	if got := WidgetTheme(dark, cfg).ShadowColor; got != widget.DarkTheme().ShadowColor {
		t.Errorf("dark palette shadow %#08x, want the dark theme's", uint32(got))
	}
	light := *dark
	light.Bg = render.RGB(0xf5, 0xf5, 0xf5)
	if got := WidgetTheme(&light, cfg).ShadowColor; got != widget.LightTheme().ShadowColor {
		t.Errorf("light palette shadow %#08x, want the light theme's", uint32(got))
	}
}

// Building and reloading the theme install lys-ui's from the resolved
// palette: a config with another palette retints lys-ui's widgets too.
func TestThemeInstallsTheWidgetTheme(t *testing.T) {
	isolateConfigDir(t)
	t.Cleanup(func() { widget.SetTheme(nil) })
	cfg := config.Defaults()
	th := New(cfg)
	if widget.Current().Bg != th.RenderPalette().Bg || widget.Current().Accent != th.RenderPalette().Primary {
		t.Fatal("building the theme left lys-ui's on another palette")
	}
	next := config.Defaults()
	red, err := config.ParseHexColor("#ff0000")
	if err != nil {
		t.Fatal(err)
	}
	next.Styling.Palette.Primary = red
	th.SetConfig(next)
	if widget.Current().Accent != render.RGB(0xff, 0, 0) {
		t.Errorf("a new palette left lys-ui's accent at %#08x", uint32(widget.Current().Accent))
	}
}
