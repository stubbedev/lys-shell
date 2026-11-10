// Package bussignaltest holds what the tests of lys-shell's signal
// readers share: a descriptor to send in a signal, and the proof that
// every copy of it the bus handed the process was closed again.
package bussignaltest

import (
	"errors"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"golang.org/x/sys/unix"
)

// liveness bounds the wait for a reader to close its copies: a guard
// against a hang, not a speed bound.
const liveness = 10 * time.Second

// Descriptor is the write end of a pipe, to send in a signal, and
// closed, which closes that end and fails t unless every other copy of
// it in the process, one per signal channel the signal reached, is
// closed too within the liveness bound.
func Descriptor(t testing.TB) (fd dbus.UnixFD, closed func(what string)) {
	t.Helper()
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatalf("open a pipe: %v", err)
	}
	read, write := pipe[0], pipe[1]
	open := true
	closeWrite := func() {
		if !open {
			return
		}
		open = false
		if err := unix.Close(write); err != nil {
			t.Errorf("close the pipe's write end: %v", err)
		}
	}
	t.Cleanup(func() {
		closeWrite()
		if err := unix.Close(read); err != nil {
			t.Errorf("close the pipe's read end: %v", err)
		}
	})
	return dbus.UnixFD(write), func(what string) { //nolint:gosec // a descriptor number
		t.Helper()
		closeWrite()
		fds := []unix.PollFd{{Fd: int32(read), Events: unix.POLLIN}} //nolint:gosec // a descriptor number
		for deadline := time.Now().Add(liveness); ; {
			n, err := unix.Poll(fds, int(time.Until(deadline).Milliseconds()))
			if errors.Is(err, unix.EINTR) && time.Now().Before(deadline) {
				continue
			}
			if err != nil || n != 1 || fds[0].Revents&unix.POLLHUP == 0 {
				t.Fatalf("%s: poll the pipe = %d, %v, events %#x; want a hangup, every copy of its write end closed", what, n, err, fds[0].Revents)
			}
			return
		}
	}
}
