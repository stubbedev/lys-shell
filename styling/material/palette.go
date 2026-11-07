package material

import "math"

type tonalPalette struct {
	hue, chroma float64
	keyColor    HCT
}

func paletteFromHCT(h HCT) tonalPalette {
	return tonalPalette{hue: h.Hue, chroma: h.Chroma, keyColor: h}
}

func paletteFromHueAndChroma(hue, chroma float64) tonalPalette {
	return tonalPalette{hue: hue, chroma: chroma, keyColor: keyColor(hue, chroma)}
}

func (p tonalPalette) tone(tone float64) ARGB {
	if tone == 99 && isYellowHue(p.hue) {
		return averageARGB(p.tone(98), p.tone(100))
	}
	return solveToInt(p.hue, p.chroma, tone)
}

func (p tonalPalette) hct(tone float64) HCT { return HCTOf(p.tone(tone)) }

func averageARGB(a, b ARGB) ARGB {
	avg := func(x, y uint8) uint8 { return uint8(jsRound(float64((float64(x) + float64(y)) / 2))) }
	return RGB(avg(a.red(), b.red()), avg(a.green(), b.green()), avg(a.blue(), b.blue()))
}

const keyColorMaxChroma = 200.0

func keyColor(hue, requestedChroma float64) HCT {
	const pivotTone, toneStepSize, epsilon = 50, 1, 0.01
	var cache [102]float64
	var known [102]bool
	maxChroma := func(tone int) float64 {
		if !known[tone] {
			cache[tone], known[tone] = SolveHCT(hue, keyColorMaxChroma, float64(tone)).Chroma, true
		}
		return cache[tone]
	}
	lower, upper := 0, 100
	for lower < upper {
		mid := (lower + upper) / 2
		isAscending := maxChroma(mid) < maxChroma(mid+toneStepSize)
		sufficientChroma := maxChroma(mid) >= requestedChroma-epsilon
		switch {
		case sufficientChroma && math.Abs(float64(lower-pivotTone)) < math.Abs(float64(upper-pivotTone)):
			upper = mid
		case sufficientChroma:
			if lower == mid {
				return SolveHCT(hue, requestedChroma, float64(lower))
			}
			lower = mid
		case isAscending:
			lower = mid + toneStepSize
		default:
			upper = mid
		}
	}
	return SolveHCT(hue, requestedChroma, float64(lower))
}
