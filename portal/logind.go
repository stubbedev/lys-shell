package portal

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// logindTimeout bounds every call to logind.
const logindTimeout = 5 * time.Second

const (
	logindName      = "org.freedesktop.login1"
	logindPath      = dbus.ObjectPath("/org/freedesktop/login1")
	logindManagerIf = "org.freedesktop.login1.Manager"
	logindSessionIf = "org.freedesktop.login1.Session"
)

// logind is the backend's one system-bus link to systemd-logind,
// connected on first use and shared by Inhibit's block locks and the
// monitor sessions' screensaver state.
type logind struct {
	once sync.Once
	conn *dbus.Conn
	err  error
}

func (l *logind) bus() (*dbus.Conn, error) {
	l.once.Do(func() {
		l.conn, l.err = dbus.ConnectSystemBus()
		if l.err != nil {
			l.err = fmt.Errorf("connect to the system bus: %w", l.err)
		}
	})
	return l.conn, l.err
}

// close drops the connection, if one was made.
func (l *logind) close() {
	if l.conn != nil {
		_ = l.conn.Close()
	}
}

// lock takes a block inhibitor for what ("idle:sleep:shutdown"),
// shown with why.
func (l *logind) lock(what, why string) (*os.File, error) {
	conn, err := l.bus()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()
	var fd dbus.UnixFD
	err = conn.Object(logindName, logindPath).CallWithContext(ctx, logindManagerIf+".Inhibit", 0, what, "lys-shell portal", why, "block").Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("logind Inhibit %s: %w", what, err)
	}
	return dbusx.File(fd, "inhibit")
}

// session resolves the user's session object: the caller's, or (for a
// portal started as a user service, outside any session) the user's
// display session, which is what "auto" means to logind.
func (l *logind) session(ctx context.Context) (*dbus.Conn, dbus.ObjectPath, error) {
	conn, err := l.bus()
	if err != nil {
		return nil, "", err
	}
	var path dbus.ObjectPath
	if err := conn.Object(logindName, logindPath).CallWithContext(ctx, logindManagerIf+".GetSession", 0, "auto").Store(&path); err != nil {
		return nil, "", fmt.Errorf("logind GetSession auto: %w", err)
	}
	return conn, path, nil
}

// screensaverActive reads the session's LockedHint, which the lock
// screen sets.
func (l *logind) screensaverActive() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()
	conn, path, err := l.session(ctx)
	if err != nil {
		return false, err
	}
	var locked bool
	if err := conn.Object(logindName, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, logindSessionIf, "LockedHint").Store(&locked); err != nil {
		return false, fmt.Errorf("logind LockedHint: %w", err)
	}
	return locked, nil
}

// watchScreensaver calls fn with LockedHint whenever logind announces
// it changed, until stop.
func (l *logind) watchScreensaver(fn func(active bool)) (stop func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), logindTimeout)
	defer cancel()
	conn, path, err := l.session(ctx)
	if err != nil {
		return nil, err
	}
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return nil, fmt.Errorf("watch LockedHint: %w", err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				if sig.Path != path || len(sig.Body) < 2 || sig.Body[0] != logindSessionIf {
					continue
				}
				changed, _ := sig.Body[1].(map[string]dbus.Variant)
				if v, ok := changed["LockedHint"].Value().(bool); ok {
					fn(v)
				}
			}
		}
	}()
	return sync.OnceFunc(func() {
		conn.RemoveSignal(signals)
		close(done)
		_ = conn.RemoveMatchSignal(match...)
	}), nil
}
