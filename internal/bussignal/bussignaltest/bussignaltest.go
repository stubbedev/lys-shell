// Package bussignaltest is a reader of signals for a test's own that
// keeps no descriptor.
package bussignaltest

import (
	"errors"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

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
		if err := dbusx.RemoveSignal(conn, raw); err != nil {
			t.Errorf("remove the signal channel: %v", err)
		}
		if err := conn.RemoveMatchSignal(match...); err != nil && !errors.Is(err, dbus.ErrClosed) {
			t.Errorf("remove the signal match: %v", err)
		}
	})
	t.Cleanup(stop)
	return out, stop
}
