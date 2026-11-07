package logind

import (
	"context"
	"fmt"
	"log"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// CallTimeout is how long one call to logind may take: callers on a path
// that must not hang give their calls this deadline.
const CallTimeout = 5 * time.Second

// Client is the caller's logind session on the system bus.
type Client struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

// NewClient resolves the user's session object once (Session) under ctx;
// every call and signal match uses its real path.
func NewClient(ctx context.Context, conn *dbus.Conn) (*Client, error) {
	path, err := Session(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("logind: resolve session: %w", err)
	}
	return &Client{conn: conn, path: path}, nil
}

// Path is the resolved session object path.
func (l *Client) Path() dbus.ObjectPath { return l.path }

// SetLockedHint tells logind whether the session is locked, waiting no
// longer than ctx.
func (l *Client) SetLockedHint(ctx context.Context, locked bool) error {
	if err := l.conn.Object(Service, l.path).CallWithContext(ctx, SessionIf+".SetLockedHint", 0, locked).Err; err != nil {
		return fmt.Errorf("logind: SetLockedHint(%t) on %s: %w", locked, l.path, err)
	}
	return nil
}

// LockedHint reads the session's LockedHint property, waiting no longer
// than ctx.
func (l *Client) LockedHint(ctx context.Context) (bool, error) {
	var locked bool
	err := l.conn.Object(Service, l.path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, SessionIf, "LockedHint").Store(&locked)
	if err != nil {
		return false, fmt.Errorf("logind: read LockedHint of %s: %w", l.path, err)
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
	subscribing, subscribed := context.WithTimeout(ctx, CallTimeout)
	err := l.conn.AddMatchSignalContext(subscribing, opts...)
	subscribed()
	if err != nil {
		return fmt.Errorf("logind: subscribe: %w", err)
	}
	defer func() {
		removing, removed := context.WithTimeout(context.Background(), CallTimeout)
		defer removed()
		_ = l.conn.RemoveMatchSignalContext(removing, opts...)
	}()
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

// SystemClient connects to logind on the system bus under ctx, logging
// (not failing) when it is unavailable, as what without it is optional;
// what names the user of the client in the log line. The connection
// outlives ctx once made.
func SystemClient(ctx context.Context, what string) *Client {
	life, closeConn := context.WithCancel(context.Background())
	giveUp := context.AfterFunc(ctx, closeConn)
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(life))
	if err != nil {
		giveUp()
		closeConn()
		log.Printf("%s: logind unavailable: %v", what, err)
		return nil
	}
	l, err := NewClient(ctx, conn)
	if !giveUp() && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		log.Printf("%s: logind unavailable: %v", what, err)
		closeConn()
		return nil
	}
	return l
}

// Lock asks logind to lock the session: logind sends the session's Lock
// signal, which the lock screen answers.
func (l *Client) Lock(ctx context.Context) error {
	if err := l.conn.Object(Service, l.path).CallWithContext(ctx, SessionIf+".Lock", 0).Err; err != nil {
		return fmt.Errorf("logind: Lock %s: %w", l.path, err)
	}
	return nil
}

// WaitLocked returns once the session's LockedHint is true, or with ctx's
// error when it ends first.
func (l *Client) WaitLocked(ctx context.Context) error {
	opts := []dbus.MatchOption{
		dbus.WithMatchObjectPath(l.path),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, SessionIf),
	}
	if err := l.conn.AddMatchSignalContext(ctx, opts...); err != nil {
		return fmt.Errorf("logind: watch LockedHint of %s: %w", l.path, err)
	}
	defer func() {
		removing, removed := context.WithTimeout(context.Background(), CallTimeout)
		defer removed()
		_ = l.conn.RemoveMatchSignalContext(removing, opts...)
	}()
	signals := make(chan *dbus.Signal, 8)
	l.conn.Signal(signals)
	defer l.conn.RemoveSignal(signals)
	locked, err := l.LockedHint(ctx)
	if err != nil {
		return err
	}
	for !locked {
		select {
		case <-ctx.Done():
			return fmt.Errorf("logind: wait for LockedHint of %s: %w", l.path, ctx.Err())
		case sig, ok := <-signals:
			if !ok {
				return fmt.Errorf("logind: wait for LockedHint of %s: the bus connection closed", l.path)
			}
			locked = lockedHintChanged(sig, l.path)
		}
	}
	return nil
}

func lockedHintChanged(sig *dbus.Signal, path dbus.ObjectPath) bool {
	if sig.Path != path || sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" || len(sig.Body) < 2 {
		return false
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return false
	}
	v, ok := changed["LockedHint"]
	if !ok {
		return false
	}
	locked, ok := v.Value().(bool)
	return ok && locked
}
