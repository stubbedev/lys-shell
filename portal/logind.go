package portal

import (
	"context"
	"os"
	"sync"

	"github.com/stubbedev/lys-desktop/identity"
	login1 "github.com/stubbedev/lys-lib/logind"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// logind is the backend's one system-bus link to systemd-logind,
// connected on first use and shared by Inhibit's block locks and the
// monitor sessions' screensaver state.
type logind struct {
	once sync.Once
	conn *dbus.Conn
	err  error
}

func (l *logind) manager() (login1.Manager, error) {
	l.once.Do(func() {
		l.conn, l.err = login1.Dial(context.Background())
	})
	if l.err != nil {
		return login1.Manager{}, l.err
	}
	return login1.NewManager(l.conn), nil
}

// close drops the connection, if one was made.
func (l *logind) close() {
	if l.conn != nil {
		_ = l.conn.Close()
	}
}

// lock takes a block inhibitor on what, shown with why.
func (l *logind) lock(what login1.InhibitWhat, why string) (*os.File, error) {
	m, err := l.manager()
	if err != nil {
		return nil, err
	}
	return m.Inhibit(context.Background(), what, identity.Name+" portal", why, login1.InhibitBlock)
}

// session resolves the user's session.
func (l *logind) session(ctx context.Context) (login1.Session, error) {
	m, err := l.manager()
	if err != nil {
		return login1.Session{}, err
	}
	return m.GetSession(ctx, login1.AutoSession)
}

// screensaverActive reads the session's LockedHint, which the lock
// screen sets.
func (l *logind) screensaverActive() (bool, error) {
	ctx := context.Background()
	s, err := l.session(ctx)
	if err != nil {
		return false, err
	}
	return s.LockedHint(ctx)
}

// watchScreensaver calls fn with LockedHint whenever logind announces
// it changed, until stop.
func (l *logind) watchScreensaver(fn func(active bool)) (stop func(), err error) {
	ctx := context.Background()
	s, err := l.session(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.Watch(ctx, func(e login1.Event, err error) {
		if err != nil {
			warnf("screensaver: %v", err)
			return
		}
		if c, ok := e.(login1.LockedHintChanged); ok {
			fn(c.Locked)
		}
	})
	if err != nil {
		return nil, err
	}
	return func() {
		if err := w.Stop(); err != nil {
			warnf("screensaver: %v", err)
		}
	}, nil
}
