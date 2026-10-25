package dbusx

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// busIface is the message bus's own interface, which announces a name
// gaining or losing its owner.
const busIface = "org.freedesktop.DBus"

// OwnerMatch matches the NameOwnerChanged signals about name.
func OwnerMatch(name string) []dbus.MatchOption {
	return []dbus.MatchOption{dbus.WithMatchInterface(busIface), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, name)}
}

// NameOwnerChange reads a NameOwnerChanged signal about name: the old
// and new owners ("" for none), and whether the signal is that one.
func NameOwnerChange(sig *dbus.Signal, name string) (oldOwner, newOwner string, ok bool) {
	if sig.Name != busIface+".NameOwnerChanged" || len(sig.Body) != 3 {
		return "", "", false
	}
	if n, _ := sig.Body[0].(string); n != name {
		return "", "", false
	}
	oldOwner, _ = sig.Body[1].(string)
	newOwner, _ = sig.Body[2].(string)
	return oldOwner, newOwner, true
}

// WatchOwner runs appeared, on a goroutine of its own, each time name
// gains an owner on conn, until ctx ends: a service that starts (or
// restarts) after its client did.
func WatchOwner(ctx context.Context, conn *dbus.Conn, name string, appeared func()) error {
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	if err := conn.AddMatchSignalContext(ctx, OwnerMatch(name)...); err != nil {
		conn.RemoveSignal(signals)
		return fmt.Errorf("watch %s: %w", name, err)
	}
	go func() {
		defer conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if _, owner, isName := NameOwnerChange(sig, name); isName && owner != "" {
					appeared()
				}
			}
		}
	}()
	return nil
}
