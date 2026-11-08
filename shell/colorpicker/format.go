package colorpicker

import (
	"fmt"
	"math"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/third_party/gomath"
)

// Format writes the color in f.
func (c RGB) Format(f config.ColorFormat) string {
	switch f {
	case config.ColorFormatHex:
		return c.Hex()
	case config.ColorFormatRGB:
		return fmt.Sprintf("rgb(%d, %d, %d)", c.R, c.G, c.B)
	case config.ColorFormatHSL:
		h, s, l := c.HSL()
		return fmt.Sprintf("hsl(%d, %d%%, %d%%)", roundHue(h), percent(s), percent(l))
	case config.ColorFormatHSV:
		h, s, v := c.HSV()
		return fmt.Sprintf("hsv(%d, %d%%, %d%%)", roundHue(h), percent(s), percent(v))
	case config.ColorFormatOKLCH:
		l, ch, h := c.OKLCH()
		if ch < 0.00005 {
			ch, h = 0, 0
		}
		return fmt.Sprintf("oklch(%.2f%% %.4f %.2f)", l*100, ch, h)
	case config.ColorFormatCMYK:
		cy, m, y, k := c.CMYK()
		return fmt.Sprintf("cmyk(%d%%, %d%%, %d%%, %d%%)", percent(cy), percent(m), percent(y), percent(k))
	}
	panic(fmt.Sprintf("colorpicker: unknown color format %q", string(f)))
}

// HSL is the color's hue in degrees [0, 360) and its HSL saturation and
// lightness in [0, 1].
func (c RGB) HSL() (h, s, l float64) {
	r, g, b := c.Float()
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return 0, 0, l
	}
	return hue(r, g, b, hi, d), d / (1 - math.Abs(hi+lo-1)), l
}

// HSV is the color's hue in degrees [0, 360) and its HSV saturation and
// value in [0, 1].
func (c RGB) HSV() (h, s, v float64) {
	r, g, b := c.Float()
	hi, lo := max(r, g, b), min(r, g, b)
	d := hi - lo
	if d == 0 {
		return 0, 0, hi
	}
	return hue(r, g, b, hi, d), d / hi, hi
}

// hue is the hexcone hue in degrees of channels whose largest is hi and
// whose spread is d, which is positive.
func hue(r, g, b, hi, d float64) float64 {
	var h float64
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h = float64(h * 60)
	if h < 0 {
		h += 360
	}
	return h
}

// CMYK is the color's naive cyan, magenta, yellow and key in [0, 1].
func (c RGB) CMYK() (cy, m, y, k float64) {
	r, g, b := c.Float()
	k = 1 - max(r, g, b)
	if k == 1 {
		return 0, 0, 0, 1
	}
	return (1 - r - k) / (1 - k), (1 - g - k) / (1 - k), (1 - b - k) / (1 - k), k
}

// OKLCH is the color in Björn Ottosson's Oklab, as lightness in [0, 1],
// chroma, and hue in degrees [0, 360).
func (c RGB) OKLCH() (l, ch, h float64) {
	r, g, b := linear(c.R), linear(c.G), linear(c.B)
	lm := gomath.Cbrt(float64(0.4122214708*r) + float64(0.5363325363*g) + float64(0.0514459929*b))
	mm := gomath.Cbrt(float64(0.2119034982*r) + float64(0.6806995451*g) + float64(0.1073969566*b))
	sm := gomath.Cbrt(float64(0.0883024619*r) + float64(0.2817188376*g) + float64(0.6299787005*b))
	l = float64(0.2104542553*lm) + float64(0.7936177850*mm) - float64(0.0040720468*sm)
	a := float64(1.9779984951*lm) - float64(2.4285922050*mm) + float64(0.4505937099*sm)
	bb := float64(0.0259040371*lm) + float64(0.7827717662*mm) - float64(0.8086757660*sm)
	ch = gomath.Hypot(a, bb)
	h = gomath.Atan2(bb, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return l, ch, h
}

// linear is an sRGB channel's linear light.
func linear(v uint8) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return gomath.Pow((f+0.055)/1.055, 2.4)
}

func percent(v float64) int { return int(math.Round(v * 100)) }

func roundHue(h float64) int { return int(math.Round(h)) % 360 }
