// Package logind is systemd-logind on the system bus as lys-shell uses
// it: the user's session resolved without the environment, and logging
// out of it.
package logind

import (
	"context"
	"fmt"

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
