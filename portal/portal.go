// Package portal is lys-shell's xdg-desktop-portal backend
// (crates/lys-shell-portal): the org.freedesktop.impl.portal.* interfaces
// the compositor-independent xdg-desktop-portal frontend routes
// sandboxed apps' requests to, served as one object under the
// org.freedesktop.impl.portal.desktop.lys name that `lys-shell portal`
// claims. Interactive interfaces delegate their dialogs to the running
// shell over its own D-Bus services.
package portal

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/toplevels"
)

// BusName is the backend's well-known name (lys-shell.portal's DBusName).
const BusName = "org.freedesktop.impl.portal.desktop.lys"

// ObjectPath is where every interface is mounted.
const ObjectPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")

// Backend is the running portal backend.
type Backend struct {
	conn     *dbus.Conn
	cfg      *config.Service
	settings *settings
	sessions *sessions
	// spawn starts a program the portal hands off to (detach; a
	// recorder in tests).
	spawn     func(argv, env []string) error
	logind    *logind
	inhibitor *inhibitor
	notifier  *notifier
	clipboard *clipboardBridge
	shortcuts *globalShortcuts
	// sizes is the running streams' sizes, ScreenCast's for RemoteDesktop.
	sizes      *streamSizes
	screenCast *screenCast
	remote     *remoteDesktop
	// windows is the compositor's window list (a fake in tests).
	windows windowSource
}

// New builds the backend over a session-bus connection and the config.
func New(conn *dbus.Conn, cfg *config.Service) *Backend {
	b := &Backend{
		conn: conn, cfg: cfg, sessions: newSessions(conn),
		settings: &settings{cfg: cfg, gtk: hostGTKSettings, getenv: os.Getenv},
		spawn:    detach, logind: &logind{}, notifier: newNotifier(conn),
	}
	b.inhibitor = newInhibitor(conn, b.sessions, b.logind)
	b.windows = &windowList{conn: conn, watch: toplevels.Watch}
	// One Wayland connection serves every interface that needs the
	// compositor, started by the first of them.
	b.sizes = &streamSizes{sizes: map[uint32][2]int32{}}
	b.screenCast = newScreenCast(conn, b.sessions, b.sizes, startScreenStream)
	b.remote = newRemoteDesktop(conn, b.sessions, b.sizes, dialInput)
	loop := &waylandLoop{}
	b.clipboard = newClipboardBridge(conn, b.sessions, waylandClipboardStarter(loop))
	b.shortcuts = newGlobalShortcuts(conn, b.sessions, waylandShortcutsStarter(loop))
	loop.onEnd(b.clipboard.loopEnded)
	loop.onEnd(b.shortcuts.loopEnded)
	return b
}

// interfaces is every interface the backend mounts at ObjectPath.
func (b *Backend) interfaces() []dbusx.Interface {
	return append([]dbusx.Interface{
		b.settings.iface(),
		lockdown{b.cfg}.iface(),
		background{b.conn, b.notifier, b.windows}.iface(),
		usbIface(b.conn),
		emailIface(b.conn, b.spawn),
		secretIface(b.conn),
		b.inhibitor.iface(),
		screenshotIface(b.conn),
		fileChooserIface(b.conn),
		printIface(b.conn),
		b.clipboard.iface(),
		b.shortcuts.iface(),
		b.screenCast.iface(),
		b.remote.iface(),
		b.notifier.iface(),
		wallpaperIface(b.conn, b.cfg),
	}, dialogIfaces(b.conn, b.cfg)...)
}

// Serve mounts the interfaces, claims BusName (failing loudly when
// another backend owns it rather than queueing and idling forever) and
// starts the settings watcher and the notification action forwarder.
// The returned stop ends both, runs
// every live session's cleanup and drops the name.
func (b *Backend) Serve() (stop func(), err error) {
	if _, err := dbusx.Export(b.conn, ObjectPath, b.interfaces()...); err != nil {
		return nil, fmt.Errorf("cannot register D-Bus object: %w", err)
	}
	release, err := dbusx.Own(b.conn, BusName)
	if err != nil {
		return nil, fmt.Errorf("cannot request D-Bus name: %w", err)
	}
	unforward, err := b.notifier.forward()
	if err != nil {
		// Buttons still show; their presses just do not reach the app.
		warnf("notification: cannot subscribe to ActionInvoked: %v", err)
		unforward = func() {}
	}
	unwatch := b.settings.watch(b.conn)
	unwatchLockdown := lockdown{b.cfg}.watch(b.conn)
	return func() {
		unwatchLockdown()
		unwatch()
		unforward()
		b.sessions.clearAll()
		b.windows.close()
		b.logind.close()
		release()
	}, nil
}
