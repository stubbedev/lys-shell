// Package logindtest is a fake systemd-logind on a private bus, seen
// from a caller outside any login session: a shell the systemd user
// manager started.
package logindtest

import (
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/service/logind"
)

// errNoSuchSession is logind's reply when a name resolves to no session.
const errNoSuchSession = "org.freedesktop.login1.NoSuchSession"

// Logind is the fake. Its manager resolves "auto" to the user's display
// session, and "self" to nothing, since the caller is in no session.
type Logind struct {
	// Bus is the private bus logind owns its name on.
	Bus *dbustest.Bus
	// Terminated lists the session paths Terminate was called on.
	Terminated dbustest.Var[[]dbus.ObjectPath]
	display    dbus.ObjectPath
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
		if err := conn.Export(session{l, display}, display, logind.SessionIf); err != nil {
			t.Fatalf("logindtest: export %s: %v", display, err)
		}
	}
	reply, err := conn.RequestName(logind.Service, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("logindtest: claim %s: %v %v", logind.Service, reply, err)
	}
	return l
}

type manager struct{ l *Logind }

func (m manager) GetSession(name string) (dbus.ObjectPath, *dbus.Error) {
	if name != "auto" || m.l.display == "" {
		return "", dbus.NewError(errNoSuchSession, []any{"no session " + name})
	}
	return m.l.display, nil
}

type session struct {
	l    *Logind
	path dbus.ObjectPath
}

func (s session) Terminate() *dbus.Error {
	s.l.Terminated.Update(func(paths []dbus.ObjectPath) []dbus.ObjectPath { return append(paths, s.path) })
	return nil
}
