package lockclient

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary play `lys-login lock`: FAKE_LOCK names
// how it ends.
func TestMain(m *testing.M) {
	switch os.Getenv("FAKE_LOCK") {
	case "":
		os.Exit(m.Run())
	case "unlock":
		fmt.Println(LockedLine)
		os.Exit(0)
	case "held":
		os.Exit(ExitHeld)
	case "refused":
		os.Exit(ExitRefused)
	case "crash":
		fmt.Println(LockedLine)
		os.Exit(2)
	}
	os.Exit(99)
}

type rig struct {
	c      *Client
	starts atomic.Int32
	locked atomic.Int32
	ended  chan error
	mu     sync.Mutex
	plan   []string
	hint   bool
}

// newRig plays the lock processes plan names, one per start.
func newRig(t *testing.T, enabled bool, plan ...string) *rig {
	t.Helper()
	r := &rig{plan: plan, ended: make(chan error, 16), hint: true}
	r.c = New(func() bool { return enabled }, func() { r.locked.Add(1) }, func() (bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hint, nil
	})
	r.c.program = os.Args[0]
	r.c.start = func(cmd *exec.Cmd) (io.Reader, error) {
		r.mu.Lock()
		if len(r.plan) == 0 {
			r.mu.Unlock()
			return nil, errors.New("no more planned lock processes")
		}
		mode := r.plan[0]
		r.plan = r.plan[1:]
		r.mu.Unlock()
		r.starts.Add(1)
		cmd.Args = []string{os.Args[0], "-test.run=^$"}
		cmd.Env = append(os.Environ(), "FAKE_LOCK="+mode)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		return out, cmd.Start()
	}
	return r
}

// settle waits until no lock process runs.
func (r *rig) settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r.c.mu.Lock()
		running := r.c.running
		r.c.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lock process never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLockRunsTheLockProcessOnce(t *testing.T) {
	r := newRig(t, true, "unlock")
	r.c.Lock()
	r.c.Lock()
	r.settle(t)
	if n := r.starts.Load(); n != 1 {
		t.Errorf("%d lock processes for two requests while one ran", n)
	}
	if n := r.locked.Load(); n != 1 {
		t.Errorf("onLocked ran %d times, want once on the locked line", n)
	}
}

func TestDisabledLockStartsNothing(t *testing.T) {
	r := newRig(t, false, "unlock")
	r.c.Lock()
	if n := r.starts.Load(); n != 0 {
		t.Errorf("a disabled lock started %d processes", n)
	}
}

// A lock process that dies with the session still locked is started
// again; one that unlocked, found the lock held, or was refused is not.
func TestADeadLockClientIsReplacedWhileLocked(t *testing.T) {
	r := newRig(t, true, "crash", "unlock")
	r.c.Lock()
	deadline := time.Now().Add(10 * time.Second)
	for r.starts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	r.settle(t)
	if n := r.starts.Load(); n != 2 {
		t.Fatalf("%d lock processes, want the crashed one replaced once", n)
	}
	for _, mode := range []string{"held", "refused", "unlock"} {
		r := newRig(t, true, mode, "unlock")
		r.c.Lock()
		r.settle(t)
		time.Sleep(50 * time.Millisecond)
		if n := r.starts.Load(); n != 1 {
			t.Errorf("%s: %d lock processes, want no replacement", mode, n)
		}
	}
}

// A dead lock client is not replaced once logind says the session is
// unlocked, and the replacements are bounded.
func TestReplacementsAreBounded(t *testing.T) {
	r := newRig(t, true, "crash")
	r.hint = false
	r.c.Lock()
	r.settle(t)
	time.Sleep(50 * time.Millisecond)
	if n := r.starts.Load(); n != 1 {
		t.Errorf("an unlocked session got %d lock processes, want no replacement", n)
	}

	r = newRig(t, true, "crash", "crash", "crash", "crash", "crash", "crash")
	r.c.Lock()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.settle(t)
		time.Sleep(50 * time.Millisecond)
		r.c.mu.Lock()
		running := r.c.running
		r.c.mu.Unlock()
		if !running {
			break
		}
	}
	if n := r.starts.Load(); n != 1+maxRespawns {
		t.Errorf("%d lock processes for a client that always dies, want 1 + %d replacements", n, maxRespawns)
	}
}

func TestClaimLockOnStart(t *testing.T) {
	dir := t.TempDir()
	if !claimLockOnStart(dir) {
		t.Fatal("the first start of a session must lock")
	}
	if second, third := claimLockOnStart(dir), claimLockOnStart(dir); second || third {
		t.Error("a restart within the session must not lock")
	}
	if !claimLockOnStart("") {
		t.Error("without a runtime dir it must fail secure and lock")
	}
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 && !claimLockOnStart(ro) {
		t.Error("an unwritable runtime dir must fail secure and lock")
	}
}

func TestShouldRelock(t *testing.T) {
	if !shouldRelock(true, nil) {
		t.Error("LockedHint set: the lock client died holding the lock, re-acquire")
	}
	if shouldRelock(false, nil) {
		t.Error("LockedHint clear: a restart must not lock")
	}
	if shouldRelock(true, errors.New("no logind")) {
		t.Error("probe unavailable: fail soft")
	}
}
