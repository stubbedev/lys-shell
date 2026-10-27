package logind

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

// The session's Lock and Unlock signals drive the lock screen, so
// `loginctl lock-session`, idle daemons, and `lys-shell lock` all end up
// there; SetLockedHint reports the state back, and LockedHint - kept by
// logind across restarts - says whether a dead lock client held the lock.

// logindTimeout bounds resolving the session.
const logindTimeout = 5 * time.Second

// Client is the caller's logind session on the system bus.
type Client struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

// NewClient resolves the user's session object once (Session); every
// call and signal match uses its real path.
func NewClient(conn *dbus.Conn) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()
	path, err := Session(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("logind: resolve session: %w", err)
	}
	return &Client{conn: conn, path: path}, nil
}

// Path is the resolved session object path.
func (l *Client) Path() dbus.ObjectPath { return l.path }

// SetLockedHint tells logind whether the session is locked.
func (l *Client) SetLockedHint(locked bool) error {
	return l.conn.Object(Service, l.path).Call(SessionIf+".SetLockedHint", 0, locked).Err
}

// LockedHint reads the session's LockedHint property.
func (l *Client) LockedHint() (bool, error) {
	v, err := l.conn.Object(Service, l.path).GetProperty(SessionIf + ".LockedHint")
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
func (l *Client) Listen(ctx context.Context, onLock, onUnlock func()) error {
	opts := []dbus.MatchOption{
		dbus.WithMatchObjectPath(l.path),
		dbus.WithMatchInterface(SessionIf),
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
			case SessionIf + ".Lock":
				onLock()
			case SessionIf + ".Unlock":
				onUnlock()
			}
		}
	}
}

// SystemClient connects to logind on the system bus, logging (not
// failing) when it is unavailable, as what without it is optional; what
// names the user of the client in the log line.
func SystemClient(what string) *Client {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Printf("%s: logind unavailable: %v", what, err)
		return nil
	}
	l, err := NewClient(conn)
	if err != nil {
		log.Printf("%s: logind unavailable: %v", what, err)
		_ = conn.Close()
		return nil
	}
	return l
}
