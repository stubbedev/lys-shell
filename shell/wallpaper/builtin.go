package wallpaper

import (
	"image"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/third_party/gomath"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/offheap"
)

// builtinWallpaper is the path of the wallpaper a monitor shows when no
// image is configured. It is drawn for the monitor, not read from a file.
const builtinWallpaper = "builtin:lys-shell"

// builtinFallback is the size drawn while the output's is unreported.
var builtinFallback = image.Pt(1920, 1080)

// encodeLevels is the size of the table from linear light to sRGB.
const encodeLevels = 4096

// glow is a soft patch of color; x, y and the radii are fractions of the
// image, and strength is how far it blends over what is below at its center.
type glow struct {
	x, y, rx, ry float64
	color        [3]float64
	strength     float64
}

var (
	builtinBase  = srgbLinear([3]float64{0x11, 0x12, 0x20})
	builtinGlows = []glow{
		{0.82, 1.05, 0.75, 0.80, srgbLinear([3]float64{0xe0, 0x94, 0x7a}), 0.42},
		{0.15, 0.95, 0.80, 0.70, srgbLinear([3]float64{0x5a, 0x4a, 0xa8}), 0.60},
		{0.55, -0.10, 0.70, 0.55, srgbLinear([3]float64{0x2c, 0x4c, 0x88}), 0.55},
	}
)

func srgbLinear(c [3]float64) [3]float64 {
	for i, v := range c {
		c[i] = gomath.Pow(v/255, 2.2)
	}
	return c
}

// falloff is one axis of a glow, whose Gaussian falls off independently along
// each axis, so a table per axis makes a pixel a product instead of an exp.
func falloff(out []float64, center, radius float64) {
	for i := range out {
		d := (float64(i)/float64(len(out)) - center) / radius
		out[i] = gomath.Exp(-2.4 * d * d)
	}
}

// ditherAt is triangular noise in about a level, deterministic in x and y. It
// keeps eight bits from banding on slopes this shallow.
func ditherAt(x, y int) float64 {
	h := intconv.Clamp[uint32](x)*0x9e3779b1 ^ intconv.Clamp[uint32](y)*0x85ebca77
	h ^= h >> 15
	h *= 0xc2b2ae3d
	h ^= h >> 13
	return (float64(h&0xffff) - float64(h>>16)) / 65535
}

// paintBuiltin draws the wallpaper into img, opaque. Blending happens in
// linear light, so the glows do not muddy where they meet.
func paintBuiltin(img *image.RGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	tables := make([]float64, len(builtinGlows)*(w+h)+encodeLevels)
	for i, g := range builtinGlows {
		at := i * (w + h)
		falloff(tables[at:at+w], g.x, g.rx)
		falloff(tables[at+w:at+w+h], g.y, g.ry)
	}
	encode := tables[len(builtinGlows)*(w+h):]
	for i := range encode {
		encode[i] = 255 * gomath.Pow(float64(i)/(encodeLevels-1), 1/2.2)
	}
	for y := range h {
		line := img.Pix[y*img.Stride : y*img.Stride+4*w]
		for x := range w {
			c := builtinBase
			for i, g := range builtinGlows {
				at := i * (w + h)
				t := tables[at+x] * tables[at+w+y] * g.strength
				for ch := range c {
					c[ch] += float64((g.color[ch] - c[ch]) * t)
				}
			}
			noise := ditherAt(x, y)
			px := line[4*x : 4*x+4 : 4*x+4]
			for ch := range c {
				px[ch] = uint8(encode[int(c[ch]*(encodeLevels-1))] + noise + 0.5)
			}
			px[3] = 0xff
		}
	}
}

// builtinRaster draws the wallpaper at the monitor's size, so no resample
// averages its dither away.
func builtinRaster(mem *memory.Shared, box target, known bool) (*raster, error) {
	size := builtinFallback
	scale := widget.ImageCover
	if known {
		size, scale = image.Pt(box.w, box.h), widget.ImageScaleDown
	}
	dst, pix, err := offheap.NewRGBA(mem, size.X, size.Y)
	if err != nil {
		return nil, err
	}
	paintBuiltin(dst)
	return &raster{image: render.NewStillImage(dst), scale: scale, pix: pix}, nil
}
