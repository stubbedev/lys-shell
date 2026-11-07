package styling

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling/material"
)

func argb(c render.Color) material.ARGB {
	s := c.Straight()
	return material.RGB(s[0], s[1], s[2])
}

// over composites a token that may be translucent over the base it is
// painted on.
func over(c, base render.Color) render.Color {
	if c.A() == 0xff {
		return c
	}
	ch := func(a, b uint8) uint8 { return uint8(uint32(a) + (uint32(b)*(255-uint32(c.A()))+127)/255) }
	bs := base.Straight()
	return render.RGB(ch(c.R(), bs[0]), ch(c.G(), bs[1]), ch(c.B(), bs[2]))
}

func TestHighContrastPalettesMeetWCAGAAOnEverySurface(t *testing.T) {
	texts := []config.CssToken{
		config.TokenFgDefault, config.TokenFgMuted, config.TokenFgSubtle, config.TokenAccent,
		config.TokenStatusError, config.TokenStatusWarning, config.TokenStatusSuccess, config.TokenStatusInfo,
		config.TokenRed, config.TokenYellow, config.TokenGreen, config.TokenBlue,
	}
	surfaces := []config.CssToken{
		config.TokenBgBase, config.TokenBgSurface, config.TokenBgSurfaceElevated, config.TokenBgElevated,
		config.TokenBgOverlay, config.TokenBgHover, config.TokenBgActive, config.TokenBgSelected,
	}
	for _, dark := range []bool{false, true} {
		p, err := PaletteFromHex(HighContrastPalette(dark))
		if err != nil {
			t.Fatal(err)
		}
		p.HighContrast = true
		for _, bgTok := range surfaces {
			bg, _ := p.Token(bgTok)
			bg = over(bg, p.Bg)
			for _, fgTok := range texts {
				fg, _ := p.Token(fgTok)
				if r := material.ContrastRatio(argb(over(fg, bg)), argb(bg)); r < 4.5 {
					t.Errorf("dark=%t: %s on %s is %.2f:1, below AA", dark, fgTok, bgTok, r)
				}
			}
		}
		accent, _ := p.Token(config.TokenAccent)
		onAccent, _ := p.Token(config.TokenFgOnAccent)
		if r := material.ContrastRatio(argb(onAccent), argb(accent)); r < 4.5 {
			t.Errorf("dark=%t: text on the accent is %.2f:1", dark, r)
		}
		for _, tok := range []config.CssToken{config.TokenBorderSubtle, config.TokenBorderDefault, config.TokenBorderStrong} {
			if c, _ := p.Token(tok); c != p.Fg {
				t.Errorf("dark=%t: %s is %v, want the solid text color", dark, tok, c)
			}
		}
	}
}

func TestShellPaletteSwitchesToHighContrastInTheProvidersLightness(t *testing.T) {
	s := config.DefaultsStyling()
	normal, rendered, err := ShellPalette(s)
	if err != nil || rendered.HighContrast || normal != s.ActivePalette() {
		t.Fatalf("off: %+v, high contrast %t, %v", normal, rendered.HighContrast, err)
	}
	s.HighContrast = true
	hc, rendered, err := ShellPalette(s)
	if err != nil || !rendered.HighContrast || hc != HighContrastPalette(true) {
		t.Errorf("on over the dark default: %+v, %v; want the dark high-contrast palette", hc, err)
	}
	s.Appearance = config.AppearanceLight
	if hc, _, _ := ShellPalette(s); hc != HighContrastPalette(false) {
		t.Errorf("on with light forced: %+v, want the light high-contrast palette", hc)
	}
	tokens := TokensCSS(rendered)
	if !strings.Contains(tokens, "--border-default: #ffffff;") {
		t.Errorf("high-contrast tokens carry no solid border:\n%s", tokens)
	}
}

func TestReduceTransparencyMakesEverySurfaceOpaqueAndOffRestoresIt(t *testing.T) {
	cfg := config.Defaults()
	palette := cfg.Styling.ActivePalette()
	translucent := []string{
		BarCSS(cfg.Bar, cfg.Styling),
		ThemeCSS(palette, cfg.General, cfg.Bar, cfg.Styling),
		NotificationCSS(palette, cfg.Notification, cfg.Styling),
	}
	for _, mode := range []string{"reduce-transparency", "high-contrast"} {
		s := cfg.Styling
		if mode == "high-contrast" {
			s.HighContrast = true
		} else {
			s.ReduceTransparency = true
		}
		if s.Translucent() {
			t.Fatalf("%s: surfaces still translucent", mode)
		}
		if got := BarCSS(cfg.Bar, s); !strings.Contains(got, "--bar-opacity: 100%;") {
			t.Errorf("%s: bar %s", mode, got)
		}
		if got := ThemeCSS(palette, cfg.General, cfg.Bar, s); !strings.Contains(got, ", 1.00);") {
			t.Errorf("%s: dropdown surface not opaque:\n%s", mode, got)
		}
		if got := NotificationCSS(palette, cfg.Notification, s); !strings.Contains(got, ", 1.00);") {
			t.Errorf("%s: popup surface not opaque: %s", mode, got)
		}
	}
	if cfg.Bar.DropdownOpacity == 100 || cfg.Notification.PopupOpacity == 100 {
		t.Fatal("the defaults are opaque; the test proves nothing")
	}
	again := []string{
		BarCSS(cfg.Bar, cfg.Styling),
		ThemeCSS(palette, cfg.General, cfg.Bar, cfg.Styling),
		NotificationCSS(palette, cfg.Notification, cfg.Styling),
	}
	if fmt.Sprint(again) != fmt.Sprint(translucent) {
		t.Error("with both off the configured opacities do not come back")
	}
	if !strings.Contains(translucent[2], fmt.Sprintf(", %.2f);", float64(cfg.Notification.PopupOpacity)/100)) {
		t.Errorf("popup opacity %d not shown: %s", cfg.Notification.PopupOpacity, translucent[2])
	}
}
