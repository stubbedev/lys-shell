// Package bussignal is what lys-shell's D-Bus signal readers share
// beyond dbusx's watches: the bus announcing that a name lost its owner,
// and the upkeep of a signal channel a reader holds itself because its
// signals come from paths or peers no watch can name ahead of time.
// Every channel owns the descriptors of the signals it is handed, so a
// reader that does not take them over discards each signal it has read
// and takes its channel off the connection with Remove.
package bussignal

import (
	"context"
	"errors"
	"log"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const (
	busName = "org.freedesktop.DBus"
	busPath = dbus.ObjectPath("/org/freedesktop/DBus")
)

type ownerChange struct {
	Name     string `dbus:"name"`
	OldOwner string `dbus:"old_owner"`
	NewOwner string `dbus:"new_owner"`
}

var nameOwnerChanged = dbusx.NewSignal[ownerChange](busName, "NameOwnerChanged")

// OnNameLost routes the bus's announcement that a name lost its owner
// to lost, in a watch of dbusx.FromBus: a well-known name released, or
// the unique name of a peer that left the bus. lost also gets the
// announcement's sequence on the connection, which orders the loss
// against a reply the name's owner sent (dbus.Call.ResponseSequence):
// an answer sequenced before it came from an owner that has left since.
// WithArg0 narrows the route to one name.
func OnNameLost(lost func(name string, announced dbus.Sequence) error) dbusx.Route {
	return dbusx.On(nameOwnerChanged, busPath, func(change ownerChange, announced dbus.Sequence) error {
		if change.NewOwner != "" {
			return nil
		}
		return lost(change.Name, announced)
	})
}

// Discard closes the descriptors sig carries, for the reader of a
// channel that has read it and takes none of them over. A signal
// without descriptors costs nothing.
func Discard(sig *dbus.Signal) {
	if err := sig.CloseFDs(); err != nil {
		log.Printf("dbus: discard %s from %s: %v", sig.Name, sig.Sender, err)
	}
}

// Remove takes signals off conn and discards the signals it was sent
// and nobody read.
func Remove(conn *dbus.Conn, signals chan *dbus.Signal) {
	conn.RemoveSignal(signals)
	for {
		select {
		case sig, open := <-signals:
			if !open {
				return
			}
			Discard(sig)
		default:
			return
		}
	}
}

// Ticks is a watch as a feed of ticks: routes builds the watch's routes
// around tick, which a handler calls for a signal that changed what the
// caller reads. One tick is pending at most. The feed closes once the
// watch has ended, with ctx, with stop or with the connection; stop
// returns once the watch has taken its match rules off conn. A signal
// that does not decode is logged under what.
func Ticks(ctx context.Context, conn *dbus.Conn, from dbusx.Sender, what string, routes func(tick func()) []dbusx.Route) (ticks <-chan struct{}, stop func(), err error) {
	feed := make(chan struct{}, 1)
	watch, err := dbusx.WatchSignals(ctx, conn, from,
		func(err error) { log.Printf("%s: %v", what, err) },
		routes(func() {
			select {
			case feed <- struct{}{}:
			default:
			}
		})...)
	if err != nil {
		return nil, nil, err
	}
	go func() {
		<-watch.Done()
		close(feed)
	}()
	return feed, func() {
		if err := watch.Stop(); err != nil && !errors.Is(err, dbusx.ErrDisconnected) {
			log.Printf("%s: %v", what, err)
		}
	}, nil
}
