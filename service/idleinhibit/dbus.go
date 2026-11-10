package idleinhibit

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/stubbedev/lys-desktop/service/idleinhibit/inhibitduration"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
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

// Export serves the interface under its well-known name, and
// org.freedesktop.ScreenSaver on the same holds unless another program
// owns that name, and releases the holds of every caller that leaves the
// bus; the returned release stops all of it.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	d.conn = conn
	departed, err := dbusx.WatchSignals(context.Background(), conn, dbusx.FromBus(),
		func(err error) { log.Printf("idle inhibit: %v", err) },
		bussignal.OnNameLost(func(name string) error {
			d.state.ReleaseOwner(name)
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("idle inhibit: %w", err)
	}
	unwatch := func() {
		if err := departed.Stop(); err != nil && !errors.Is(err, dbusx.ErrDisconnected) {
			log.Printf("idle inhibit: %v", err)
		}
	}
	release, err := d.serve(conn)
	if err != nil {
		unwatch()
		return nil, err
	}
	screenSaver, err := d.serveScreenSaver(conn)
	if err != nil {
		log.Printf("%v; apps calling org.freedesktop.ScreenSaver hold idle through its owner, not lys-shell", err)
		screenSaver = func() {}
	}
	return func() {
		screenSaver()
		release()
		unwatch()
	}, nil
}

func (d *Daemon) serve(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{
		Name:      ServiceName,
		Path:      ServicePath,
		Interface: ServiceName,
		Methods:   d,
		Properties: dbusx.Getters{
			"Active":     func() any { return d.state.Active() },
			"Duration":   func() any { return d.state.Duration().Minutes() },
			"Remaining":  func() any { return d.state.Remaining() },
			"Indefinite": func() any { return d.state.Duration().Indefinite() },
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
	if d.state.Duration().Indefinite() {
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

// SetDuration stores the duration in minutes, refusing one above
// MaxMinutes with InvalidArgs.
func (d *Daemon) SetDuration(minutes uint32) *dbus.Error {
	duration, err := inhibitduration.NewDuration(int64(minutes))
	if err != nil {
		return dbusx.InvalidArgs(err.Error())
	}
	d.state.SetDuration(duration)
	return nil
}

// AdjustDuration shifts the stored duration, refusing a shift past
// MaxMinutes with InvalidArgs.
func (d *Daemon) AdjustDuration(deltaMinutes int32) *dbus.Error {
	if err := d.state.AdjustDuration(deltaMinutes); err != nil {
		return dbusx.InvalidArgs(err.Error())
	}
	return nil
}
