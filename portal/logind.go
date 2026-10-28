package portal

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/version"
	login1 "github.com/stubbedev/lys-shell/service/logind"
)

// logindTimeout bounds every call to logind.
const logindTimeout = 5 * time.Second

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
	err = conn.Object(login1.Service, login1.ManagerPath).CallWithContext(ctx, login1.ManagerIf+".Inhibit", 0, what, version.Name+" portal", why, "block").Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("logind Inhibit %s: %w", what, err)
	}
	return dbusx.File(fd, "inhibit")
}

// session resolves the user's session object (login1.Session).
func (l *logind) session(ctx context.Context) (*dbus.Conn, dbus.ObjectPath, error) {
	conn, err := l.bus()
	if err != nil {
		return nil, "", err
	}
	path, err := login1.Session(ctx, conn)
	if err != nil {
		return nil, "", err
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
	if err := conn.Object(login1.Service, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, login1.SessionIf, "LockedHint").Store(&locked); err != nil {
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
				if sig.Path != path || len(sig.Body) < 2 || sig.Body[0] != login1.SessionIf {
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
