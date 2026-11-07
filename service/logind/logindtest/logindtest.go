// Package logindtest is a fake systemd-logind on a private bus, seen
// from a caller outside any login session: a shell the systemd user
// manager started.
package logindtest

import (
	"io"
	"os"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/logind"
)

// errNoSuchSession is logind's reply when a name resolves to no session.
const errNoSuchSession = "org.freedesktop.login1.NoSuchSession"

// autoPath is logind's session object alias for the "auto" session.
const autoPath = logind.ManagerPath + "/session/auto"

// The entries Events records, in the order they happen.
const (
	EventLock              = "lock"
	EventLockedHint        = "locked-hint"
	EventUnlockedHint      = "unlocked-hint"
	EventInhibit           = "inhibit"
	EventInhibitorReleased = "inhibitor-released"
	EventSuspend           = "suspend"
)

// Logind is the fake. Its manager resolves "auto" to the user's display
// session, and "self" to nothing, since the caller is in no session.
// Likewise the /session/auto object alias is the display session, and
// /session/self does not exist.
type Logind struct {
	// Bus is the private bus logind owns its name on.
	Bus *dbustest.Bus
	// Terminated lists the session paths Terminate was called on.
	Terminated dbustest.Var[[]dbus.ObjectPath]
	// Locked lists the session paths Lock was called on, aliases
	// resolved.
	Locked dbustest.Var[[]dbus.ObjectPath]
	// LockedHint is what the sessions report for LockedHint.
	LockedHint dbustest.Var[bool]
	// Asked counts the GetSession calls, so a test can tell nobody came.
	Asked dbustest.Var[int]
	// Events lists the lock, LockedHint, inhibitor and suspend events in
	// the order they happened.
	Events dbustest.Var[[]string]
	// Inhibitors counts the sleep delay inhibitors held now.
	Inhibitors dbustest.Var[int]
	// OnLock runs after a Lock is recorded, as the lock screen answering
	// logind's Lock signal would; nil does nothing.
	OnLock  dbustest.Var[func()]
	conn    *dbus.Conn
	display dbus.ObjectPath
}

// Start runs the fake on a private bus. display is the user's display
// session; "" is a user without one.
func Start(t *testing.T, display dbus.ObjectPath) *Logind {
	t.Helper()
	l := &Logind{Bus: dbustest.Start(t), display: display}
	conn := l.Bus.Conn(t)
	l.conn = conn
	if err := conn.Export(manager{l}, logind.ManagerPath, logind.ManagerIf); err != nil {
		t.Fatalf("logindtest: export the manager: %v", err)
	}
	if display != "" {
		for _, path := range []dbus.ObjectPath{display, autoPath} {
			if err := conn.Export(session{l, display}, path, logind.SessionIf); err != nil {
				t.Fatalf("logindtest: export %s: %v", path, err)
			}
			if err := conn.Export(properties{l}, path, "org.freedesktop.DBus.Properties"); err != nil {
				t.Fatalf("logindtest: export the properties of %s: %v", path, err)
			}
		}
	}
	reply, err := conn.RequestName(logind.Service, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("logindtest: claim %s: %v %v", logind.Service, reply, err)
	}
	return l
}

// StartUnit runs the fake as the system bus of a process the systemd
// user manager started: XDG_SESSION_ID unset for the test, and every
// D-Bus client the test runs dialling the fake for the system bus.
func StartUnit(t *testing.T, display dbus.ObjectPath) *Logind {
	t.Helper()
	t.Setenv("XDG_SESSION_ID", "")
	if err := os.Unsetenv("XDG_SESSION_ID"); err != nil {
		t.Fatalf("logindtest: unset XDG_SESSION_ID: %v", err)
	}
	l := Start(t, display)
	l.Bus.UseAsSystemBus(t)
	return l
}

// SetLockedHint changes the display session's LockedHint and announces it
// with PropertiesChanged, as logind does when the lock screen sets it.
func (l *Logind) SetLockedHint(locked bool) error {
	l.LockedHint.Store(locked)
	event := EventUnlockedHint
	if locked {
		event = EventLockedHint
	}
	l.record(event)
	return l.conn.Emit(l.display, "org.freedesktop.DBus.Properties.PropertiesChanged", logind.SessionIf, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(locked)}, []string{})
}

// PrepareForSleep emits the manager's PrepareForSleep(start).
func (l *Logind) PrepareForSleep(start bool) error {
	return logind.PrepareForSleep.Emit(l.conn, logind.ManagerPath, logind.Sleep{Start: start})
}

func (l *Logind) record(event string) {
	l.Events.Update(func(events []string) []string { return append(events, event) })
}

type manager struct{ l *Logind }

func (m manager) GetSession(name string) (dbus.ObjectPath, *dbus.Error) {
	m.l.Asked.Update(func(n int) int { return n + 1 })
	if name != "auto" || m.l.display == "" {
		return "", dbus.NewError(errNoSuchSession, []any{"no session " + name})
	}
	return m.l.display, nil
}

func (m manager) LockSession(name string) *dbus.Error {
	path, err := m.GetSession(name)
	if err != nil {
		return err
	}
	return session{m.l, path}.Lock()
}

func (m manager) Suspend(bool) *dbus.Error {
	m.l.record(EventSuspend)
	return nil
}

func (m manager) Inhibit(what, _, _, mode string) (*os.File, *dbus.Error) {
	if what != "sleep" || mode != "delay" {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.NotSupported", []any{"the fake takes sleep delay inhibitors only"})
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	m.l.Inhibitors.Update(func(n int) int { return n + 1 })
	m.l.record(EventInhibit)
	go func() {
		_, _ = io.Copy(io.Discard, r)
		_ = r.Close()
		m.l.Inhibitors.Update(func(n int) int { return n - 1 })
		m.l.record(EventInhibitorReleased)
	}()
	return w, nil
}

type session struct {
	l    *Logind
	path dbus.ObjectPath
}

func (s session) Terminate() *dbus.Error {
	s.l.Terminated.Update(func(paths []dbus.ObjectPath) []dbus.ObjectPath { return append(paths, s.path) })
	return nil
}

func (s session) Lock() *dbus.Error {
	s.l.Locked.Update(func(paths []dbus.ObjectPath) []dbus.ObjectPath { return append(paths, s.path) })
	s.l.record(EventLock)
	if hook := s.l.OnLock.Load(); hook != nil {
		go hook()
	}
	return nil
}

type properties struct{ l *Logind }

func (p properties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != logind.SessionIf || name != "LockedHint" {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{iface + "." + name})
	}
	return dbus.MakeVariant(p.l.LockedHint.Load()), nil
}
