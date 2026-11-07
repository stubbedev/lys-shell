package idleinhibit

import (
	"fmt"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// The freedesktop ScreenSaver identity Chromium, Electron and media players
// call to hold idle off: the interface at both paths apps use.
const (
	ScreenSaverName = "org.freedesktop.ScreenSaver"
	ScreenSaverPath = dbus.ObjectPath("/org/freedesktop/ScreenSaver")
	screenSaverAlt  = dbus.ObjectPath("/ScreenSaver")
)

type screenSaver struct{ d *Daemon }

// Inhibit holds idle for app until UnInhibit with the returned cookie, or
// until the caller leaves the bus.
func (s screenSaver) Inhibit(app, reason string, sender dbus.Sender) (uint32, *dbus.Error) {
	return s.d.Hold(app, reason, sender)
}

// UnInhibit ends a hold the caller took.
func (s screenSaver) UnInhibit(cookie uint32, sender dbus.Sender) *dbus.Error {
	return s.d.Release(cookie, sender)
}

// serveScreenSaver exports the ScreenSaver interface at both paths and
// takes its name.
func (d *Daemon) serveScreenSaver(conn *dbus.Conn) (func(), error) {
	iface := dbusx.Interface{Name: ScreenSaverName, Methods: screenSaver{d}}
	unexport, err := dbusx.Export(conn, ScreenSaverPath, iface)
	if err != nil {
		return nil, fmt.Errorf("idle inhibit: %w", err)
	}
	unexportAlt, err := dbusx.Export(conn, screenSaverAlt, iface)
	if err != nil {
		unexport()
		return nil, fmt.Errorf("idle inhibit: %w", err)
	}
	release, err := dbusx.Own(conn, ScreenSaverName)
	if err != nil {
		unexportAlt()
		unexport()
		return nil, fmt.Errorf("idle inhibit: %w", err)
	}
	return func() {
		release()
		unexportAlt()
		unexport()
	}, nil
}
