package colorpicker

import (
	"math"
	"testing"

	"github.com/stubbedev/lys-shell/config"
)

func TestEachFormatWritesReferenceColors(t *testing.T) {
	for _, tc := range []struct {
		c    RGB
		want map[config.ColorFormat]string
	}{
		{RGB{0, 0, 0}, map[config.ColorFormat]string{
			config.ColorFormatHex: "#000000", config.ColorFormatRGB: "rgb(0, 0, 0)", config.ColorFormatHSL: "hsl(0, 0%, 0%)",
			config.ColorFormatHSV: "hsv(0, 0%, 0%)", config.ColorFormatOKLCH: "oklch(0.00% 0.0000 0.00)", config.ColorFormatCMYK: "cmyk(0%, 0%, 0%, 100%)",
		}},
		{RGB{255, 255, 255}, map[config.ColorFormat]string{
			config.ColorFormatHex: "#FFFFFF", config.ColorFormatRGB: "rgb(255, 255, 255)", config.ColorFormatHSL: "hsl(0, 0%, 100%)",
			config.ColorFormatHSV: "hsv(0, 0%, 100%)", config.ColorFormatOKLCH: "oklch(100.00% 0.0000 0.00)", config.ColorFormatCMYK: "cmyk(0%, 0%, 0%, 0%)",
		}},
		{RGB{255, 0, 0}, map[config.ColorFormat]string{
			config.ColorFormatHex: "#FF0000", config.ColorFormatRGB: "rgb(255, 0, 0)", config.ColorFormatHSL: "hsl(0, 100%, 50%)",
			config.ColorFormatHSV: "hsv(0, 100%, 100%)", config.ColorFormatOKLCH: "oklch(62.80% 0.2577 29.23)", config.ColorFormatCMYK: "cmyk(0%, 100%, 100%, 0%)",
		}},
		{RGB{0, 0, 255}, map[config.ColorFormat]string{
			config.ColorFormatHSL: "hsl(240, 100%, 50%)", config.ColorFormatHSV: "hsv(240, 100%, 100%)", config.ColorFormatCMYK: "cmyk(100%, 100%, 0%, 0%)",
		}},
		{RGB{255, 128, 0}, map[config.ColorFormat]string{
			config.ColorFormatHex: "#FF8000", config.ColorFormatHSL: "hsl(30, 100%, 50%)", config.ColorFormatHSV: "hsv(30, 100%, 100%)",
			config.ColorFormatCMYK: "cmyk(0%, 50%, 100%, 0%)",
		}},
		{RGB{0x66, 0x33, 0x99}, map[config.ColorFormat]string{
			config.ColorFormatHSL: "hsl(270, 50%, 40%)", config.ColorFormatHSV: "hsv(270, 67%, 60%)", config.ColorFormatCMYK: "cmyk(33%, 67%, 0%, 40%)",
		}},
	} {
		for f, want := range tc.want {
			if got := tc.c.Format(f); got != want {
				t.Errorf("%s as %s = %q, want %q", tc.c.Hex(), f, got, want)
			}
		}
	}
}

func TestOKLCHMatchesPublishedValues(t *testing.T) {
	for _, tc := range []struct {
		c        RGB
		l, ch, h float64
	}{
		{RGB{255, 0, 0}, 0.62796, 0.25768, 29.2339},
		{RGB{0, 255, 0}, 0.86644, 0.29483, 142.4953},
		{RGB{0, 0, 255}, 0.45201, 0.31321, 264.052},
		{RGB{255, 255, 255}, 1, 0, -1},
	} {
		l, ch, h := tc.c.OKLCH()
		if math.Abs(l-tc.l) > 0.0005 || math.Abs(ch-tc.ch) > 0.0005 || (tc.h >= 0 && math.Abs(h-tc.h) > 0.05) {
			t.Errorf("%s: oklch(%.5f %.5f %.4f), want (%.5f %.5f %.4f)", tc.c.Hex(), l, ch, h, tc.l, tc.ch, tc.h)
		}
	}
}

func TestEveryConversionRoundTrips(t *testing.T) {
	for r := 0; r < 256; r += 15 {
		for g := 0; g < 256; g += 17 {
			for b := 0; b < 256; b += 51 {
				c := RGB{uint8(r), uint8(g), uint8(b)}
				h, s, l := c.HSL()
				near(t, c, "hsl", fromHSL(h, s, l))
				h, s, v := c.HSV()
				near(t, c, "hsv", fromHSV(h, s, v))
				cy, m, y, k := c.CMYK()
				near(t, c, "cmyk", [3]float64{(1 - cy) * (1 - k), (1 - m) * (1 - k), (1 - y) * (1 - k)})
				lo, ch, hu := c.OKLCH()
				near(t, c, "oklch", fromOKLCH(lo, ch, hu))
			}
		}
	}
}

func near(t *testing.T, c RGB, via string, back [3]float64) {
	t.Helper()
	want := [3]uint8{c.R, c.G, c.B}
	for i := range back {
		if math.Abs(back[i]*255-float64(want[i])) > 0.5 {
			t.Errorf("%s through %s came back as %v", c.Hex(), via, back)
			return
		}
	}
}

func fromHSL(h, s, l float64) [3]float64 {
	ch := (1 - math.Abs(2*l-1)) * s
	return fromChroma(h, ch, l-ch/2)
}

func fromHSV(h, s, v float64) [3]float64 {
	ch := v * s
	return fromChroma(h, ch, v-ch)
}

func fromChroma(h, ch, m float64) [3]float64 {
	hp := h / 60
	x := ch * (1 - math.Abs(math.Mod(hp, 2)-1))
	var rgb [3]float64
	switch int(hp) {
	case 0:
		rgb = [3]float64{ch, x, 0}
	case 1:
		rgb = [3]float64{x, ch, 0}
	case 2:
		rgb = [3]float64{0, ch, x}
	case 3:
		rgb = [3]float64{0, x, ch}
	case 4:
		rgb = [3]float64{x, 0, ch}
	default:
		rgb = [3]float64{ch, 0, x}
	}
	return [3]float64{rgb[0] + m, rgb[1] + m, rgb[2] + m}
}

func fromOKLCH(l, ch, h float64) [3]float64 {
	a, b := ch*math.Cos(h*math.Pi/180), ch*math.Sin(h*math.Pi/180)
	lm, mm, sm := l+0.3963377774*a+0.2158037573*b, l-0.1055613458*a-0.0638541728*b, l-0.0894841775*a-1.2914855480*b
	lm, mm, sm = lm*lm*lm, mm*mm*mm, sm*sm*sm
	lin := [3]float64{
		4.0767416621*lm - 3.3077115913*mm + 0.2309699292*sm,
		-1.2684380046*lm + 2.6097574011*mm - 0.3413193965*sm,
		-0.0041960863*lm - 0.7034186147*mm + 1.7076147010*sm,
	}
	for i, v := range lin {
		if v <= 0.0031308 {
			lin[i] = 12.92 * v
		} else {
			lin[i] = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
	}
	return lin
}
