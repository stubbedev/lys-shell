package network

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
)

// unwatchTimeout bounds removing a watch's match rules, which runs once
// the watch's context may have ended.
const unwatchTimeout = 5 * time.Second

// signalWatch is a signal channel on the bar's shared system connection
// and the match rules feeding it, held until stop. Its reader discards
// each signal it has handled: none carries a descriptor it keeps.
type signalWatch struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
	matches [][]dbus.MatchOption
}

// watchSignals adds a channel of size and the match rules on conn. A
// rule the bus refuses takes back the ones added before it.
func watchSignals(ctx context.Context, conn *dbus.Conn, size int, matches ...[]dbus.MatchOption) (*signalWatch, error) {
	w := &signalWatch{conn: conn, signals: make(chan *dbus.Signal, size)}
	conn.Signal(w.signals)
	for _, match := range matches {
		if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
			return nil, errors.Join(fmt.Errorf("add match rule: %w", err), w.stop())
		}
		w.matches = append(w.matches, match)
	}
	return w, nil
}

// stop removes the channel, discarding the signals nobody read, and
// every rule the watch added, leaving the shared connection as it was
// before; a closed connection's rules went with it.
func (w *signalWatch) stop() error {
	bussignal.Remove(w.conn, w.signals)
	ctx, cancel := context.WithTimeout(context.Background(), unwatchTimeout)
	defer cancel()
	var errs []error
	for _, match := range w.matches {
		if err := w.conn.RemoveMatchSignalContext(ctx, match...); err != nil && !errors.Is(err, dbus.ErrClosed) {
			errs = append(errs, fmt.Errorf("remove match rule: %w", err))
		}
	}
	w.matches = nil
	return errors.Join(errs...)
}

// unwind is what a started service holds on the shared connection, taken
// back last first.
type unwind []func() error

func (u *unwind) push(stop func() error) { *u = append(*u, stop) }

func (u unwind) run() error {
	errs := make([]error, 0, len(u))
	for _, stop := range slices.Backward(u) {
		errs = append(errs, stop())
	}
	return errors.Join(errs...)
}
