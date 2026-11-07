package material

import (
	"math"

	"github.com/stubbedev/lys-lib/third_party/gomath"
)

type viewingConditions struct {
	n, aw, nbb, ncb, c, nc float64
	rgbD                   [3]float64
	fl, fLRoot, z          float64
}

var defaultViewing = makeViewingConditions()

func makeViewingConditions() viewingConditions {
	xyz := whitePointD65
	adaptingLuminance := (200.0 / math.Pi) * yFromLstar(50.0) / 100.0
	const backgroundLstar, surround = 50.0, 2.0
	rW := float64(xyz[0]*0.401288) + float64(xyz[1]*0.650173) + float64(xyz[2]*-0.051461)
	gW := float64(xyz[0]*-0.250268) + float64(xyz[1]*1.204414) + float64(xyz[2]*0.045854)
	bW := float64(xyz[0]*-0.002079) + float64(xyz[1]*0.048952) + float64(xyz[2]*0.953127)
	f := 0.8 + surround/10.0
	var c float64
	if f >= 0.9 {
		c = lerp(0.59, 0.69, (f-0.9)*10.0)
	} else {
		c = lerp(0.525, 0.59, (f-0.8)*10.0)
	}
	d := f * (1.0 - float64((1.0/3.6)*gomath.Exp((-adaptingLuminance-42.0)/92.0)))
	d = clampUpTo(1, d)
	rgbD := [3]float64{
		float64(d*(100.0/rW)) + 1.0 - d,
		float64(d*(100.0/gW)) + 1.0 - d,
		float64(d*(100.0/bW)) + 1.0 - d,
	}
	k := 1.0 / (float64(5.0*adaptingLuminance) + 1.0)
	k4 := k * k * k * k
	k4F := 1.0 - k4
	fl := float64(k4*adaptingLuminance) + float64(0.1*k4F*k4F*gomath.Cbrt(5.0*adaptingLuminance))
	n := yFromLstar(backgroundLstar) / whitePointD65[1]
	z := 1.48 + math.Sqrt(n)
	nbb := 0.725 / gomath.Pow(n, 0.2)
	factors := [3]float64{
		gomath.Pow((fl*rgbD[0]*rW)/100.0, 0.42),
		gomath.Pow((fl*rgbD[1]*gW)/100.0, 0.42),
		gomath.Pow((fl*rgbD[2]*bW)/100.0, 0.42),
	}
	var rgbA [3]float64
	for i, f := range factors {
		rgbA[i] = (400.0 * f) / (f + 27.13)
	}
	aw := (float64(2.0*rgbA[0]) + rgbA[1] + float64(0.05*rgbA[2])) * nbb
	return viewingConditions{
		n: n, aw: aw, nbb: nbb, ncb: nbb, c: c, nc: f, rgbD: rgbD,
		fl: fl, fLRoot: gomath.Pow(fl, 0.25), z: z,
	}
}

func cam16HueChroma(c ARGB) (hue, chroma float64) {
	vc := &defaultViewing
	lin := linearRGB(c)
	redL, greenL, blueL := lin[0], lin[1], lin[2]
	x := float64(0.41233895*redL) + float64(0.35762064*greenL) + float64(0.18051042*blueL)
	y := float64(0.2126*redL) + float64(0.7152*greenL) + float64(0.0722*blueL)
	z := float64(0.01932141*redL) + float64(0.11916382*greenL) + float64(0.95034478*blueL)
	rC := float64(0.401288*x) + float64(0.650173*y) - float64(0.051461*z)
	gC := float64(-0.250268*x) + float64(1.204414*y) + float64(0.045854*z)
	bC := float64(-0.002079*x) + float64(0.048952*y) + float64(0.953127*z)
	rD := vc.rgbD[0] * rC
	gD := vc.rgbD[1] * gC
	bD := vc.rgbD[2] * bC
	rAF := gomath.Pow((vc.fl*math.Abs(rD))/100.0, 0.42)
	gAF := gomath.Pow((vc.fl*math.Abs(gD))/100.0, 0.42)
	bAF := gomath.Pow((vc.fl*math.Abs(bD))/100.0, 0.42)
	rA := (signum(rD) * 400.0 * rAF) / (rAF + 27.13)
	gA := (signum(gD) * 400.0 * gAF) / (gAF + 27.13)
	bA := (signum(bD) * 400.0 * bAF) / (bAF + 27.13)
	a := (float64(11.0*rA) + float64(-12.0*gA) + bA) / 11.0
	b := (rA + gA - float64(2.0*bA)) / 9.0
	u := (float64(20.0*rA) + float64(20.0*gA) + float64(21.0*bA)) / 20.0
	p2 := (float64(40.0*rA) + float64(20.0*gA) + bA) / 20.0
	atanDegrees := (gomath.Atan2(b, a) * 180.0) / math.Pi
	hue = sanitizeDegreesDouble(atanDegrees)
	ac := p2 * vc.nbb
	j := 100.0 * gomath.Pow(ac/vc.aw, vc.c*vc.z)
	huePrime := hue
	if hue < 20.14 {
		huePrime = hue + 360
	}
	eHue := 0.25 * (gomath.Cos((huePrime*math.Pi)/180.0+2.0) + 3.8)
	p1 := (50000.0 / 13.0) * eHue * vc.nc * vc.ncb
	t := (p1 * math.Sqrt(float64(a*a)+float64(b*b))) / (u + 0.305)
	alpha := gomath.Pow(t, 0.9) * gomath.Pow(1.64-gomath.Pow(0.29, vc.n), 0.73)
	return hue, float64(alpha * math.Sqrt(j/100.0))
}
