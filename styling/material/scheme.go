package material

import (
	"fmt"
	"math"
)

// Variant is a dynamic scheme style.
type Variant uint8

// The scheme variants of the 2021 spec.
const (
	Monochrome Variant = iota
	Neutral
	TonalSpot
	Vibrant
	Expressive
	Fidelity
	Content
	Rainbow
	FruitSalad
)

func (v Variant) String() string {
	switch v {
	case Monochrome:
		return "monochrome"
	case Neutral:
		return "neutral"
	case TonalSpot:
		return "tonal-spot"
	case Vibrant:
		return "vibrant"
	case Expressive:
		return "expressive"
	case Fidelity:
		return "fidelity"
	case Content:
		return "content"
	case Rainbow:
		return "rainbow"
	case FruitSalad:
		return "fruit-salad"
	}
	return fmt.Sprintf("Variant(%d)", uint8(v))
}

type paletteKind uint8

const (
	primaryPalette paletteKind = iota
	secondaryPalette
	tertiaryPalette
	neutralPalette
	neutralVariantPalette
	errorPalette
	paletteCount
)

// Scheme is every color role of one dynamic scheme.
type Scheme struct {
	colors [RoleCount]ARGB
}

// Color is the scheme's color for a role.
func (s *Scheme) Color(r Role) ARGB { return s.colors[r] }

type scheme struct {
	source   HCT
	variant  Variant
	dark     bool
	contrast float64
	palettes [paletteCount]tonalPalette
	tones    [RoleCount]float64
	known    [RoleCount]bool
}

// NewScheme builds the scheme of a seed color in one variant, light or
// dark, at a contrast level from -1 (reduced) through 0 (standard) to 1
// (high).
func NewScheme(seed ARGB, variant Variant, dark bool, contrast float64) Scheme {
	source := HCTOf(seed)
	s := &scheme{source: source, variant: variant, dark: dark, contrast: contrast}
	s.palettes = variantPalettes(variant, source)
	var out Scheme
	for r := range RoleCount {
		out.colors[r] = s.palettes[roleSpecs[r].palette].tone(s.tone(r))
	}
	return out
}

func rotatedHue(source HCT, breakpoints, rotations []float64) float64 {
	rotation := 0.0
	for i := range min(len(breakpoints)-1, len(rotations)) {
		if source.Hue >= breakpoints[i] && source.Hue < breakpoints[i+1] {
			rotation = sanitizeDegreesDouble(rotations[i])
			break
		}
	}
	return sanitizeDegreesDouble(source.Hue + rotation)
}

var (
	expressiveBreakpoints  = []float64{0, 21, 51, 121, 151, 191, 271, 321, 360}
	expressiveSecondary    = []float64{45, 95, 45, 20, 45, 90, 45, 45, 45}
	expressiveTertiary     = []float64{120, 120, 20, 45, 20, 15, 20, 120, 120}
	vibrantBreakpoints     = []float64{0, 41, 61, 101, 131, 181, 251, 301, 360}
	vibrantSecondary       = []float64{18, 15, 10, 12, 15, 18, 15, 12, 12}
	vibrantTertiary        = []float64{35, 30, 20, 25, 30, 35, 30, 25, 25}
	defaultErrorPaletteHue = 25.0
)

func variantPalettes(v Variant, src HCT) [paletteCount]tonalPalette {
	hc := paletteFromHueAndChroma
	var p [paletteCount]tonalPalette
	p[errorPalette] = hc(defaultErrorPaletteHue, 84)
	switch v {
	case Content, Fidelity:
		p[primaryPalette] = hc(src.Hue, src.Chroma)
		p[secondaryPalette] = hc(src.Hue, max(src.Chroma-32.0, src.Chroma*0.5))
		if v == Content {
			p[tertiaryPalette] = paletteFromHCT(fixIfDisliked(newTemperatureCache(src).analogous(3, 6)[2]))
		} else {
			p[tertiaryPalette] = paletteFromHCT(fixIfDisliked(newTemperatureCache(src).complement()))
		}
		p[neutralPalette] = hc(src.Hue, src.Chroma/8.0)
		p[neutralVariantPalette] = hc(src.Hue, float64(src.Chroma/8.0)+4.0)
	case FruitSalad:
		p[primaryPalette] = hc(sanitizeDegreesDouble(src.Hue-50.0), 48)
		p[secondaryPalette] = hc(sanitizeDegreesDouble(src.Hue-50.0), 36)
		p[tertiaryPalette] = hc(src.Hue, 36)
		p[neutralPalette] = hc(src.Hue, 10)
		p[neutralVariantPalette] = hc(src.Hue, 16)
	case Monochrome:
		for _, k := range []paletteKind{primaryPalette, secondaryPalette, tertiaryPalette, neutralPalette, neutralVariantPalette} {
			p[k] = hc(src.Hue, 0)
		}
	case Neutral:
		p[primaryPalette] = hc(src.Hue, 12)
		p[secondaryPalette] = hc(src.Hue, 8)
		p[tertiaryPalette] = hc(src.Hue, 16)
		p[neutralPalette] = hc(src.Hue, 2)
		p[neutralVariantPalette] = hc(src.Hue, 2)
	case Rainbow:
		p[primaryPalette] = hc(src.Hue, 48)
		p[secondaryPalette] = hc(src.Hue, 16)
		p[tertiaryPalette] = hc(sanitizeDegreesDouble(src.Hue+60.0), 24)
		p[neutralPalette] = hc(src.Hue, 0)
		p[neutralVariantPalette] = hc(src.Hue, 0)
	case TonalSpot:
		p[primaryPalette] = hc(src.Hue, 36)
		p[secondaryPalette] = hc(src.Hue, 16)
		p[tertiaryPalette] = hc(sanitizeDegreesDouble(src.Hue+60.0), 24)
		p[neutralPalette] = hc(src.Hue, 6)
		p[neutralVariantPalette] = hc(src.Hue, 8)
	case Expressive:
		p[primaryPalette] = hc(sanitizeDegreesDouble(src.Hue+240), 40)
		p[secondaryPalette] = hc(rotatedHue(src, expressiveBreakpoints, expressiveSecondary), 24)
		p[tertiaryPalette] = hc(rotatedHue(src, expressiveBreakpoints, expressiveTertiary), 32)
		p[neutralPalette] = hc(sanitizeDegreesDouble(src.Hue+15), 8)
		p[neutralVariantPalette] = hc(sanitizeDegreesDouble(src.Hue+15), 12)
	case Vibrant:
		p[primaryPalette] = hc(src.Hue, 200)
		p[secondaryPalette] = hc(rotatedHue(src, vibrantBreakpoints, vibrantSecondary), 24)
		p[tertiaryPalette] = hc(rotatedHue(src, vibrantBreakpoints, vibrantTertiary), 32)
		p[neutralPalette] = hc(src.Hue, 10)
		p[neutralVariantPalette] = hc(src.Hue, 12)
	default:
		panic(fmt.Sprintf("material: unsupported variant %v", v))
	}
	return p
}

func (s *scheme) isMonochrome() bool { return s.variant == Monochrome }

func (s *scheme) isFidelity() bool { return s.variant == Fidelity || s.variant == Content }

func (s *scheme) pick(dark, light float64) float64 {
	if s.dark {
		return dark
	}
	return light
}

func (s *scheme) tone(r Role) float64 {
	if s.known[r] {
		return s.tones[r]
	}
	t := s.computeTone(r)
	s.tones[r], s.known[r] = t, true
	return t
}

func (s *scheme) computeTone(r Role) float64 {
	spec := &roleSpecs[r]
	if spec.pair != nil {
		return s.pairTone(r, spec)
	}
	answer := spec.tone(s)
	if spec.background == nil {
		return answer
	}
	bgTone := s.tone(spec.background(s))
	desired := spec.curve.get(s.contrast)
	if ratioOfTones(bgTone, answer) < desired || s.contrast < 0 {
		answer = foregroundTone(bgTone, desired)
	}
	if spec.isBackground && 50 <= answer && answer < 60 {
		if ratioOfTones(49, bgTone) >= desired {
			answer = 49
		} else {
			answer = 60
		}
	}
	if spec.secondBackground == nil {
		return answer
	}
	bgTone1, bgTone2 := bgTone, s.tone(spec.secondBackground(s))
	upper, lower := max(bgTone1, bgTone2), min(bgTone1, bgTone2)
	if ratioOfTones(upper, answer) >= desired && ratioOfTones(lower, answer) >= desired {
		return answer
	}
	lightOption := lighterTone(upper, desired)
	darkOption := darkerTone(lower, desired)
	if tonePrefersLightForeground(bgTone1) || tonePrefersLightForeground(bgTone2) {
		if lightOption < 0 {
			return 100
		}
		return lightOption
	}
	if (lightOption != -1) != (darkOption != -1) {
		if lightOption != -1 {
			return lightOption
		}
		return darkOption
	}
	if darkOption < 0 {
		return 0
	}
	return darkOption
}

func (s *scheme) pairTone(r Role, spec *roleSpec) float64 {
	pair := spec.pair
	aIsNearer := pair.polarity == nearer ||
		(pair.polarity == lighter && !s.dark) ||
		(pair.polarity == darker && s.dark)
	near, far := pair.a, pair.b
	if !aIsNearer {
		near, far = far, near
	}
	nearSpec, farSpec := &roleSpecs[near], &roleSpecs[far]
	expansion := -1.0
	if s.dark {
		expansion = 1
	}
	nTone, fTone := nearSpec.tone(s), farSpec.tone(s)
	if spec.background != nil {
		bgTone := s.tone(spec.background(s))
		nContrast := nearSpec.curve.get(s.contrast)
		fContrast := farSpec.curve.get(s.contrast)
		if ratioOfTones(bgTone, nTone) < nContrast || s.contrast < 0 {
			nTone = foregroundTone(bgTone, nContrast)
		}
		if ratioOfTones(bgTone, fTone) < fContrast || s.contrast < 0 {
			fTone = foregroundTone(bgTone, fContrast)
		}
	}
	delta := pair.delta
	if (fTone-nTone)*expansion < delta {
		fTone = clampUpTo(100, nTone+float64(delta*expansion))
		if (fTone-nTone)*expansion < delta {
			nTone = clampUpTo(100, fTone-float64(delta*expansion))
		}
	}
	settle := func() {
		if expansion > 0 {
			nTone = 60
			fTone = max(fTone, nTone+float64(delta*expansion))
		} else {
			nTone = 49
			fTone = min(fTone, nTone+float64(delta*expansion))
		}
	}
	switch {
	case 50 <= nTone && nTone < 60:
		settle()
	case 50 <= fTone && fTone < 60 && pair.stayTogether:
		settle()
	case 50 <= fTone && fTone < 60 && expansion > 0:
		fTone = 60
	case 50 <= fTone && fTone < 60:
		fTone = 49
	}
	if r == near {
		return nTone
	}
	return fTone
}

func findDesiredChromaByTone(hue, chroma, tone float64, byDecreasingTone bool) float64 {
	answer := tone
	closest := SolveHCT(hue, chroma, tone)
	if closest.Chroma >= chroma {
		return answer
	}
	peak := closest.Chroma
	step := 1.0
	if byDecreasingTone {
		step = -1
	}
	for closest.Chroma < chroma {
		answer += step
		candidate := SolveHCT(hue, chroma, answer)
		if peak > candidate.Chroma || math.Abs(candidate.Chroma-chroma) < 0.4 {
			break
		}
		if math.Abs(candidate.Chroma-chroma) < math.Abs(closest.Chroma-chroma) {
			closest = candidate
		}
		peak = max(peak, candidate.Chroma)
	}
	return answer
}
