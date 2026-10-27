package config

import (
	"strconv"
	"strings"
	"testing"
)

func TestLockBlurTakesTheLargest(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "lock:\n  blur: "+strconv.Itoa(MaxLockBlur)+"\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("the largest blur was refused: %v", err)
	}
	if got := cfg.Lock.Blur.Sigma(); got != MaxLockBlur {
		t.Errorf("blur = %v, want %d", got, MaxLockBlur)
	}
}

func TestLockBlurRefusesOutOfRange(t *testing.T) {
	for _, sigma := range []int64{MaxLockBlur + 1, 1 << 32, -1} {
		path := writeFile(t, t.TempDir(), "config.yaml", "lock:\n  blur: "+strconv.FormatInt(sigma, 10)+"\n")
		cfg, err := LoadFile(path)
		want := "Error: lock blur out of range: " + strconv.FormatInt(sigma, 10) + " px, valid 0-1000"
		if err == nil || !strings.Contains(err.Error(), "Field: lock.blur") || !strings.Contains(err.Error(), want) {
			t.Errorf("blur %d: err = %v, want the field's range diagnostic", sigma, err)
		}
		if got := cfg.Lock.Blur; got != DefaultsLock().Blur {
			t.Errorf("blur %d: kept %v, want the default", sigma, got.Sigma())
		}
	}
}

func TestLockBlurDefaultLoads(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "lock:\n  enabled: true\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("the default config was refused: %v", err)
	}
	if got := cfg.Lock.Blur.Sigma(); got != 0 {
		t.Errorf("default blur = %v, want none", got)
	}
}

func TestLockBlurIsABoundedSpin(t *testing.T) {
	m, ok := Field("lock.blur")
	if !ok || m.Kind != FieldInt || m.Min != 0 || m.Max != MaxLockBlur {
		t.Errorf("meta = %+v, want an int field in 0-%d", m, MaxLockBlur)
	}
}
