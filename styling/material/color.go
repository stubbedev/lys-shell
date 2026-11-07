// Package material computes Material You color schemes from an image,
// a port of Google's material-color-utilities (the TypeScript release
// 0.4.0, 2021 color spec): CAM16 and HCT, the Celebi quantizer (Wu, then
// WSMeans), the seed scorer, tonal palettes and the dynamic scheme roles
// of every variant, at any contrast level.
package material

import (
	"fmt"
	"math"

	"github.com/stubbedev/lys-lib/third_party/gomath"
)

// ARGB is an opaque sRGB color packed as 0xAARRGGBB.
type ARGB uint32

// RGB packs 8-bit channels into an opaque color.
func RGB(r, g, b uint8) ARGB {
	return ARGB(0xff000000 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// Hex is the color as #rrggbb.
func (c ARGB) Hex() string { return fmt.Sprintf("#%06x", uint32(c)&0xffffff) }

func (c ARGB) alpha() uint8 { return uint8(c >> 24 & 0xff) }
func (c ARGB) red() uint8   { return uint8(c >> 16 & 0xff) }
func (c ARGB) green() uint8 { return uint8(c >> 8 & 0xff) }
func (c ARGB) blue() uint8  { return uint8(c & 0xff) }

var srgbToXYZ = [3][3]float64{
	{0.41233895, 0.35762064, 0.18051042},
	{0.2126, 0.7152, 0.0722},
	{0.01932141, 0.11916382, 0.95034478},
}

var xyzToSRGB = [3][3]float64{
	{3.2413774792388685, -1.5376652402851851, -0.49885366846268053},
	{-0.9691452513005321, 1.8758853451067872, 0.04156585616912061},
	{0.05562093689691305, -0.20395524564742123, 1.0571799111220335},
}

var whitePointD65 = [3]float64{95.047, 100.0, 108.883}

func jsRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

func signum(x float64) float64 {
	switch {
	case x < 0:
		return -1
	case x == 0:
		return 0
	}
	return 1
}

func lerp(start, stop, amount float64) float64 {
	return float64((1.0-amount)*start) + float64(amount*stop)
}

func clampUpTo(hi, x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > hi {
		return hi
	}
	return x
}

func sanitizeDegreesInt(degrees int) int {
	degrees %= 360
	if degrees < 0 {
		degrees += 360
	}
	return degrees
}

func sanitizeDegreesDouble(degrees float64) float64 {
	degrees = math.Mod(degrees, 360.0)
	if degrees < 0 {
		degrees += 360.0
	}
	return degrees
}

func differenceDegrees(a, b float64) float64 {
	return 180.0 - math.Abs(math.Abs(a-b)-180.0)
}

func matrixMultiply(row [3]float64, m *[3][3]float64) [3]float64 {
	return [3]float64{
		float64(row[0]*m[0][0]) + float64(row[1]*m[0][1]) + float64(row[2]*m[0][2]),
		float64(row[0]*m[1][0]) + float64(row[1]*m[1][1]) + float64(row[2]*m[1][2]),
		float64(row[0]*m[2][0]) + float64(row[1]*m[2][1]) + float64(row[2]*m[2][2]),
	}
}

func rgbChannel(v float64) uint8 {
	return uint8(clampUpTo(255, jsRound(v)))
}

func argbFromLinrgb(linrgb [3]float64) ARGB {
	return RGB(delinearized(linrgb[0]), delinearized(linrgb[1]), delinearized(linrgb[2]))
}

func argbFromXYZ(x, y, z float64) ARGB {
	return argbFromLinrgb(matrixMultiply([3]float64{x, y, z}, &xyzToSRGB))
}

func linearRGB(c ARGB) [3]float64 {
	return [3]float64{linearized(c.red()), linearized(c.green()), linearized(c.blue())}
}

func xyzFromARGB(c ARGB) [3]float64 {
	return matrixMultiply(linearRGB(c), &srgbToXYZ)
}

func argbFromLab(l, a, b float64) ARGB {
	fy := (l + 16.0) / 116.0
	fx := a/500.0 + fy
	fz := fy - b/200.0
	return argbFromXYZ(
		float64(labInvf(fx)*whitePointD65[0]),
		float64(labInvf(fy)*whitePointD65[1]),
		float64(labInvf(fz)*whitePointD65[2]),
	)
}

func labFromARGB(c ARGB) [3]float64 {
	xyz := xyzFromARGB(c)
	fx := labF(xyz[0] / whitePointD65[0])
	fy := labF(xyz[1] / whitePointD65[1])
	fz := labF(xyz[2] / whitePointD65[2])
	return [3]float64{float64(116.0*fy) - 16, 500.0 * (fx - fy), 200.0 * (fy - fz)}
}

func argbFromLstar(lstar float64) ARGB {
	c := delinearized(yFromLstar(lstar))
	return RGB(c, c, c)
}

func lstarFromARGB(c ARGB) float64 {
	return float64(116.0*labF(xyzFromARGB(c)[1]/100.0)) - 16.0
}

func yFromLstar(lstar float64) float64 {
	return float64(100.0 * labInvf((lstar+16.0)/116.0))
}

func lstarFromY(y float64) float64 {
	return float64(labF(y/100.0)*116.0) - 16.0
}

func linearized(component uint8) float64 {
	normalized := float64(component) / 255.0
	if normalized <= 0.040449936 {
		return normalized / 12.92 * 100.0
	}
	return gomath.Pow((normalized+0.055)/1.055, 2.4) * 100.0
}

func trueDelinearized(component float64) float64 {
	normalized := component / 100.0
	if normalized <= 0.0031308 {
		return normalized * 12.92 * 255.0
	}
	return (float64(1.055*gomath.Pow(normalized, 1.0/2.4)) - 0.055) * 255.0
}

func delinearized(component float64) uint8 {
	normalized := component / 100.0
	var v float64
	if normalized <= 0.0031308 {
		v = normalized * 12.92
	} else {
		v = float64(1.055*gomath.Pow(normalized, 1.0/2.4)) - 0.055
	}
	return rgbChannel(float64(v * 255.0))
}

const (
	labE     = 216.0 / 24389.0
	labKappa = 24389.0 / 27.0
)

func labF(t float64) float64 {
	if t > labE {
		return gomath.Pow(t, 1.0/3.0)
	}
	return (float64(labKappa*t) + 16) / 116
}

func labInvf(ft float64) float64 {
	ft3 := ft * ft * ft
	if ft3 > labE {
		return ft3
	}
	return (float64(116*ft) - 16) / labKappa
}
