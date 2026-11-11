// Package bussignaltest holds what the tests of lys-shell's signal
// readers share: a descriptor to send in a signal, the proof that every
// copy of it the bus handed the process was closed again, and a reader
// of a test's own that keeps no descriptor.
package bussignaltest

import (
	"errors"
	"sync"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/bussignal"
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

// Signals hands the test the signals match brings conn, on a channel
// of size, until stop or the end of the test. The descriptors a signal
// carries are closed before the test gets it, so a test that reads
// none, or only some, of what it was sent holds no descriptor of them;
// the numbers left in a body name nothing. stop takes the match and
// the channel off conn, discarding what was not read, and returns once
// nothing more is sent on the channel; it may be called again.
func Signals(t testing.TB, conn *dbus.Conn, size int, match ...dbus.MatchOption) (signals <-chan *dbus.Signal, stop func()) {
	t.Helper()
	if err := conn.AddMatchSignal(match...); err != nil {
		t.Fatalf("match signals: %v", err)
	}
	raw := make(chan *dbus.Signal, size)
	out := make(chan *dbus.Signal, size)
	quit := make(chan struct{})
	var forward sync.WaitGroup
	conn.Signal(raw)
	forward.Go(func() {
		for {
			select {
			case <-quit:
				return
			case sig, ok := <-raw:
				if !ok {
					return
				}
				if err := sig.CloseFDs(); err != nil {
					t.Errorf("close the descriptors of %s: %v", sig.Name, err)
				}
				select {
				case out <- sig:
				case <-quit:
					return
				}
			}
		}
	})
	stop = sync.OnceFunc(func() {
		close(quit)
		forward.Wait()
		bussignal.Remove(conn, raw)
		if err := conn.RemoveMatchSignal(match...); err != nil && !errors.Is(err, dbus.ErrClosed) {
			t.Errorf("remove the signal match: %v", err)
		}
	})
	t.Cleanup(stop)
	return out, stop
}
