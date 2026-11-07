package nightlight

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func labOf(rgb [3]float64) [3]float64 {
	r, g, b := srgbToLinear(rgb[0]), srgbToLinear(rgb[1]), srgbToLinear(rgb[2])
	x := (0.4124564*r + 0.3575761*g + 0.1804375*b) / 0.95047
	y := 0.2126729*r + 0.7151522*g + 0.0721750*b
	z := (0.0193339*r + 0.1191920*g + 0.9503041*b) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return [3]float64{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func deltaE2000(lab1, lab2 [3]float64) float64 {
	l1, a1, b1 := lab1[0], lab1[1], lab1[2]
	l2, a2, b2 := lab2[0], lab2[1], lab2[2]
	c1, c2 := math.Hypot(a1, b1), math.Hypot(a2, b2)
	cBar7 := math.Pow((c1+c2)/2, 7)
	g := 0.5 * (1 - math.Sqrt(cBar7/(cBar7+math.Pow(25, 7))))
	a1p, a2p := (1+g)*a1, (1+g)*a2
	c1p, c2p := math.Hypot(a1p, b1), math.Hypot(a2p, b2)
	hue := func(b, a float64) float64 {
		if a == 0 && b == 0 {
			return 0
		}
		h := math.Atan2(b, a) * 180 / math.Pi
		if h < 0 {
			h += 360
		}
		return h
	}
	h1p, h2p := hue(b1, a1p), hue(b2, a2p)
	dLp, dCp := l2-l1, c2p-c1p
	var dhp float64
	switch {
	case c1p*c2p == 0:
	case math.Abs(h2p-h1p) <= 180:
		dhp = h2p - h1p
	case h2p-h1p > 180:
		dhp = h2p - h1p - 360
	default:
		dhp = h2p - h1p + 360
	}
	dHp := 2 * math.Sqrt(c1p*c2p) * math.Sin(dhp*math.Pi/360)
	lBarp, cBarp := (l1+l2)/2, (c1p+c2p)/2
	var hBarp float64
	switch {
	case c1p*c2p == 0:
		hBarp = h1p + h2p
	case math.Abs(h1p-h2p) <= 180:
		hBarp = (h1p + h2p) / 2
	case h1p+h2p < 360:
		hBarp = (h1p + h2p + 360) / 2
	default:
		hBarp = (h1p + h2p - 360) / 2
	}
	rad := math.Pi / 180
	t := 1 - 0.17*math.Cos((hBarp-30)*rad) + 0.24*math.Cos(2*hBarp*rad) + 0.32*math.Cos((3*hBarp+6)*rad) - 0.20*math.Cos((4*hBarp-63)*rad)
	hueOff := (hBarp - 275) / 25
	dTheta := 30 * math.Exp(-hueOff*hueOff)
	cBarp7 := math.Pow(cBarp, 7)
	rc := 2 * math.Sqrt(cBarp7/(cBarp7+math.Pow(25, 7)))
	lOff := (lBarp - 50) * (lBarp - 50)
	sl := 1 + 0.015*lOff/math.Sqrt(20+lOff)
	sc := 1 + 0.045*cBarp
	sh := 1 + 0.015*cBarp*t
	rt := -math.Sin(2*dTheta*rad) * rc
	l, c, h := dLp/sl, dCp/sc, dHp/sh
	return math.Sqrt(l*l + c*c + h*h + rt*c*h)
}

func TestDeltaE2000MatchesSharmasPairs(t *testing.T) {
	for _, c := range []struct {
		a, b [3]float64
		want float64
	}{
		{[3]float64{50, 2.6772, -79.7751}, [3]float64{50, 0, -82.7485}, 2.0425},
		{[3]float64{50, -1.3802, -84.2814}, [3]float64{50, 0, -82.7485}, 1.0000},
		{[3]float64{50, 2.5, 0}, [3]float64{73, 25, -18}, 27.1492},
		{[3]float64{60.2574, -34.0099, 36.2677}, [3]float64{60.4626, -34.1751, 39.4387}, 1.2644},
		{[3]float64{2.0776, 0.0795, -1.1350}, [3]float64{0.9033, -0.0636, -0.5514}, 0.9082},
	} {
		if got := deltaE2000(c.a, c.b); math.Abs(got-c.want) > 1e-4 {
			t.Errorf("ΔE00(%v, %v) = %.4f, want %.4f", c.a, c.b, got, c.want)
		}
	}
}

type shownColour func(s Setting, code int) [3]float64

func ctmShown(s Setting, code int) [3]float64 {
	e := encodeMatrix(s.Matrix())
	v := float64(code) / 255
	return [3]float64{v * float64(e[0]) / 256, v * float64(e[4]) / 256, v * float64(e[8]) / 256}
}

func gammaShown(s Setting, code int) [3]float64 {
	const size = 256
	ramps := s.Ramps(size)
	return [3]float64{float64(ramps[code]) / math.MaxUint16, float64(ramps[size+code]) / math.MaxUint16, float64(ramps[2*size+code]) / math.MaxUint16}
}

func largestEaseStep(night Setting, shown shownColour, codes []int, sends int) (worst float64, at string) {
	for _, code := range codes {
		prev := labOf(shown(identity, code))
		for i := 1; i <= sends; i++ {
			lab := labOf(shown(night.eased(float64(i)/float64(sends)), code))
			if d := deltaE2000(prev, lab); d > worst {
				worst, at = d, fmt.Sprintf("grey %d, send %d", code, i)
			}
			prev = lab
		}
	}
	return worst, at
}

func TestNoEaseStepIsAJustNoticeableDifference(t *testing.T) {
	codes := []int{16, 32, 64, 96, 128, 160, 192, 224, 255}
	for _, strength := range [][2]int{{5000, 100}, {3500, 100}, {2500, 90}, {1000, 50}} {
		night, err := NewSetting(strength[0], strength[1])
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct {
			name  string
			shown shownColour
			step  time.Duration
		}{{"ctm", ctmShown, ctmEaseStep}, {"gamma", gammaShown, gammaEaseStep}} {
			worst, at := largestEaseStep(night, p.shown, codes, int(15*time.Minute/p.step))
			t.Logf("%dK/%d%% %s: largest step ΔE00 %.3f at %s", strength[0], strength[1], p.name, worst, at)
			if worst >= 1 {
				t.Errorf("%dK/%d%% on %s: a step of ΔE00 %.3f at %s is a just noticeable difference", strength[0], strength[1], p.name, worst, at)
			}
		}
	}
}
