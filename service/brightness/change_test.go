package brightness

import (
	"testing"
)

func TestParseChangeAcceptsLevelsAndSteps(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		relative bool
		percent  int32
	}{
		{"+10", true, 10},
		{"-10", true, -10},
		{"50", false, 50},
		{"0", false, 0},
		{"100", false, 100},
		{"+0", true, 0},
		{"-0", true, 0},
		{"+250", true, 250},
		{"007", false, 7},
	} {
		got, err := ParseChange(tc.raw)
		if err != nil {
			t.Errorf("ParseChange(%q): %v", tc.raw, err)
			continue
		}
		if got.IsRelative() != tc.relative || got.Percent() != tc.percent {
			t.Errorf("ParseChange(%q) = relative %v percent %d, want %v %d", tc.raw, got.IsRelative(), got.Percent(), tc.relative, tc.percent)
		}
	}
}

func TestParseChangeRejectsWithAPreciseError(t *testing.T) {
	syntax := "expected N to set, +N to raise or -N to lower, with N a whole percent"
	for raw, want := range map[string]string{
		"abc":          syntax,
		"+":            syntax,
		"-":            syntax,
		"":             syntax,
		"+-5":          syntax,
		"--5":          syntax,
		"5%":           syntax,
		"+5.5":         syntax,
		" 5":           syntax,
		"0x10":         syntax,
		"101":          "101 is not in 0..=100",
		"+99999999999": "99999999999 is too large",
		"2147483648":   "2147483648 is too large",
	} {
		if got, err := ParseChange(raw); err == nil || err.Error() != want {
			t.Errorf("ParseChange(%q) = %+v, %v; want error %q", raw, got, err, want)
		}
	}
}

func TestNewChangeValidatesOnlyLevels(t *testing.T) {
	if c, err := NewChange(true, -500); err != nil || c.Percent() != -500 {
		t.Errorf("a big step: %+v %v", c, err)
	}
	if _, err := NewChange(false, -1); err == nil || err.Error() != "-1 is not in 0..=100" {
		t.Errorf("a negative level: %v", err)
	}
	if _, err := NewChange(false, 101); err == nil {
		t.Error("a level over 100 was accepted")
	}
}

func TestTargetStepsFromTheRoundedLevel(t *testing.T) {
	d := Device{Brightness: 498, Max: 1000}
	if got := Relative(10).Target(d, 0); got != 60 {
		t.Errorf("+10 from 49.8%% = %v, want 60", got)
	}
	if got := Relative(-10).Target(d, 0); got != 40 {
		t.Errorf("-10 from 49.8%% = %v, want 40", got)
	}
	level, _ := Absolute(30)
	if got := level.Target(d, 0); got != 30 {
		t.Errorf("30 = %v", got)
	}
	if got := Relative(80).Target(d, 0); got != 100 {
		t.Errorf("a step past full = %v, want 100", got)
	}
}

func TestTargetHoldsTheFloor(t *testing.T) {
	d := Device{Brightness: 10, Max: 100}
	if got := Relative(-50).Target(d, 5); got != 5 {
		t.Errorf("a step below the floor = %v, want 5", got)
	}
	zero, _ := Absolute(0)
	if got := zero.Target(d, 5); got != 5 {
		t.Errorf("0 with a 5%% floor = %v, want 5", got)
	}
	if got := zero.Target(d, 0); got != 0 {
		t.Errorf("0 with no floor = %v, want 0", got)
	}
	if got := Relative(5).Target(d, 5); got != 15 {
		t.Errorf("a step above the floor = %v, want 15", got)
	}
}

func TestTargetFloorIsAtLeastOneRawStep(t *testing.T) {
	coarse := Device{Brightness: 3, Max: 7}
	got := Relative(-100).Target(coarse, 5)
	if raw := rawLevel(got, coarse.Max); raw != 1 {
		t.Errorf("floor on a 7-step panel = %v%% (raw %d), want one raw step", got, raw)
	}
	if got := Relative(-100).Target(coarse, 0); rawLevel(got, coarse.Max) != 0 {
		t.Errorf("no floor still lit the panel at %v%%", got)
	}
}
