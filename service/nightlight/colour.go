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

// Setting is a filter strength: the colour temperature in Kelvin and the
// gamma, a brightness percentage. NewSetting is the only way to build
// one, so every Setting is in range.
type Setting struct {
	temp  int
	gamma int
}

// NewSetting validates temp against MinTemp..MaxTemp and gamma against
// 0..MaxGamma.
func NewSetting(temp, gamma int) (Setting, error) {
	if temp < MinTemp || temp > MaxTemp {
		return Setting{}, fmt.Errorf("nightlight: temperature %dK is outside %d-%dK", temp, MinTemp, MaxTemp)
	}
	if gamma < 0 || gamma > MaxGamma {
		return Setting{}, fmt.Errorf("nightlight: gamma %d%% is outside 0-%d%%", gamma, MaxGamma)
	}
	return Setting{temp: temp, gamma: gamma}, nil
}

// Temp is the colour temperature in Kelvin.
func (s Setting) Temp() int { return s.temp }

// Gamma is the brightness percentage.
func (s Setting) Gamma() int { return s.gamma }

// Matrix is a row-major 3×3 colour transform.
type Matrix [9]float64

// whitepoint is the red, green and blue scale of a blackbody at temp:
// Tanner Helland's fit, computed on whole hundreds of Kelvin, the
// approximation hyprsunset's matrixForKelvin uses.
func whitepoint(temp int) [3]float64 {
	t := float64(temp / 100)
	var r, g, b float64
	if t <= 66 {
		r = 255
		g = clamp255(99.4708025861*math.Log(t) - 161.1195681661)
		if t > 19 {
			b = clamp255(138.5177312231*math.Log(t-10) - 305.0447927307)
		}
	} else {
		r = clamp255(329.698727446 * math.Pow(t-60, -0.1332047592))
		g = clamp255(288.1221695283 * math.Pow(t-60, -0.0755148492))
		b = 255
	}
	return [3]float64{r / 255, g / 255, b / 255}
}

func clamp255(v float64) float64 { return math.Min(math.Max(v, 0), 255) }

// scale is each channel's multiplier: the whitepoint dimmed by gamma.
func (s Setting) scale() [3]float64 {
	wp := whitepoint(s.temp)
	k := float64(s.gamma) / 100
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
