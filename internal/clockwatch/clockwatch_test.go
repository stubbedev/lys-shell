package clockwatch

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// vmEnv gates the tests that move the system clock and suspend the
// machine to the VM of tests/clockwatch-vm.nix, the one place they may run.
const vmEnv = "LYS_CLOCKWATCH_VM"

const jumpTimeout = 30 * time.Second

// watching runs Watch until the test ends and hands back its jumps.
func watching(t *testing.T) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	jumps := make(chan struct{}, 16)
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, func() { jumps <- struct{}{} }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Watch: %v", err)
			}
		case <-time.After(jumpTimeout):
			t.Error("Watch did not return after its context ended")
		}
	})
	return jumps
}

func TestAQuietClockIsNoJumpAndCancelEndsTheWatch(t *testing.T) {
	var jumps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, func() { jumps.Add(1) }) }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Watch returned on its own: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch after cancel = %v, want nil", err)
		}
	case <-time.After(jumpTimeout):
		t.Fatal("Watch did not return after its context ended")
	}
	if n := jumps.Load(); n != 0 {
		t.Errorf("a quiet clock reported %d jumps", n)
	}
}

func TestAnEndedContextReturnsAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Watch(ctx, func() { t.Error("a jump on an ended watch") }); err != nil {
		t.Errorf("Watch = %v, want nil", err)
	}
}

func requireVM(t *testing.T) {
	t.Helper()
	if os.Getenv(vmEnv) == "" {
		t.Skipf("moves the system clock; runs in tests/clockwatch-vm.nix with %s set", vmEnv)
	}
}

func wantJump(t *testing.T, jumps <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-jumps:
	case <-time.After(jumpTimeout):
		t.Fatalf("%s reported no jump", what)
	}
}

func drain(jumps <-chan struct{}) {
	for {
		select {
		case <-jumps:
		case <-time.After(500 * time.Millisecond):
			return
		}
	}
}

// Every step is reported, not only the first: a jump clears the timer.
func TestAStepOfTheClockIsAJump(t *testing.T) {
	requireVM(t)
	jumps := watching(t)
	time.Sleep(100 * time.Millisecond)
	for range 3 {
		now := time.Now().Add(time.Hour)
		ts := unix.NsecToTimespec(now.UnixNano())
		if err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts); err != nil {
			t.Fatal(err)
		}
		wantJump(t, jumps, "a clock step")
		drain(jumps)
	}
}

// sleepOnce takes the kernel through its whole suspend and resume of its
// core, timekeeping included, then back, short of the firmware a VM cannot
// return from.
func sleepOnce(t *testing.T) {
	t.Helper()
	for _, step := range [][2]string{{"pm_test", "core"}, {"state", "mem"}, {"pm_test", "none"}} {
		if err := os.WriteFile("/sys/power/"+step[0], []byte(step[1]), 0o600); err != nil {
			t.Fatalf("write %s to /sys/power/%s: %v", step[1], step[0], err)
		}
	}
}

// A sleep is reported on wake, and the next sleep again, with no logind or
// any other service involved.
func TestAResumeIsAJump(t *testing.T) {
	requireVM(t)
	jumps := watching(t)
	time.Sleep(100 * time.Millisecond)
	for range 2 {
		monoBefore := time.Now()
		var bootBefore unix.Timespec
		if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &bootBefore); err != nil {
			t.Fatal(err)
		}
		sleepOnce(t)
		var bootAfter unix.Timespec
		if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &bootAfter); err != nil {
			t.Fatal(err)
		}
		slept := time.Duration(bootAfter.Nano()-bootBefore.Nano()) - time.Since(monoBefore)
		if slept < 2*time.Second {
			t.Fatalf("slept %v in boot time: the machine did not sleep", slept)
		}
		wantJump(t, jumps, "a resume")
		drain(jumps)
	}
}
