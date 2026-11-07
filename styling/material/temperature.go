package material

import (
	"math"
	"slices"

	"github.com/stubbedev/lys-lib/third_party/gomath"
)

type temperatureCache struct {
	input  HCT
	byHue  [361]HCT
	temps  [362]float64
	coldI  int
	warmI  int
	byTemp []int
}

const temperatureInput = 361

func newTemperatureCache(input HCT) *temperatureCache {
	t := &temperatureCache{input: input}
	for hue := range 361 {
		t.byHue[hue] = SolveHCT(float64(hue), input.Chroma, input.Tone)
		t.temps[hue] = rawTemperature(t.byHue[hue])
	}
	t.temps[temperatureInput] = rawTemperature(input)
	t.byTemp = make([]int, 362)
	for i := range t.byTemp {
		t.byTemp[i] = i
	}
	slices.SortStableFunc(t.byTemp, func(a, b int) int {
		d := t.temps[a] - t.temps[b]
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return 0
	})
	t.coldI, t.warmI = t.byTemp[0], t.byTemp[len(t.byTemp)-1]
	return t
}

func (t *temperatureCache) hct(i int) HCT {
	if i == temperatureInput {
		return t.input
	}
	return t.byHue[i]
}

func (t *temperatureCache) relativeTemperature(i int) float64 {
	rng := t.temps[t.warmI] - t.temps[t.coldI]
	diff := t.temps[i] - t.temps[t.coldI]
	if rng == 0 {
		return 0.5
	}
	return diff / rng
}

func (t *temperatureCache) analogous(count, divisions int) []HCT {
	startHue := int(jsRound(t.input.Hue))
	lastTemp := t.relativeTemperature(startHue)
	all := []int{startHue}
	absoluteTotal := 0.0
	for i := range 360 {
		temp := t.relativeTemperature(sanitizeDegreesInt(startHue + i))
		absoluteTotal += math.Abs(temp - lastTemp)
		lastTemp = temp
	}
	hueAddend := 1
	tempStep := absoluteTotal / float64(divisions)
	total := 0.0
	lastTemp = t.relativeTemperature(startHue)
	for len(all) < divisions {
		hue := sanitizeDegreesInt(startHue + hueAddend)
		temp := t.relativeTemperature(hue)
		total += math.Abs(temp - lastTemp)
		satisfied := total >= float64(len(all))*tempStep
		indexAddend := 1
		for satisfied && len(all) < divisions {
			all = append(all, hue)
			satisfied = total >= float64(len(all)+indexAddend)*tempStep
			indexAddend++
		}
		lastTemp = temp
		hueAddend++
		if hueAddend > 360 {
			for len(all) < divisions {
				all = append(all, hue)
			}
			break
		}
	}
	answers := []HCT{t.input}
	increase := (count - 1) / 2
	for i := 1; i < increase+1; i++ {
		index := -i
		for index < 0 {
			index += len(all)
		}
		index %= len(all)
		answers = slices.Insert(answers, 0, t.byHue[all[index]])
	}
	decrease := count - increase - 1
	for i := 1; i < decrease+1; i++ {
		answers = append(answers, t.byHue[all[i%len(all)]])
	}
	return answers
}

func (t *temperatureCache) complement() HCT {
	coldestHue, warmestHue := t.hct(t.coldI).Hue, t.hct(t.warmI).Hue
	coldestTemp := t.temps[t.coldI]
	rng := t.temps[t.warmI] - coldestTemp
	coldToWarm := isBetween(t.input.Hue, coldestHue, warmestHue)
	startHue, endHue := coldestHue, warmestHue
	if coldToWarm {
		startHue, endHue = warmestHue, coldestHue
	}
	smallestError := 1000.0
	answer := int(jsRound(t.input.Hue))
	complementRelative := 1.0 - t.relativeTemperature(temperatureInput)
	for addend := 0.0; addend <= 360.0; addend++ {
		hue := sanitizeDegreesDouble(startHue + addend)
		if !isBetween(hue, startHue, endHue) {
			continue
		}
		candidate := int(jsRound(hue))
		relative := (t.temps[candidate] - coldestTemp) / rng
		if e := math.Abs(complementRelative - relative); e < smallestError {
			smallestError, answer = e, candidate
		}
	}
	return t.byHue[answer]
}

func isBetween(angle, a, b float64) bool {
	if a < b {
		return a <= angle && angle <= b
	}
	return a <= angle || angle <= b
}

func rawTemperature(h HCT) float64 {
	lab := labFromARGB(h.argb)
	hue := sanitizeDegreesDouble(gomath.Atan2(lab[2], lab[1]) * 180.0 / math.Pi)
	chroma := math.Sqrt(float64(lab[1]*lab[1]) + float64(lab[2]*lab[2]))
	return -0.5 + float64(0.02*gomath.Pow(chroma, 1.07)*gomath.Cos(sanitizeDegreesDouble(hue-50.0)*math.Pi/180.0))
}
