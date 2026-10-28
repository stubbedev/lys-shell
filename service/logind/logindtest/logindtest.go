// Package logindtest is a fake systemd-logind on a private bus, seen
// from a caller outside any login session: a shell the systemd user
// manager started.
package logindtest

import (
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
	Locked  dbustest.Var[[]dbus.ObjectPath]
	display dbus.ObjectPath
}

// Start runs the fake on a private bus. display is the user's display
// session; "" is a user without one.
func Start(t *testing.T, display dbus.ObjectPath) *Logind {
	t.Helper()
	l := &Logind{Bus: dbustest.Start(t), display: display}
	conn := l.Bus.Conn(t)
	if err := conn.Export(manager{l}, logind.ManagerPath, logind.ManagerIf); err != nil {
		t.Fatalf("logindtest: export the manager: %v", err)
	}
	if display != "" {
		for _, path := range []dbus.ObjectPath{display, autoPath} {
			if err := conn.Export(session{l, display}, path, logind.SessionIf); err != nil {
				t.Fatalf("logindtest: export %s: %v", path, err)
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

type manager struct{ l *Logind }

func (m manager) GetSession(name string) (dbus.ObjectPath, *dbus.Error) {
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
	return nil
}
