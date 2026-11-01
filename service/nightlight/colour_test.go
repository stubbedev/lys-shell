package nightlight

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func mustSetting(t *testing.T, temp, gamma int) Setting {
	t.Helper()
	s, err := NewSetting(temp, gamma)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The whitepoints are hyprsunset's matrixForKelvin, evaluated
// independently (Python, double precision); between whole hundreds the
// fit is continuous, which hyprsunset's is not.
func TestMatrixMatchesHyprsunset(t *testing.T) {
	for _, tc := range []struct {
		temp int
		want [3]float64
	}{
		{1000, [3]float64{1, 0.266354585, 0}},
		{2500, [3]float64{1, 0.62378272, 0.274777165}},
		{4550, [3]float64{1, 0.857377808, 0.742740306}},
		{5000, [3]float64{1, 0.894166666, 0.807570275}},
		{6500, [3]float64{1, 0.996510133, 0.980556503}},
		{10000, [3]float64{0.790997435, 0.855179294, 1}},
		{20000, [3]float64{0.669426071, 0.777986321, 1}},
	} {
		m := mustSetting(t, tc.temp, 100).Matrix()
		got := [3]float64{m[0], m[4], m[8]}
		for i := range got {
			if math.Abs(got[i]-tc.want[i]) > 1e-8 {
				t.Errorf("%dK diagonal = %v, want %v", tc.temp, got, tc.want)
				break
			}
		}
		for _, off := range []int{1, 2, 3, 5, 6, 7} {
			if m[off] != 0 {
				t.Errorf("%dK matrix %v is not diagonal", tc.temp, m)
			}
		}
	}
}

// The neutral temperature is the fit's exact identity, so an ease ends
// where clearing the filter leaves the display: no last-moment step.
func TestNeutralTemperatureIsTheIdentity(t *testing.T) {
	if got := identity.Matrix(); got != (Matrix{1, 0, 0, 0, 1, 0, 0, 0, 1}) {
		t.Errorf("identity matrix = %v", got)
	}
	if !mustSetting(t, neutralTemp, 100).IsIdentity() {
		t.Error("the neutral temperature at full gamma is not the identity")
	}
	if mustSetting(t, neutralTemp, 99).IsIdentity() || mustSetting(t, 6500, 100).IsIdentity() {
		t.Error("a strength that tints reads as the identity")
	}
	if got := mustSetting(t, neutralTemp, 100).Ramps(3); !slices.Equal(got, []uint16{0, 32768, 65535, 0, 32768, 65535, 0, 32768, 65535}) {
		t.Errorf("identity ramps = %v", got)
	}
}

// An eased strength reads back rounded; the fraction is only for the
// colour.
func TestEasedStrengthReadsWhole(t *testing.T) {
	s := mustSetting(t, 3000, 81).eased(0.5)
	if s.Temp() != 4125 || s.Gamma() != 91 {
		t.Errorf("eased = %dK %d%%, want 4125K 91%%", s.Temp(), s.Gamma())
	}
}

func TestMatrixScalesByGamma(t *testing.T) {
	full := mustSetting(t, 5000, 100).Matrix()
	dim := mustSetting(t, 5000, 50).Matrix()
	for i := range full {
		if math.Abs(dim[i]-full[i]/2) > 1e-12 {
			t.Fatalf("50%% gamma = %v, want half of %v", dim, full)
		}
	}
	if off := mustSetting(t, 5000, 0).Matrix(); off != (Matrix{}) {
		t.Errorf("0%% gamma = %v, want black", off)
	}
}

func TestRampsMatchTheMatrix(t *testing.T) {
	got := mustSetting(t, 4000, 80).Ramps(4)
	want := []uint16{0, 17476, 34952, 52428, 0, 14105, 28211, 42316, 0, 11382, 22764, 34146}
	if !slices.Equal(got, want) {
		t.Errorf("ramps = %v, want %v", got, want)
	}
	// Above 100% gamma the ramps saturate instead of wrapping.
	if got := mustSetting(t, 5000, 150).Ramps(2); !slices.Equal(got, []uint16{0, 65535, 0, 65535, 0, 65535}) {
		t.Errorf("150%% ramps = %v, want saturated", got)
	}
	if n := len(mustSetting(t, 5000, 100).Ramps(1024)); n != 3*1024 {
		t.Errorf("1024-entry ramps have %d values", n)
	}
}

func TestNewSettingRejectsOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		temp, gamma int
		want        string
	}{
		{999, 100, "temperature 999K"},
		{20001, 100, "temperature 20001K"},
		{5000, -1, "gamma -1%"},
		{5000, 201, "gamma 201%"},
	} {
		_, err := NewSetting(tc.temp, tc.gamma)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("NewSetting(%d, %d) = %v, want an error naming %q", tc.temp, tc.gamma, err, tc.want)
		}
	}
	for _, ok := range [][2]int{{MinTemp, 0}, {MaxTemp, MaxGamma}} {
		if _, err := NewSetting(ok[0], ok[1]); err != nil {
			t.Errorf("NewSetting(%d, %d): %v", ok[0], ok[1], err)
		}
	}
}

// The fit's pieces stop short of the identity at the neutral temperature,
// green above it and blue below. An ease must not show that as a step at
// either end, on any channel, and must still arrive at the fit itself.
func TestAnEaseCrossesTheFitsSeamWithoutAStep(t *testing.T) {
	const visible = 0.005
	const steps = 900
	for _, temp := range []int{MinTemp, 3000, 6500, 6599, 6601, 6700, 7500, 10000, MaxTemp} {
		night := mustSetting(t, temp, 100)
		prev := night.eased(0).scale()
		if prev != ([3]float64{1, 1, 1}) {
			t.Errorf("%dK: the ease starts at %v, want the identity", temp, prev)
		}
		for i := 1; i <= steps; i++ {
			now := night.eased(float64(i) / steps).scale()
			for ch := range now {
				if d := math.Abs(now[ch] - prev[ch]); d > visible {
					t.Fatalf("%dK step %d: channel %d jumped by %v", temp, i, ch, d)
				}
				if now[ch] > prev[ch]+1e-12 {
					t.Fatalf("%dK step %d: channel %d turned back, %v to %v", temp, i, ch, prev[ch], now[ch])
				}
			}
			prev = now
		}
		if got := night.eased(1); got != night {
			t.Errorf("%dK: the ease ends at %+v, want %+v", temp, got, night)
		}
	}
}

// The seam is only ever closed along an ease: a settled strength is the
// fit, however close to neutral, with the seam's step still in it.
func TestASettledStrengthKeepsTheFitsEndValues(t *testing.T) {
	cool := mustSetting(t, neutralTemp+1, 100).scale()
	if want := whitepoint(neutralTemp + 1); cool != want {
		t.Errorf("6601K scale = %v, want the fit's %v", cool, want)
	}
	if cool[1] > 0.99 {
		t.Errorf("6601K green = %v: the fit's own value moved", cool[1])
	}
	warm := mustSetting(t, neutralTemp-1, 100).scale()
	if want := whitepoint(neutralTemp - 1); warm != want {
		t.Errorf("6599K scale = %v, want the fit's %v", warm, want)
	}
}

// Easing to the neutral temperature has no seam to close, so a schedule
// configured at the identity never asks for a filter.
func TestAnEaseToTheIdentityStaysTheIdentity(t *testing.T) {
	for _, fraction := range []float64{0, 0.001, 0.25, 0.5, 0.75, 0.999, 1} {
		if got := identity.eased(fraction); !got.IsIdentity() {
			t.Errorf("identity eased by %v = %+v", fraction, got)
		}
	}
	if mustSetting(t, 5000, 100).eased(0.5).IsIdentity() {
		t.Error("a strength partway through an ease reads as the identity")
	}
	if got := mustSetting(t, neutralTemp, 80).eased(0.5).scale(); got != [3]float64{0.9, 0.9, 0.9} {
		t.Errorf("a gamma-only ease = %v, want 90%% on every channel", got)
	}
}
