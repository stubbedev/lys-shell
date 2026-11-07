// Package logind is systemd-logind on the system bus as lys-shell uses
// it: the user's session resolved without the environment, logging out
// of it, locking it, suspending, and holding sleep off until it is locked.
package logind

import (
	"context"
	"fmt"
	"os"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// The logind service, its manager object, and its interfaces.
const (
	Service     = "org.freedesktop.login1"
	ManagerPath = dbus.ObjectPath("/org/freedesktop/login1")
	ManagerIf   = "org.freedesktop.login1.Manager"
	SessionIf   = "org.freedesktop.login1.Session"
)

// autoSession is logind's alias for the caller's own session or, for a
// caller outside any session (a service of the systemd user manager),
// the user's display session.
const autoSession = "auto"

// Session resolves the user's session object path through
// Manager.GetSession("auto"). logind emits signals from the real path,
// not from the /session/auto alias, so callers keep the resolved one.
func Session(ctx context.Context, conn *dbus.Conn) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	err := conn.Object(Service, ManagerPath).CallWithContext(ctx, ManagerIf+".GetSession", 0, autoSession).Store(&path)
	if err != nil {
		return "", fmt.Errorf("logind GetSession %q: %w", autoSession, err)
	}
	return path, nil
}

// Logout terminates the user's session: the one Session resolves.
func Logout(ctx context.Context, conn *dbus.Conn) error {
	path, err := Session(ctx, conn)
	if err != nil {
		return fmt.Errorf("log out: %w", err)
	}
	if err := conn.Object(Service, path).CallWithContext(ctx, SessionIf+".Terminate", 0).Err; err != nil {
		return fmt.Errorf("log out: logind Terminate %s: %w", path, err)
	}
	return nil
}

// Sleep is PrepareForSleep's argument: true as the system is about to
// sleep, false once it has woken.
type Sleep struct{ Start bool }

// PrepareForSleep is the manager's signal around a suspend or hibernate.
var PrepareForSleep = dbusx.NewSignal[Sleep](ManagerIf, "PrepareForSleep")

// PrepareForSleepMatch matches PrepareForSleep from the manager.
func PrepareForSleepMatch() []dbus.MatchOption {
	return []dbus.MatchOption{dbus.WithMatchObjectPath(ManagerPath), dbus.WithMatchInterface(ManagerIf), dbus.WithMatchMember("PrepareForSleep")}
}

// Suspend asks logind to suspend the system, not interactively: a polkit
// prompt is never shown for it.
func Suspend(ctx context.Context, conn *dbus.Conn) error {
	if err := conn.Object(Service, ManagerPath).CallWithContext(ctx, ManagerIf+".Suspend", 0, false).Err; err != nil {
		return fmt.Errorf("logind Suspend: %w", err)
	}
	return nil
}

// InhibitSleep takes a delay inhibitor on sleep for who, saying why: logind
// waits (up to its InhibitDelayMaxSec) before sleeping until the returned
// file is closed.
func InhibitSleep(ctx context.Context, conn *dbus.Conn, who, why string) (*os.File, error) {
	var fd dbus.UnixFD
	err := conn.Object(Service, ManagerPath).CallWithContext(ctx, ManagerIf+".Inhibit", 0, "sleep", who, why, "delay").Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("logind Inhibit sleep delay for %s: %w", who, err)
	}
	f, err := dbusx.File(fd, "logind sleep inhibitor")
	if err != nil {
		return nil, fmt.Errorf("logind Inhibit sleep delay for %s: %w", who, err)
	}
	return f, nil
}
