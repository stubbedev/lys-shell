package apptheme

import (
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

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
		"Secondary":      {th.Secondary, token(config.TokenBgOverlay)},
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
	if want := widget.DarkTheme().Radius; th.Radius != want {
		t.Errorf("Radius = %d with the default rounding and scale, want lys-ui's %d", th.Radius, want)
	}
	cfg.Styling.Rounding = config.RoundingLg
	cfg.Styling.Scale = 2
	if got, want := WidgetTheme(p, cfg).Radius, styling.RoundingRadiusPx(config.RoundingLg, 2); got != want || got == widget.DarkTheme().Radius {
		t.Errorf("Radius = %d with rounding lg at scale 2, want the override %d", got, want)
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

// The shell sheet leaves lys-ui's button variants their fills: a
// suggested or destructive action paints the palette's primary or
// error, a secondary one the control fill that sets it off the panel
// surface, over the plain buttons' transparent rest.
func TestShellSheetKeepsTheButtonVariantFills(t *testing.T) {
	isolateConfigDir(t)
	t.Cleanup(func() { widget.SetTheme(nil) })
	theme := New(config.Defaults())
	p := theme.RenderPalette()
	errColor, ok := p.Token(config.TokenStatusError)
	if !ok {
		t.Fatal("the palette has no status-error")
	}
	overlay, ok := p.Token(config.TokenBgOverlay)
	if !ok || overlay == p.Surface {
		t.Fatal("the palette's control fill is missing or the panel surface")
	}
	button := func(class string) *widget.Button {
		b := widget.NewButton(widget.NewSpacer(40, 40), 0, 0)
		b.AddClass(class)
		return b
	}
	plain, suggested, destructive, secondary := button("plain"), button(css.SuggestedAction), button(css.DestructiveAction), button(css.Secondary)
	root := widget.NewBox(widget.Column, 0, 0)
	for _, b := range []*widget.Button{plain, suggested, destructive, secondary} {
		root.Append(b, false)
	}
	theme.Attach(root)
	const w, h = 200, 300
	widget.SizeWithin(root, widget.Size{W: w, H: h})
	root.Arrange(render.Rect{W: w, H: h})
	data := make([]byte, render.Stride(w)*h)
	root.Paint(render.NewScaled(data, render.Stride(w), w, h, 1, 1))
	center := func(b *widget.Button) (widget.Color, byte) {
		r := b.Bounds()
		i := (r.Y+r.H/2)*render.Stride(w) + (r.X+r.W/2)*4
		return render.RGB(data[i+2], data[i+1], data[i]), data[i+3]
	}
	if _, alpha := center(plain); alpha != 0 {
		t.Errorf("a plain button paints alpha %d at rest, want transparent", alpha)
	}
	if got, _ := center(suggested); got != p.Primary {
		t.Errorf("a suggested action paints %#08x, want the primary %#08x", uint32(got), uint32(p.Primary))
	}
	if got, alpha := center(destructive); !nearPremultiplied(got, alpha, errColor, destructiveAlpha) {
		t.Errorf("a destructive action paints %#08x at alpha %d, want status-error %#08x at lys-ui's dark %d", uint32(got), alpha, uint32(errColor), destructiveAlpha)
	}
	if got, _ := center(secondary); got != overlay {
		t.Errorf("a secondary button paints %#08x, want the control fill %#08x", uint32(got), uint32(overlay))
	}
}

// destructiveAlpha is lys-ui's dark-theme destructive fill, 60% of the
// destructive color.
const destructiveAlpha = 153

// nearPremultiplied reports whether a pixel painted over transparency
// (premultiplied rgb, alpha) is c at alpha a, within rounding.
func nearPremultiplied(rgb widget.Color, alpha byte, c widget.Color, a byte) bool {
	near := func(x, y int) bool { return x-y <= 1 && y-x <= 1 }
	scale := func(v uint8) int { return (int(v)*int(a) + 127) / 255 }
	return near(int(alpha), int(a)) &&
		near(int(rgb.R()), scale(c.R())) && near(int(rgb.G()), scale(c.G())) && near(int(rgb.B()), scale(c.B()))
}
