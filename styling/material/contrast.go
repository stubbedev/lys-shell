package material

import "math"

// ContrastRatio is the WCAG contrast ratio between two colors, 1 to 21.
func ContrastRatio(a, b ARGB) float64 {
	return ratioOfYs(xyzFromARGB(a)[1], xyzFromARGB(b)[1])
}

func ratioOfTones(a, b float64) float64 {
	return ratioOfYs(yFromLstar(clampUpTo(100, a)), yFromLstar(clampUpTo(100, b)))
}

func ratioOfYs(y1, y2 float64) float64 {
	lighter := max(y1, y2)
	darker := y2
	if lighter == y2 {
		darker = y1
	}
	return (lighter + 5.0) / (darker + 5.0)
}

func lighterTone(tone, ratio float64) float64 {
	if tone < 0 || tone > 100 {
		return -1
	}
	darkY := yFromLstar(tone)
	lightY := float64(ratio*(darkY+5.0)) - 5.0
	actual := ratioOfYs(lightY, darkY)
	if actual < ratio && math.Abs(actual-ratio) > 0.04 {
		return -1
	}
	v := lstarFromY(lightY) + 0.4
	if v < 0 || v > 100 {
		return -1
	}
	return v
}

func darkerTone(tone, ratio float64) float64 {
	if tone < 0 || tone > 100 {
		return -1
	}
	lightY := yFromLstar(tone)
	darkY := ((lightY + 5.0) / ratio) - 5.0
	actual := ratioOfYs(lightY, darkY)
	if actual < ratio && math.Abs(actual-ratio) > 0.04 {
		return -1
	}
	v := lstarFromY(darkY) - 0.4
	if v < 0 || v > 100 {
		return -1
	}
	return v
}

func lighterUnsafe(tone, ratio float64) float64 {
	if v := lighterTone(tone, ratio); v >= 0 {
		return v
	}
	return 100
}

func darkerUnsafe(tone, ratio float64) float64 {
	if v := darkerTone(tone, ratio); v >= 0 {
		return v
	}
	return 0
}

func foregroundTone(bgTone, ratio float64) float64 {
	lighter := lighterUnsafe(bgTone, ratio)
	darker := darkerUnsafe(bgTone, ratio)
	lighterRatio := ratioOfTones(lighter, bgTone)
	darkerRatio := ratioOfTones(darker, bgTone)
	if tonePrefersLightForeground(bgTone) {
		negligible := math.Abs(lighterRatio-darkerRatio) < 0.1 && lighterRatio < ratio && darkerRatio < ratio
		if lighterRatio >= ratio || lighterRatio >= darkerRatio || negligible {
			return lighter
		}
		return darker
	}
	if darkerRatio >= ratio || darkerRatio >= lighterRatio {
		return darker
	}
	return lighter
}

func tonePrefersLightForeground(tone float64) bool { return jsRound(tone) < 60.0 }

type contrastCurve struct{ low, normal, medium, high float64 }

func (c contrastCurve) get(level float64) float64 {
	switch {
	case level <= -1:
		return c.low
	case level < 0:
		return lerp(c.low, c.normal, (level-(-1))/1)
	case level < 0.5:
		return lerp(c.normal, c.medium, (level-0)/0.5)
	case level < 1:
		return lerp(c.medium, c.high, (level-0.5)/0.5)
	}
	return c.high
}

func isDisliked(h HCT) bool {
	hue := jsRound(h.Hue)
	return hue >= 90 && hue <= 111 && jsRound(h.Chroma) > 16 && jsRound(h.Tone) < 65
}

func fixIfDisliked(h HCT) HCT {
	if isDisliked(h) {
		return SolveHCT(h.Hue, h.Chroma, 70)
	}
	return h
}
