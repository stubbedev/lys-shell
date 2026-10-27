package styling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// The testdata/theme_*.css goldens are the output of the Rust
// theme_css for the same inputs, captured from a probe test in
// crates/wayle-styling.
func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCSS(t *testing.T, name, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gl), len(wl)) {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Fatalf("%s: line %d\n got: %q\nwant: %q", name, i+1, g, w)
		}
	}
	t.Fatalf("%s: output differs from the golden", name)
}

func TestThemeCSSMatchesRustGoldens(t *testing.T) {
	d := config.Defaults()
	latte, _ := config.PaletteByName("catppuccin-latte")

	px := d.Bar
	px.ButtonIconSize = config.Size{Value: 24, Unit: config.SizePixels}
	px.ButtonLabelPadding = config.Size{Value: 3.5, Unit: config.SizePixels}
	px.ButtonGap = config.Size{Value: 1.25, Unit: config.SizeMultiplier}
	px.Scale = 1.3

	rounding := d.Bar
	rounding.Rounding = config.RoundingLg
	rounding.ButtonRounding = config.RoundingNone
	rounding.ButtonGroupRounding = config.RoundingMd
	rounding.DropdownOpacity = 85
	roundingStyling := d.Styling
	roundingStyling.Rounding = config.RoundingFull
	roundingStyling.Scale = 0.9

	opacity := d.Bar
	opacity.DropdownOpacity = 33

	for _, tc := range []struct {
		name    string
		palette config.Palette
		bar     config.BarConfig
		styling config.StylingConfig
	}{
		{"theme_defaults.css", d.Styling.ActivePalette(), d.Bar, d.Styling},
		{"theme_px.css", d.Styling.ActivePalette(), px, d.Styling},
		{"theme_rounding.css", d.Styling.ActivePalette(), rounding, roundingStyling},
		{"theme_opacity33.css", latte, opacity, d.Styling},
	} {
		assertCSS(t, tc.name, ThemeCSS(tc.palette, d.General, tc.bar, tc.styling), golden(t, tc.name))
	}
}

func TestThemeCSSPixelOverridesOnlyForPixelSizes(t *testing.T) {
	d := config.Defaults()
	bar := d.Bar
	bar.ButtonLabelSize = config.Size{Value: 14, Unit: config.SizePixels}
	bar.ButtonIconPadding = config.Size{Value: 2, Unit: config.SizeMultiplier}
	css := ThemeCSS(d.Styling.ActivePalette(), d.General, bar, d.Styling)
	if !strings.Contains(css, "    --bar-btn-label-size-override: 14px;\n") ||
		!strings.Contains(css, "    --bar-btn-label-scale: 1;\n") {
		t.Errorf("a pixel label size must emit its override and a unit scale:\n%s", css)
	}
	if strings.Contains(css, "--bar-btn-icon-padding-override") || !strings.Contains(css, "--bar-btn-icon-padding-scale: 2;") {
		t.Errorf("a scale size must set its scale and emit no override:\n%s", css)
	}
}

func TestFormatF32MatchesRustDisplay(t *testing.T) {
	// Rust's f32 Display for the same values (probe output).
	for v, want := range map[float32]string{
		1: "1", 1.01: "1.01", 0.9: "0.9", 1.3: "1.3", 0.25: "0.25", 3: "3",
		1.25: "1.25", 3.5: "3.5", 24: "24", 0.1: "0.1", 1e-7: "0.0000001", 123456.7: "123456.7",
	} {
		if got := formatF32(v); got != want {
			t.Errorf("formatF32(%v) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[float64]string{0.85: "0.85", 1: "1", 0.33: "0.33", 0: "0", 0.07: "0.07", 0.01: "0.01", 0.99: "0.99"} {
		if got := formatF64(v); got != want {
			t.Errorf("formatF64(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestPaletteFromHex(t *testing.T) {
	p, err := PaletteFromHex(config.Palette{
		Bg: "#11111b", Surface: "#181825", Elevated: "#1e1e2e", Fg: "#cdd6f4", FgMuted: "#bac2de",
		Primary: "#b4befe", Red: "#f38ba8", Yellow: "#f9e2af", Green: "#a6e3a1", Blue: "#74c7ec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if *p != *Default() {
		t.Errorf("catppuccin-mocha hex = %+v, want the Default palette", p)
	}
	short, err := PaletteFromHex(config.Palette{
		Bg: "#abc", Surface: "#abcd", Elevated: "#000", Fg: "#fff", FgMuted: "#fff",
		Primary: "#fff", Red: "#fff", Yellow: "#fff", Green: "#fff", Blue: "#fff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if short.Bg != render.RGB(0xaa, 0xbb, 0xcc) {
		t.Errorf("#abc = %#08x, want the nibble expansion", short.Bg)
	}
	if short.Surface>>24 != 0xdd {
		t.Errorf("#abcd alpha = %#02x, want 0xdd", short.Surface>>24)
	}
	for _, bad := range []string{"", "abcdef", "#ggg", "rgb(1,2,3)"} {
		p := config.Palette{
			Bg: "#000", Surface: "#000", Elevated: "#000", Fg: "#000", FgMuted: "#000",
			Primary: "#000", Red: "#000", Yellow: "#000", Green: "#000", Blue: bad,
		}
		if _, err := PaletteFromHex(p); err == nil || !strings.Contains(err.Error(), "blue") {
			t.Errorf("blue %q: err = %v, want an error naming the slot", bad, err)
		}
	}
}

func TestConfigPalette(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := config.DefaultsStyling()
	configured, err := PaletteFromHex(s.ActivePalette())
	if err != nil {
		t.Fatal(err)
	}
	p, err := ConfigPalette(s)
	if err != nil || *p != *configured {
		t.Errorf("the default styling = %+v, %v; want its own palette", p, err)
	}
	// A provider with nothing to read keeps the configured palette.
	s.ColorExtractor.ThemeProvider = config.ThemeProviderMatugen
	p, err = ConfigPalette(s)
	if err == nil {
		t.Error("a provider without its colors file reported no failure")
	}
	if *p != *configured {
		t.Errorf("after the provider failed = %+v, want the configured palette", p)
	}
}

// TokensCSS declares every config token as Palette.Token resolves it,
// translucent ones with their alpha, and nothing else.
func TestTokensCSSIsTheTokenTable(t *testing.T) {
	p := Default()
	css := TokensCSS(p)
	for _, tok := range config.CssTokens() {
		c, _ := p.Token(tok)
		if want := "--" + string(tok) + ": " + cssColor(c) + ";"; !strings.Contains(css, want) {
			t.Errorf("TokensCSS lacks %q", want)
		}
	}
	if got, want := strings.Count(css, ";"), len(config.CssTokens()); got != want {
		t.Errorf("TokensCSS declares %d properties, want the %d tokens", got, want)
	}
	if subtle, _ := p.Token(config.TokenFgSubtle); !strings.Contains(css, "--fg-subtle: "+cssColor(subtle)+";") || len(cssColor(subtle)) != 9 {
		t.Errorf("the translucent fg-subtle lost its alpha: %q", cssColor(subtle))
	}
}

// The bundle parses clean and a rule reading a token paints the
// token's resolved color, so the stylesheet and the Go table cannot
// drift.
func TestBundleTokensPaintTheTokenColor(t *testing.T) {
	cfg := config.Defaults()
	p := Default()
	var warns []string
	widget.SetParseWarn(func(msg string) { warns = append(warns, msg) })
	sheet := widget.NewStylesheet(BundleCSS(cfg, cfg.Styling.ActivePalette(), p)+"\n.token-probe { background-color: var(--bg-overlay); }", 200)
	widget.SetParseWarn(nil)
	if len(warns) > 0 {
		t.Fatalf("the bundle trips the CSS engine: %v", warns)
	}
	box := widget.NewBox(widget.Row, 0, 0)
	box.AddClass("token-probe")
	box.SetSizeRequest(10, 10)
	box.AttachStylesheet(sheet)
	widget.SizeWithin(box, widget.Size{W: 10, H: 10})
	box.Arrange(render.Rect{W: 10, H: 10})
	data := make([]byte, render.Stride(10)*10)
	box.Paint(render.NewScaled(data, render.Stride(10), 10, 10, 1, 1))
	i := 5*render.Stride(10) + 5*4
	got := render.RGB(data[i+2], data[i+1], data[i])
	if want, _ := p.Token(config.TokenBgOverlay); got != want {
		t.Errorf("var(--bg-overlay) paints %#08x, want the token's %#08x", uint32(got), uint32(want))
	}
}

// The popup cards' resting surface is the card color, the palette's
// elevated surface, at popup-opacity: opaque at 100, translucent below.
func TestNotificationCSSTintsTheCardColor(t *testing.T) {
	cfg := config.Defaults()
	palette := cfg.Styling.ActivePalette()
	paint := func(opacity config.Percentage) [4]byte {
		cfg.Notification.PopupOpacity = opacity
		sheet := widget.NewStylesheet(BundleCSS(cfg, palette, Default())+"\n.probe { background-color: var(--notification-popup-surface); }", 200)
		box := widget.NewBox(widget.Row, 0, 0)
		box.AddClass("probe")
		box.SetSizeRequest(10, 10)
		box.AttachStylesheet(sheet)
		widget.SizeWithin(box, widget.Size{W: 10, H: 10})
		box.Arrange(render.Rect{W: 10, H: 10})
		data := make([]byte, render.Stride(10)*10)
		box.Paint(render.NewScaled(data, render.Stride(10), 10, 10, 1, 1))
		i := 5*render.Stride(10) + 5*4
		return [4]byte{data[i+2], data[i+1], data[i], data[i+3]}
	}
	elevated, err := PaletteFromHex(palette)
	if err != nil {
		t.Fatal(err)
	}
	want := elevated.Elevated.Straight()
	if got := paint(100); got != [4]byte{want[0], want[1], want[2], 0xff} {
		t.Errorf("popup-opacity 100 paints %v, want the opaque elevated surface %v", got, want)
	}
	if got := paint(40); got[3] < 0x60 || got[3] > 0x6a {
		t.Errorf("popup-opacity 40 paints alpha %#02x, want about 40%%", got[3])
	}
	if !strings.Contains(NotificationCSS(palette, config.NotificationConfig{PopupOpacity: 40}), ", 0.40);") {
		t.Errorf("NotificationCSS at 40 = %q", NotificationCSS(palette, config.NotificationConfig{PopupOpacity: 40}))
	}
}
