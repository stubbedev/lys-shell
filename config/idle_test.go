package config

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/idle/idlepolicy"
)

func TestIdleTimeoutParsesDurationsAndNever(t *testing.T) {
	for raw, want := range map[string]time.Duration{"90s": 90 * time.Second, "5m": 5 * time.Minute, "1h30m": 90 * time.Minute} {
		var to IdleTimeout
		if err := to.UnmarshalConfig(raw); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if got, ok := to.Timeout().After(); !ok || got != want {
			t.Errorf("%q = %s %v, want %s", raw, got, ok, want)
		}
	}
	var never IdleTimeout
	if err := never.UnmarshalConfig("never"); err != nil {
		t.Fatal(err)
	}
	if _, ok := never.Timeout().After(); ok || never.MarshalConfig() != "never" {
		t.Errorf("never = %v", never.MarshalConfig())
	}
	for raw, want := range map[IdleTimeout]string{mustIdleTimeout("4m"): "4m", mustIdleTimeout("1h"): "1h", mustIdleTimeout("1h30m"): "1h30m", mustIdleTimeout("90s"): "1m30s"} {
		if got := raw.MarshalConfig(); got != want {
			t.Errorf("marshal = %v, want %s", got, want)
		}
	}
}

func TestIdleTimeoutRefusesWhatItCannotCount(t *testing.T) {
	for _, raw := range []any{"0s", "500ms", "-5m", "5", "soon", 300, ""} {
		var to IdleTimeout
		err := to.UnmarshalConfig(raw)
		if err == nil {
			t.Errorf("%v accepted", raw)
		}
		if s, ok := raw.(string); ok && (s == "0s" || s == "500ms" || s == "-5m") && !errors.Is(err, idlepolicy.ErrTimeoutRange) {
			t.Errorf("%q: %v, want ErrTimeoutRange", s, err)
		}
	}
	var d IdleDimPercent
	if err := d.UnmarshalConfig(int64(0)); !errors.Is(err, idlepolicy.ErrDimPercentRange) {
		t.Errorf("dim-percent 0: %v", err)
	}
}

func TestIdleDefaultsArmPerPowerSource(t *testing.T) {
	p := DefaultsIdle().Policy()
	ac, battery := p.Arms(idlepolicy.AC), p.Arms(idlepolicy.Battery)
	if len(ac) != 3 || len(battery) != 4 {
		t.Fatalf("defaults arm %v on AC and %v on battery", ac, battery)
	}
	if battery[3].Stage != idlepolicy.Suspend || battery[3].After != 15*time.Minute {
		t.Errorf("battery suspend = %+v", battery[3])
	}
	if !p.LockBeforeSleep || p.DimPercent.Percent() != 30 {
		t.Errorf("defaults = %+v", p)
	}
}
