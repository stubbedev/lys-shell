package bar

import (
	"image"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-desktop/styling/material"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

const (
	contrastPixelsWidth = 400
	aaaTextContrast     = 7.0
)

// The bar as painted, pixel by pixel: with reduce-transparency, high
// contrast (dark and light) or both, every pixel of its surface is
// opaque and its text stands at least 7:1 (WCAG AAA) against what it is
// drawn on; the default translucent bar shows the desktop through, and
// a dim label falls short.
func TestAccessibleBarPixelsAreOpaqueAndHighContrast(t *testing.T) {
	t.Cleanup(func() { widget.SetMediaContrast(false) })
	for _, mode := range []struct {
		name  string
		apply func(*config.StylingConfig)
	}{
		{"reduce transparency", func(s *config.StylingConfig) { s.ReduceTransparency = true }},
		{"high contrast dark", func(s *config.StylingConfig) { s.HighContrast, s.Appearance = true, config.AppearanceDark }},
		{"high contrast light", func(s *config.StylingConfig) { s.HighContrast, s.Appearance = true, config.AppearanceLight }},
		{"both", func(s *config.StylingConfig) { s.HighContrast, s.ReduceTransparency = true, true }},
	} {
		t.Run(mode.name, func(t *testing.T) {
			cfg := config.Defaults()
			mode.apply(&cfg.Styling)
			shot := paintClockBar(t, cfg)
			if x, y, a, ok := shot.translucentSurfacePixel(); ok {
				t.Errorf("the surface pixel at (%d, %d) has alpha %d, want 255", x, y, a)
			}
			if r := shot.labelContrast(t); r < aaaTextContrast {
				t.Errorf("the clock's text stands %.2f:1 against its background, want at least %.0f:1", r, aaaTextContrast)
			}
		})
	}
	t.Run("default", func(t *testing.T) {
		shot := paintClockBar(t, config.Defaults())
		if _, _, _, ok := shot.translucentSurfacePixel(); !ok {
			t.Error("the default bar's surface is opaque, want its 60% background showing the desktop")
		}
	})
	dim, err := config.ParseColorValue("#45465c")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a dim label", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Styling.ReduceTransparency = true
		cfg.Clock.LabelColor = dim
		if r := paintClockBar(t, cfg).labelContrast(t); r >= aaaTextContrast {
			t.Errorf("a dim label reads %.2f:1, want the check to find it below %.0f:1", r, aaaTextContrast)
		}
	})
	for _, appearance := range []config.Appearance{config.AppearanceDark, config.AppearanceLight} {
		t.Run("a dim label under high contrast "+string(appearance), func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Styling.HighContrast, cfg.Styling.Appearance = true, appearance
			cfg.Clock.LabelColor = dim
			if r := paintClockBar(t, cfg).labelContrast(t); r < aaaTextContrast {
				t.Errorf("a dim custom label under high contrast reads %.2f:1, want at least %.0f:1", r, aaaTextContrast)
			}
		})
	}
	t.Run("turning high contrast on restyles a dim label in place", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Clock.LabelColor = dim
		_, _, root := paintBar(t, styledContext(t, cfg), clockLayout, contrastPixelsWidth)
		btn := treetest.WithClass(root, "bar-button")[0].(*barButton)
		if !strings.Contains(btn.InlineStyle(), "--bar-btn-label-color: #45465c;") {
			t.Fatalf("the dim label is not applied before the toggle: %q", btn.InlineStyle())
		}
		high := *cfg
		high.Styling.HighContrast = true
		restyleButtons(root, &high)
		if strings.Contains(btn.InlineStyle(), "#45465c") {
			t.Errorf("the dim label survives turning high contrast on: %q", btn.InlineStyle())
		}
	})
}

// barShot is one painted bar of a clock.
type barShot struct {
	data  []byte
	w, h  int
	label render.Rect
}

func paintClockBar(t *testing.T, cfg *config.Config) barShot {
	t.Helper()
	data, h, root := paintBar(t, styledContext(t, cfg), clockLayout, contrastPixelsWidth)
	buttons := treetest.WithClass(root, "bar-button")
	if len(buttons) != 1 {
		t.Fatalf("the bar shows %d module buttons, want the clock's", len(buttons))
	}
	label := buttons[0].(*barButton).Label()
	if label.Text() == "" {
		t.Fatal("the clock shows no text")
	}
	return barShot{data: data, w: contrastPixelsWidth, h: h, label: label.Bounds()}
}

func (s barShot) at(x, y int) render.Color { return pixelAt(s.data, s.w, x, y) }

// surface is the box the bar paints, the bounds of every pixel with
// any alpha.
func (s barShot) surface() image.Rectangle {
	var r image.Rectangle
	for y := range s.h {
		for x := range s.w {
			if s.at(x, y).A() != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

// translucentSurfacePixel finds a pixel of the surface that is not
// opaque, leaving out the corner squares a rounding up to half the
// bar's thickness antialiases.
func (s barShot) translucentSurfacePixel() (x, y int, alpha uint8, ok bool) {
	r := s.surface()
	corner := r.Dy() / 2
	for y := r.Min.Y; y < r.Max.Y; y++ {
		nearEnd := y < r.Min.Y+corner || y >= r.Max.Y-corner
		for x := r.Min.X; x < r.Max.X; x++ {
			if nearEnd && (x < r.Min.X+corner || x >= r.Max.X-corner) {
				continue
			}
			if a := s.at(x, y).A(); a != 0xff {
				return x, y, a, true
			}
		}
	}
	return 0, 0, 0, false
}

// labelContrast is the contrast between the clock's text and what it is
// drawn on: the label box's most common color is the background, its
// glyph cores the pixels farthest from it.
func (s barShot) labelContrast(t *testing.T) float64 {
	t.Helper()
	counts := map[render.Color]int{}
	for y := s.label.Y; y < s.label.Y+s.label.H; y++ {
		for x := s.label.X; x < s.label.X+s.label.W; x++ {
			counts[s.at(x, y)]++
		}
	}
	var bg render.Color
	for c, n := range counts {
		if n > counts[bg] || n == counts[bg] && c < bg {
			bg = c
		}
	}
	if bg.A() != 0xff {
		t.Fatalf("the clock's text sits on %v, which is not opaque", bg.Straight())
	}
	best := 1.0
	for c := range counts {
		if r := material.ContrastRatio(opaqueARGB(c), opaqueARGB(bg)); r > best {
			best = r
		}
	}
	return best
}

func opaqueARGB(c render.Color) material.ARGB {
	s := c.Straight()
	return material.RGB(s[0], s[1], s[2])
}
