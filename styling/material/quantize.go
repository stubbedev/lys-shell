package material

import (
	"math"
)

// Population is one quantized color and the number of pixels it stands
// for.
type Population struct {
	Color ARGB
	Count int
}

const (
	wuIndexBits  = 5
	wuSideLength = 1<<wuIndexBits + 1
	wuTotalSize  = wuSideLength * wuSideLength * wuSideLength
)

type wuBox struct {
	r0, r1, g0, g1, b0, b1, vol int
}

type wuDirection uint8

const (
	wuRed wuDirection = iota
	wuGreen
	wuBlue
)

type quantizerWu struct {
	weights, momentsR, momentsG, momentsB, moments [wuTotalSize]float64
	cubes                                          []wuBox
}

func wuIndex(r, g, b int) int {
	return r<<(wuIndexBits*2) + r<<(wuIndexBits+1) + r + g<<wuIndexBits + g + b
}

func (q *quantizerWu) quantize(pixels []ARGB, maxColors int) []ARGB {
	q.constructHistogram(pixels)
	q.computeMoments()
	return q.createResult(q.createBoxes(maxColors))
}

func (q *quantizerWu) constructHistogram(pixels []ARGB) {
	for _, p := range pixels {
		if p.alpha() < 255 {
			continue
		}
		red, green, blue := int(p.red()), int(p.green()), int(p.blue())
		const bitsToRemove = 8 - wuIndexBits
		index := wuIndex(red>>bitsToRemove+1, green>>bitsToRemove+1, blue>>bitsToRemove+1)
		q.weights[index]++
		q.momentsR[index] += float64(red)
		q.momentsG[index] += float64(green)
		q.momentsB[index] += float64(blue)
		q.moments[index] += float64(red*red + green*green + blue*blue)
	}
}

func (q *quantizerWu) computeMoments() {
	for r := 1; r < wuSideLength; r++ {
		var area, areaR, areaG, areaB, area2 [wuSideLength]float64
		for g := 1; g < wuSideLength; g++ {
			var line, lineR, lineG, lineB, line2 float64
			for b := 1; b < wuSideLength; b++ {
				index := wuIndex(r, g, b)
				line += q.weights[index]
				lineR += q.momentsR[index]
				lineG += q.momentsG[index]
				lineB += q.momentsB[index]
				line2 += q.moments[index]
				area[b] += line
				areaR[b] += lineR
				areaG[b] += lineG
				areaB[b] += lineB
				area2[b] += line2
				previous := wuIndex(r-1, g, b)
				q.weights[index] = q.weights[previous] + area[b]
				q.momentsR[index] = q.momentsR[previous] + areaR[b]
				q.momentsG[index] = q.momentsG[previous] + areaG[b]
				q.momentsB[index] = q.momentsB[previous] + areaB[b]
				q.moments[index] = q.moments[previous] + area2[b]
			}
		}
	}
}

func (q *quantizerWu) createBoxes(maxColors int) int {
	q.cubes = make([]wuBox, maxColors)
	volumeVariance := make([]float64, maxColors)
	q.cubes[0] = wuBox{r1: wuSideLength - 1, g1: wuSideLength - 1, b1: wuSideLength - 1}
	generated := maxColors
	next := 0
	for i := 1; i < maxColors; i++ {
		if q.cut(&q.cubes[next], &q.cubes[i]) {
			volumeVariance[next] = q.boxVariance(&q.cubes[next])
			volumeVariance[i] = q.boxVariance(&q.cubes[i])
		} else {
			volumeVariance[next] = 0
			i--
		}
		next = 0
		temp := volumeVariance[0]
		for j := 1; j <= i; j++ {
			if volumeVariance[j] > temp {
				temp = volumeVariance[j]
				next = j
			}
		}
		if temp <= 0 {
			generated = i + 1
			break
		}
	}
	return generated
}

func (q *quantizerWu) boxVariance(cube *wuBox) float64 {
	if cube.vol <= 1 {
		return 0
	}
	return q.variance(cube)
}

func (q *quantizerWu) createResult(colorCount int) []ARGB {
	colors := make([]ARGB, 0, colorCount)
	for i := range colorCount {
		cube := &q.cubes[i]
		weight := volume(cube, &q.weights)
		if weight <= 0 {
			continue
		}
		r := int(jsRound(volume(cube, &q.momentsR) / weight))
		g := int(jsRound(volume(cube, &q.momentsG) / weight))
		b := int(jsRound(volume(cube, &q.momentsB) / weight))
		colors = append(colors, ARGB(0xff000000|uint32(r&0xff)<<16|uint32(g&0xff)<<8|uint32(b&0xff)))
	}
	return colors
}

func (q *quantizerWu) variance(cube *wuBox) float64 {
	dr := volume(cube, &q.momentsR)
	dg := volume(cube, &q.momentsG)
	db := volume(cube, &q.momentsB)
	m := &q.moments
	xx := m[wuIndex(cube.r1, cube.g1, cube.b1)] -
		m[wuIndex(cube.r1, cube.g1, cube.b0)] -
		m[wuIndex(cube.r1, cube.g0, cube.b1)] +
		m[wuIndex(cube.r1, cube.g0, cube.b0)] -
		m[wuIndex(cube.r0, cube.g1, cube.b1)] +
		m[wuIndex(cube.r0, cube.g1, cube.b0)] +
		m[wuIndex(cube.r0, cube.g0, cube.b1)] -
		m[wuIndex(cube.r0, cube.g0, cube.b0)]
	hypotenuse := float64(dr*dr) + float64(dg*dg) + float64(db*db)
	return xx - hypotenuse/volume(cube, &q.weights)
}

func (q *quantizerWu) cut(one, two *wuBox) bool {
	wholeR := volume(one, &q.momentsR)
	wholeG := volume(one, &q.momentsG)
	wholeB := volume(one, &q.momentsB)
	wholeW := volume(one, &q.weights)
	cutR, maxR := q.maximize(one, wuRed, one.r0+1, one.r1, wholeR, wholeG, wholeB, wholeW)
	cutG, maxG := q.maximize(one, wuGreen, one.g0+1, one.g1, wholeR, wholeG, wholeB, wholeW)
	cutB, maxB := q.maximize(one, wuBlue, one.b0+1, one.b1, wholeR, wholeG, wholeB, wholeW)
	var direction wuDirection
	switch {
	case maxR >= maxG && maxR >= maxB:
		if cutR < 0 {
			return false
		}
		direction = wuRed
	case maxG >= maxR && maxG >= maxB:
		direction = wuGreen
	default:
		direction = wuBlue
	}
	two.r1, two.g1, two.b1 = one.r1, one.g1, one.b1
	switch direction {
	case wuRed:
		one.r1 = cutR
		two.r0, two.g0, two.b0 = one.r1, one.g0, one.b0
	case wuGreen:
		one.g1 = cutG
		two.r0, two.g0, two.b0 = one.r0, one.g1, one.b0
	case wuBlue:
		one.b1 = cutB
		two.r0, two.g0, two.b0 = one.r0, one.g0, one.b1
	}
	one.vol = (one.r1 - one.r0) * (one.g1 - one.g0) * (one.b1 - one.b0)
	two.vol = (two.r1 - two.r0) * (two.g1 - two.g0) * (two.b1 - two.b0)
	return true
}

func (q *quantizerWu) maximize(cube *wuBox, direction wuDirection, first, last int, wholeR, wholeG, wholeB, wholeW float64) (int, float64) {
	bottomR := bottom(cube, direction, &q.momentsR)
	bottomG := bottom(cube, direction, &q.momentsG)
	bottomB := bottom(cube, direction, &q.momentsB)
	bottomW := bottom(cube, direction, &q.weights)
	best, cut := 0.0, -1
	for i := first; i < last; i++ {
		halfR := bottomR + top(cube, direction, i, &q.momentsR)
		halfG := bottomG + top(cube, direction, i, &q.momentsG)
		halfB := bottomB + top(cube, direction, i, &q.momentsB)
		halfW := bottomW + top(cube, direction, i, &q.weights)
		if halfW == 0 {
			continue
		}
		temp := (float64(halfR*halfR) + float64(halfG*halfG) + float64(halfB*halfB)) / halfW
		halfR, halfG, halfB, halfW = wholeR-halfR, wholeG-halfG, wholeB-halfB, wholeW-halfW
		if halfW == 0 {
			continue
		}
		temp += (float64(halfR*halfR) + float64(halfG*halfG) + float64(halfB*halfB)) / halfW
		if temp > best {
			best, cut = temp, i
		}
	}
	return cut, best
}

func volume(cube *wuBox, m *[wuTotalSize]float64) float64 {
	return m[wuIndex(cube.r1, cube.g1, cube.b1)] -
		m[wuIndex(cube.r1, cube.g1, cube.b0)] -
		m[wuIndex(cube.r1, cube.g0, cube.b1)] +
		m[wuIndex(cube.r1, cube.g0, cube.b0)] -
		m[wuIndex(cube.r0, cube.g1, cube.b1)] +
		m[wuIndex(cube.r0, cube.g1, cube.b0)] +
		m[wuIndex(cube.r0, cube.g0, cube.b1)] -
		m[wuIndex(cube.r0, cube.g0, cube.b0)]
}

func bottom(cube *wuBox, direction wuDirection, m *[wuTotalSize]float64) float64 {
	switch direction {
	case wuRed:
		return -m[wuIndex(cube.r0, cube.g1, cube.b1)] +
			m[wuIndex(cube.r0, cube.g1, cube.b0)] +
			m[wuIndex(cube.r0, cube.g0, cube.b1)] -
			m[wuIndex(cube.r0, cube.g0, cube.b0)]
	case wuGreen:
		return -m[wuIndex(cube.r1, cube.g0, cube.b1)] +
			m[wuIndex(cube.r1, cube.g0, cube.b0)] +
			m[wuIndex(cube.r0, cube.g0, cube.b1)] -
			m[wuIndex(cube.r0, cube.g0, cube.b0)]
	}
	return -m[wuIndex(cube.r1, cube.g1, cube.b0)] +
		m[wuIndex(cube.r1, cube.g0, cube.b0)] +
		m[wuIndex(cube.r0, cube.g1, cube.b0)] -
		m[wuIndex(cube.r0, cube.g0, cube.b0)]
}

func top(cube *wuBox, direction wuDirection, position int, m *[wuTotalSize]float64) float64 {
	switch direction {
	case wuRed:
		return m[wuIndex(position, cube.g1, cube.b1)] -
			m[wuIndex(position, cube.g1, cube.b0)] -
			m[wuIndex(position, cube.g0, cube.b1)] +
			m[wuIndex(position, cube.g0, cube.b0)]
	case wuGreen:
		return m[wuIndex(cube.r1, position, cube.b1)] -
			m[wuIndex(cube.r1, position, cube.b0)] -
			m[wuIndex(cube.r0, position, cube.b1)] +
			m[wuIndex(cube.r0, position, cube.b0)]
	}
	return m[wuIndex(cube.r1, cube.g1, position)] -
		m[wuIndex(cube.r1, cube.g0, position)] -
		m[wuIndex(cube.r0, cube.g1, position)] +
		m[wuIndex(cube.r0, cube.g0, position)]
}

const (
	wsmeansMaxIterations       = 10
	wsmeansMinMovementDistance = 3.0
	wsmeansSeed                = 0x42688
)

type javaRandom struct{ seed uint64 }

const javaRandomMask = 1<<48 - 1

func newJavaRandom(seed uint64) javaRandom {
	return javaRandom{seed: (seed ^ 0x5DEECE66D) & javaRandomMask}
}

func (r *javaRandom) next(bits uint) uint64 {
	r.seed = (r.seed*0x5DEECE66D + 0xB) & javaRandomMask
	return r.seed >> (48 - bits)
}

func (r *javaRandom) float64() float64 {
	return float64(r.next(26)<<27+r.next(27)) / (1 << 53)
}

func labDistance(a, b [3]float64) float64 {
	dL, dA, dB := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return float64(dL*dL) + float64(dA*dA) + float64(dB*dB)
}

func quantizeWsmeans(input []ARGB, starting []ARGB, maxColors int) []Population {
	index := make(map[ARGB]int, len(input))
	points := make([][3]float64, 0, len(input))
	counts := make([]int, 0, len(input))
	for _, p := range input {
		if i, ok := index[p]; ok {
			counts[i]++
			continue
		}
		index[p] = len(points)
		points = append(points, labFromARGB(p))
		counts = append(counts, 1)
	}
	clusterCount := min(maxColors, len(points), len(starting))
	clusters := make([][3]float64, len(starting))
	for i, c := range starting {
		clusters[i] = labFromARGB(c)
	}
	rng := newJavaRandom(wsmeansSeed)
	clusterIndices := make([]int, len(points))
	for i := range clusterIndices {
		clusterIndices[i] = int(math.Floor(rng.float64() * float64(clusterCount)))
	}
	distances := make([]float64, clusterCount*clusterCount)
	for i := range distances {
		distances[i] = -1
	}
	pixelCountSums := make([]int, clusterCount)
	sums := make([][3]float64, clusterCount)
	for iteration := range wsmeansMaxIterations {
		for i := range clusterCount {
			for j := i + 1; j < clusterCount; j++ {
				d := labDistance(clusters[i], clusters[j])
				distances[j*clusterCount+i] = d
				distances[i*clusterCount+j] = d
			}
		}
		pointsMoved := 0
		for i, point := range points {
			previous := clusterIndices[i]
			previousDistance := labDistance(point, clusters[previous])
			minimumDistance := previousDistance
			newCluster := -1
			for j := range clusterCount {
				if distances[previous*clusterCount+j] >= 4*previousDistance {
					continue
				}
				if d := labDistance(point, clusters[j]); d < minimumDistance {
					minimumDistance, newCluster = d, j
				}
			}
			if newCluster != -1 && math.Abs(math.Sqrt(minimumDistance)-math.Sqrt(previousDistance)) > wsmeansMinMovementDistance {
				pointsMoved++
				clusterIndices[i] = newCluster
			}
		}
		if pointsMoved == 0 && iteration != 0 {
			break
		}
		clear(sums)
		clear(pixelCountSums)
		for i, point := range points {
			c, count := clusterIndices[i], counts[i]
			pixelCountSums[c] += count
			sums[c][0] += float64(point[0] * float64(count))
			sums[c][1] += float64(point[1] * float64(count))
			sums[c][2] += float64(point[2] * float64(count))
		}
		for i := range clusterCount {
			count := float64(pixelCountSums[i])
			if count == 0 {
				clusters[i] = [3]float64{}
				continue
			}
			clusters[i] = [3]float64{sums[i][0] / count, sums[i][1] / count, sums[i][2] / count}
		}
	}
	out := make([]Population, 0, clusterCount)
	seen := make(map[ARGB]struct{}, clusterCount)
	for i := range clusterCount {
		count := pixelCountSums[i]
		if count == 0 {
			continue
		}
		c := argbFromLab(clusters[i][0], clusters[i][1], clusters[i][2])
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, Population{Color: c, Count: count})
	}
	return out
}

// Quantize reduces the opaque pixels to at most maxColors colors with
// the Celebi quantizer (Wu boxes refined by WSMeans), in the order the
// reference implementation reports them.
func Quantize(pixels []ARGB, maxColors int) []Population {
	pixels = opaque(pixels)
	var wu quantizerWu
	return quantizeWsmeans(pixels, wu.quantize(pixels, maxColors), maxColors)
}

func opaque(pixels []ARGB) []ARGB {
	for i, p := range pixels {
		if p.alpha() == 255 {
			continue
		}
		out := append(make([]ARGB, 0, len(pixels)), pixels[:i]...)
		for _, q := range pixels[i+1:] {
			if q.alpha() == 255 {
				out = append(out, q)
			}
		}
		return out
	}
	return pixels
}
