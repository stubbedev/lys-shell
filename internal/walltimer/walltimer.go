// Package walltimer fires on wall-clock boundaries: the first instant of
// each minute or second as the date reads it, on time across suspend and
// when the clock is set, without waking in between.
package walltimer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// next is the first multiple of unit after t.
func next(t time.Time, unit time.Duration) time.Time { return t.Truncate(unit).Add(unit) }

// Start calls fire, off the caller's goroutine, on every multiple of
// unit since the epoch until ctx ends, and at once when the system clock
// is set. Several expiries missed in a suspend fire once on resume.
func Start(ctx context.Context, unit time.Duration, fire func()) error {
	fd, err := unix.TimerfdCreate(unix.CLOCK_REALTIME, unix.TFD_NONBLOCK|unix.TFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("walltimer: timerfd_create: %w", err)
	}
	if err := arm(fd, unit); err != nil {
		_ = unix.Close(fd)
		return err
	}
	file := os.NewFile(uintptr(fd), "walltimer")
	stop := context.AfterFunc(ctx, func() { _ = file.Close() })
	go func() {
		defer stop()
		wait(file, fd, unit, fire)
	}()
	return nil
}

func arm(fd int, unit time.Duration) error {
	spec := unix.ItimerSpec{
		Interval: unix.NsecToTimespec(unit.Nanoseconds()),
		Value:    unix.NsecToTimespec(next(time.Now(), unit).UnixNano()),
	}
	if err := unix.TimerfdSettime(fd, unix.TFD_TIMER_ABSTIME|unix.TFD_TIMER_CANCEL_ON_SET, &spec, nil); err != nil {
		return fmt.Errorf("walltimer: timerfd_settime: %w", err)
	}
	return nil
}

// wait reads expiries until the file closes. A clock set cancels the
// armed timer, so it fires and arms again.
func wait(file *os.File, fd int, unit time.Duration, fire func()) {
	var expiries [8]byte
	for {
		_, err := file.Read(expiries[:])
		switch {
		case err == nil:
		case errors.Is(err, unix.ECANCELED):
			if err := arm(fd, unit); err != nil {
				log.Printf("%v", err)
				return
			}
		case errors.Is(err, os.ErrClosed):
			return
		default:
			log.Printf("walltimer: read: %v", err)
			return
		}
		fire()
	}
}
