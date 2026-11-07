package material

import (
	"math"
	"slices"
)

// FallbackSeed is the seed Score ranks when no color is suitable: a
// Google blue.
const FallbackSeed ARGB = 0xff4285f4

const (
	scoreDesired                 = 4
	scoreTargetChroma            = 48.0
	scoreWeightProportion        = 0.7
	scoreWeightChromaAbove       = 0.3
	scoreWeightChromaBelow       = 0.1
	scoreCutoffChroma            = 5.0
	scoreCutoffExcitedProportion = 0.01
)

// Score ranks the quantized colors as theme seeds, best first: up to four
// colors of distinct hue, weighing chroma against how much of the image
// lies near each hue. It is never empty: FallbackSeed stands in when no
// color qualifies.
func Score(colors []Population) []ARGB {
	hcts := make([]HCT, len(colors))
	var huePopulation [360]float64
	populationSum := 0.0
	for i, p := range colors {
		hcts[i] = HCTOf(p.Color)
		huePopulation[int(math.Floor(hcts[i].Hue))] += float64(p.Count)
		populationSum += float64(p.Count)
	}
	var excited [360]float64
	for hue := range 360 {
		proportion := huePopulation[hue] / populationSum
		for i := hue - 14; i < hue+16; i++ {
			excited[sanitizeDegreesInt(i)] += proportion
		}
	}
	type scored struct {
		hct   HCT
		score float64
	}
	ranked := make([]scored, 0, len(hcts))
	for _, h := range hcts {
		proportion := excited[sanitizeDegreesInt(int(jsRound(h.Hue)))]
		if h.Chroma < scoreCutoffChroma || proportion <= scoreCutoffExcitedProportion {
			continue
		}
		chromaWeight := scoreWeightChromaAbove
		if h.Chroma < scoreTargetChroma {
			chromaWeight = scoreWeightChromaBelow
		}
		ranked = append(ranked, scored{h, float64(proportion*100.0*scoreWeightProportion) + float64((h.Chroma-scoreTargetChroma)*chromaWeight)})
	}
	slices.SortStableFunc(ranked, func(a, b scored) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		return 0
	})
	chosen := make([]HCT, 0, scoreDesired)
	for difference := 90; difference >= 15; difference-- {
		chosen = chosen[:0]
		for _, s := range ranked {
			if !slices.ContainsFunc(chosen, func(c HCT) bool { return differenceDegrees(s.hct.Hue, c.Hue) < float64(difference) }) {
				chosen = append(chosen, s.hct)
			}
			if len(chosen) >= scoreDesired {
				break
			}
		}
		if len(chosen) >= scoreDesired {
			break
		}
	}
	if len(chosen) == 0 {
		return []ARGB{FallbackSeed}
	}
	out := make([]ARGB, len(chosen))
	for i, h := range chosen {
		out[i] = h.argb
	}
	return out
}
