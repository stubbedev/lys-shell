// Package clockwatch reports when the wall clock jumps against the
// monotonic clock, which a resume from suspend or hibernation and a step
// of the clock both do. Go's timers run on the monotonic clock, which
// stands still through a suspend, so they cannot notice. The kernel says
// so itself through a timerfd, with no polling and no system service.
package clockwatch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// Watch calls onJump after every jump of the wall clock until ctx ends,
// when it returns nil. The callback runs on Watch's goroutine. Any other
// return is why the kernel will not report jumps.
func Watch(ctx context.Context, onJump func()) error {
	fd, err := unix.TimerfdCreate(unix.CLOCK_REALTIME, unix.TFD_CLOEXEC|unix.TFD_NONBLOCK)
	if err != nil {
		return fmt.Errorf("clockwatch: create a timerfd: %w", err)
	}
	timer := os.NewFile(uintptr(fd), "clockwatch-timerfd")
	defer func() { _ = timer.Close() }()
	defer context.AfterFunc(ctx, func() { _ = timer.Close() })()

	if err := arm(timer); err != nil {
		return ended(ctx, err)
	}
	var expirations [8]byte
	for {
		if _, err := timer.Read(expirations[:]); !errors.Is(err, unix.ECANCELED) {
			return ended(ctx, fmt.Errorf("clockwatch: wait on the timerfd: %w", err))
		}
		if err := arm(timer); err != nil {
			return ended(ctx, err)
		}
		onJump()
	}
}

// arm sets the timer to expire as far ahead as the clock reaches, so all
// it ever does is cancel on a jump. A jump clears it, so it is armed again
// before each onJump, and a jump in between is seen by that callback.
func arm(timer *os.File) error {
	conn, err := timer.SyscallConn()
	if err != nil {
		return fmt.Errorf("clockwatch: arm the timerfd: %w", err)
	}
	never := unix.ItimerSpec{Value: unix.NsecToTimespec(math.MaxInt64)}
	var armErr error
	if err := conn.Control(func(fd uintptr) {
		armErr = unix.TimerfdSettime(int(fd), unix.TFD_TIMER_ABSTIME|unix.TFD_TIMER_CANCEL_ON_SET, &never, nil)
	}); err != nil {
		return fmt.Errorf("clockwatch: arm the timerfd: %w", err)
	}
	if armErr != nil {
		return fmt.Errorf("clockwatch: arm the timerfd: %w", armErr)
	}
	return nil
}

// ended is nil when ctx ending is what stopped the watch, else err.
func ended(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
