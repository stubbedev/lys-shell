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
// independently (Python, double precision) on whole hundreds of Kelvin.
func TestMatrixMatchesHyprsunset(t *testing.T) {
	for _, tc := range []struct {
		temp int
		want [3]float64
	}{
		{1000, [3]float64{1, 0.266354585, 0}},
		{2500, [3]float64{1, 0.62378272, 0.274777165}},
		{4550, [3]float64{1, 0.85306747, 0.735035116}},
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
