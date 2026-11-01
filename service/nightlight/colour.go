package nightlight

import (
	"fmt"
	"math"
)

// The documented ranges of the module's temperature and gamma.
const (
	MinTemp  = 1000
	MaxTemp  = 20000
	MaxGamma = 200
)

// miredsPerKelvin is the mired value of 1 K: a mired is a million over
// the Kelvin temperature.
const miredsPerKelvin = 1_000_000.0

// neutralTemp is where the whitepoint fit is exactly the identity.
const neutralTemp = 6600

// Setting is a filter strength: the colour temperature in Kelvin and the
// gamma, a brightness percentage. NewSetting and the easing between two
// valid strengths are the only ways to build one, so every Setting is
// in range. Eased strengths are fractional, so no step of an ease shows.
type Setting struct {
	temp  float64
	gamma float64
}

// identity is the strength that leaves the display's colours alone.
var identity = Setting{temp: neutralTemp, gamma: 100}

// NewSetting validates temp against MinTemp..MaxTemp and gamma against
// 0..MaxGamma.
func NewSetting(temp, gamma int) (Setting, error) {
	if temp < MinTemp || temp > MaxTemp {
		return Setting{}, fmt.Errorf("nightlight: temperature %dK is outside %d-%dK", temp, MinTemp, MaxTemp)
	}
	if gamma < 0 || gamma > MaxGamma {
		return Setting{}, fmt.Errorf("nightlight: gamma %d%% is outside 0-%d%%", gamma, MaxGamma)
	}
	return Setting{temp: float64(temp), gamma: float64(gamma)}, nil
}

// Temp is the colour temperature in Kelvin, to the nearest whole one.
func (s Setting) Temp() int { return int(math.Round(s.temp)) }

// Gamma is the brightness percentage, to the nearest whole one.
func (s Setting) Gamma() int { return int(math.Round(s.gamma)) }

// IsIdentity reports whether s leaves the display's colours alone, so
// the filter can let go of the protocol instead of applying it.
func (s Setting) IsIdentity() bool { return s == identity }

// eased is the strength the fraction of the way from the identity to s:
// the temperature in mired, where equal distances are equal perceptual
// steps, and the gamma linearly.
func (s Setting) eased(fraction float64) Setting {
	switch {
	case fraction <= 0:
		return identity
	case fraction >= 1:
		return s
	}
	from, to := miredsPerKelvin/identity.temp, miredsPerKelvin/s.temp
	return Setting{
		temp:  miredsPerKelvin / (from + (to-from)*fraction),
		gamma: identity.gamma + (s.gamma-identity.gamma)*fraction,
	}
}

// Matrix is a row-major 3×3 colour transform.
type Matrix [9]float64

// whitepoint is the red, green and blue scale of a blackbody at temp:
// Tanner Helland's fit, the approximation hyprsunset's matrixForKelvin
// uses, on continuous Kelvin so an ease has no steps of its own.
func whitepoint(temp float64) [3]float64 {
	t := temp / 100
	var r, g, b float64
	if t <= 66 {
		r = 255
		g = clamp255(99.4708025861*math.Log(t) - 161.1195681661)
	} else {
		r = clamp255(329.698727446 * math.Pow(t-60, -0.1332047592))
		g = clamp255(288.1221695283 * math.Pow(t-60, -0.0755148492))
	}
	switch {
	case t >= 66:
		b = 255
	case t > 19:
		b = clamp255(138.5177312231*math.Log(t-10) - 305.0447927307)
	}
	return [3]float64{r / 255, g / 255, b / 255}
}

func clamp255(v float64) float64 { return math.Min(math.Max(v, 0), 255) }

// scale is each channel's multiplier: the whitepoint dimmed by gamma.
func (s Setting) scale() [3]float64 {
	wp := whitepoint(s.temp)
	k := s.gamma / 100
	return [3]float64{wp[0] * k, wp[1] * k, wp[2] * k}
}

// Matrix is the colour transform that applies s: a diagonal of the
// channel scales.
func (s Setting) Matrix() Matrix {
	c := s.scale()
	return Matrix{c[0], 0, 0, 0, c[1], 0, 0, 0, c[2]}
}

// Ramps is the gamma table that applies s on an output with size
// entries per channel: the red, then green, then blue ramp, each the
// identity scaled by its channel, so the result matches Matrix.
func (s Setting) Ramps(size int) []uint16 {
	c := s.scale()
	out := make([]uint16, 3*size)
	last := float64(max(size-1, 1))
	for ch, k := range c {
		ramp := out[ch*size : (ch+1)*size]
		for i := range ramp {
			v := math.Round(float64(i) / last * k * math.MaxUint16)
			ramp[i] = uint16(math.Min(v, math.MaxUint16))
		}
	}
	return out
}
