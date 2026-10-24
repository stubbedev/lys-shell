package idleinhibit

import (
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// Daemon is the dev.stubbe.lys.IdleInhibit1 object, which the
// `lys-shell idle` CLI drives from another process and the desktop
// portal holds idle through for apps.
type Daemon struct {
	state *State
	conn  *dbus.Conn
}

// NewDaemon wraps the state for export.
func NewDaemon(state *State) *Daemon { return &Daemon{state: state} }

// Export serves the interface under its well-known name and releases
// the holds of every caller that leaves the bus; the returned release
// stops both.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	d.conn = conn
	match := []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return nil, fmt.Errorf("idle inhibit: watch NameOwnerChanged: %w", err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	done := make(chan struct{})
	go d.releaseDeparted(signals, done)
	release, err := d.serve(conn)
	if err != nil {
		conn.RemoveSignal(signals)
		close(done)
		_ = conn.RemoveMatchSignal(match...)
		return nil, err
	}
	return func() {
		release()
		conn.RemoveSignal(signals)
		close(done)
		_ = conn.RemoveMatchSignal(match...)
	}, nil
}

// releaseDeparted drops the holds of every bus name that loses its
// owner, until done closes.
func (d *Daemon) releaseDeparted(signals <-chan *dbus.Signal, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case sig := <-signals:
			if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
				continue
			}
			name, _ := sig.Body[0].(string)
			newOwner, _ := sig.Body[2].(string)
			if newOwner == "" {
				d.state.ReleaseOwner(name)
			}
		}
	}
}

func (d *Daemon) serve(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{
		Name:      ServiceName,
		Path:      ServicePath,
		Interface: ServiceName,
		Methods:   d,
		Properties: dbusx.Getters{
			"Active":     func() any { return d.state.Active() },
			"Duration":   func() any { return d.state.Duration() },
			"Remaining":  func() any { return uint32(max(d.state.Remaining(), 0)) },
			"Indefinite": func() any { return d.state.Duration() == 0 },
		},
	})
}

// Hold inhibits idle for app until Release with the returned id, or
// until the caller leaves the bus.
func (d *Daemon) Hold(app, reason string, sender dbus.Sender) (uint32, *dbus.Error) {
	id := d.state.AddHold(Hold{Owner: string(sender), App: app, Reason: reason})
	var present bool
	err := d.conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, string(sender)).Store(&present)
	if err == nil && !present {
		d.state.ReleaseHold(id)
		return 0, dbusx.Failed("the caller left the bus")
	}
	return id, nil
}

// Release ends a hold the caller took.
func (d *Daemon) Release(id uint32, sender dbus.Sender) *dbus.Error {
	h, ok := d.state.Holds()[id]
	if !ok || h.Owner != string(sender) {
		return dbusx.Failed("no such hold")
	}
	d.state.ReleaseHold(id)
	return nil
}

// Enable turns inhibition on, indefinite or timed.
func (d *Daemon) Enable(indefinite bool) *dbus.Error {
	d.state.Enable(indefinite)
	return nil
}

// Disable turns inhibition off.
func (d *Daemon) Disable() *dbus.Error {
	d.state.Disable()
	return nil
}

// timerGuard refuses timer edits while inactive or indefinite.
func (d *Daemon) timerGuard(verb string) *dbus.Error {
	if !d.state.Active() {
		return dbusx.Failed("idle inhibit is not active")
	}
	if d.state.Duration() == 0 {
		return dbusx.Failed("cannot " + verb + " timer in indefinite mode")
	}
	return nil
}

// AdjustRemaining shifts the time left by minutes.
func (d *Daemon) AdjustRemaining(deltaMinutes int32) *dbus.Error {
	if err := d.timerGuard("adjust"); err != nil {
		return err
	}
	d.state.AdjustRemaining(deltaMinutes)
	return nil
}

// SetRemaining replaces the time left.
func (d *Daemon) SetRemaining(minutes uint32) *dbus.Error {
	if err := d.timerGuard("set"); err != nil {
		return err
	}
	d.state.SetRemaining(minutes)
	return nil
}

// SetDuration stores the duration in minutes.
func (d *Daemon) SetDuration(minutes uint32) *dbus.Error {
	d.state.SetDuration(minutes)
	return nil
}

// AdjustDuration shifts the stored duration.
func (d *Daemon) AdjustDuration(deltaMinutes int32) *dbus.Error {
	d.state.AdjustDuration(deltaMinutes)
	return nil
}
