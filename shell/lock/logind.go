package lock

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/service/logind"
)

// logind integration (shell/lock/logind.rs): the session's Lock and
// Unlock signals drive the lock screen, so `loginctl lock-session`, idle
// daemons, and `lys-shell lock` all end up here; SetLockedHint reports the
// state back, and LockedHint - kept by logind across our restarts - says
// whether a dead predecessor held the lock.

// logindTimeout bounds resolving the session.
const logindTimeout = 5 * time.Second

// Logind is the caller's logind session on the system bus.
type Logind struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

// NewLogind resolves the user's session object once (logind.Session);
// every call and signal match uses its real path.
func NewLogind(conn *dbus.Conn) (*Logind, error) {
	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()
	path, err := logind.Session(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("logind: resolve session: %w", err)
	}
	return &Logind{conn: conn, path: path}, nil
}

// Path is the resolved session object path.
func (l *Logind) Path() dbus.ObjectPath { return l.path }

// SetLockedHint tells logind whether the session is locked.
func (l *Logind) SetLockedHint(locked bool) error {
	return l.conn.Object(logind.Service, l.path).Call(logind.SessionIf+".SetLockedHint", 0, locked).Err
}

// LockedHint reads the session's LockedHint property.
func (l *Logind) LockedHint() (bool, error) {
	v, err := l.conn.Object(logind.Service, l.path).GetProperty(logind.SessionIf + ".LockedHint")
	if err != nil {
		return false, err
	}
	locked, ok := v.Value().(bool)
	if !ok {
		return false, fmt.Errorf("logind: LockedHint is %s, want a bool", v.Signature())
	}
	return locked, nil
}

// Listen delivers the session's Lock and Unlock signals until ctx ends
// or the bus drops. The callbacks run on the listener goroutine.
func (l *Logind) Listen(ctx context.Context, onLock, onUnlock func()) error {
	opts := []dbus.MatchOption{
		dbus.WithMatchObjectPath(l.path),
		dbus.WithMatchInterface(logind.SessionIf),
	}
	if err := l.conn.AddMatchSignal(opts...); err != nil {
		return fmt.Errorf("logind: subscribe: %w", err)
	}
	defer func() { _ = l.conn.RemoveMatchSignal(opts...) }()
	signals := make(chan *dbus.Signal, 8)
	l.conn.Signal(signals)
	defer l.conn.RemoveSignal(signals)
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-signals:
			if !ok {
				return nil
			}
			if sig.Path != l.path {
				continue
			}
			switch sig.Name {
			case logind.SessionIf + ".Lock":
				onLock()
			case logind.SessionIf + ".Unlock":
				onUnlock()
			}
		}
	}
}

// shouldRelock decides from the LockedHint probe whether a fresh shell
// re-acquires the lock: only a confirmed true relocks. False means the
// previous shell left the session unlocked, and a failed probe fails
// soft - an unknown state must not lock the user out.
func shouldRelock(locked bool, err error) bool {
	return err == nil && locked
}

// systemLogind connects to logind on the system bus, logging (not
// failing) when it is unavailable: the lock screen still works through
// the shell IPC without it.
func systemLogind() *Logind {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Printf("lock: logind unavailable; triggers disabled: %v", err)
		return nil
	}
	l, err := NewLogind(conn)
	if err != nil {
		log.Printf("lock: logind unavailable; triggers disabled: %v", err)
		_ = conn.Close()
		return nil
	}
	return l
}
