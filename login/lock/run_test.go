package lock

import (
	"errors"
	"testing"
)

// One lys-login holds a session locked: a second finds it held until the
// first lets go.
func TestHoldSessionIsOnePerSession(t *testing.T) {
	dir := t.TempDir()
	release, err := holdSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holdSession(dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("a second hold = %v, want ErrHeld", err)
	}
	release()
	again, err := holdSession(dir)
	if err != nil {
		t.Fatalf("a hold after the release = %v", err)
	}
	again()
	if other, err := holdSession(t.TempDir()); err != nil {
		t.Errorf("another session's hold = %v", err)
	} else {
		other()
	}
}

// The screen reports the end of every lock: an unlock, and a refusal by
// the compositor.
func TestScreenReportsTheEnd(t *testing.T) {
	h := newHarness(t, nil)
	ends := 0
	h.s.d.OnEnded = func() { ends++ }
	h.s.Lock()
	h.s.ForceUnlock()
	if ends != 1 || !h.s.unlocked {
		t.Errorf("after an unlock: %d ends, unlocked %v", ends, h.s.unlocked)
	}
	h.s.Lock()
	h.locker.cfg.OnFinished()
	if ends != 2 || h.s.unlocked {
		t.Errorf("after the compositor ended it: %d ends, unlocked %v", ends, h.s.unlocked)
	}
}
