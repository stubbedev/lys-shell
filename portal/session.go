package portal

import (
	"fmt"
	"slices"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// SessionIface is the object ScreenCast, RemoteDesktop, GlobalShortcuts
// and Inhibit monitors put at a session handle in CreateSession; it
// lives until the app or the backend closes it.
const SessionIface = "org.freedesktop.impl.portal.Session"

// sessionKind is the interface that created a session.
type sessionKind int

// Session kinds.
const (
	kindScreenCast sessionKind = iota + 1
	kindRemoteDesktop
	kindGlobalShortcuts
	kindInhibitMonitor
)

func (k sessionKind) String() string {
	switch k {
	case kindScreenCast:
		return "ScreenCast"
	case kindRemoteDesktop:
		return "RemoteDesktop"
	case kindGlobalShortcuts:
		return "GlobalShortcuts"
	case kindInhibitMonitor:
		return "Inhibit"
	}
	return "unknown"
}

// session is one live Session: who created it, and the state each
// interface keeps on it, guarded by mu. Its cleanups run exactly once,
// whoever closes it.
type session struct {
	path  dbus.ObjectPath
	kind  sessionKind
	appID string

	mu sync.Mutex
	// cast is a ScreenCast session's (or a RemoteDesktop session's
	// screen sharing) sources and streams.
	cast *castState
	// remote is a RemoteDesktop session's devices.
	remote *remoteState
	// shortcuts is a GlobalShortcuts session's binds.
	shortcuts *shortcutState
	// clipboard is set once a RemoteDesktop session enabled the
	// clipboard.
	clipboard bool

	once     sync.Once
	cleanups []func()
	unexport func()
}

// onClose registers fn to run when the session closes, after the
// cleanups registered before it. On a session already closed it runs
// at once.
func (s *session) onClose(fn func()) {
	s.mu.Lock()
	closed := s.cleanups == nil
	if !closed {
		s.cleanups = append(s.cleanups, fn)
	}
	s.mu.Unlock()
	if closed {
		fn()
	}
}

// sessions is the backend's registry of live sessions by path: one
// record per session and one close path, so shutdown, the app's Close
// and a backend-initiated close all run the same cleanups.
type sessions struct {
	conn *dbus.Conn
	mu   sync.Mutex
	live map[dbus.ObjectPath]*session
}

func newSessions(conn *dbus.Conn) *sessions {
	return &sessions{conn: conn, live: map[dbus.ObjectPath]*session{}}
}

// create exports a Session at path for kind and appID; a path that
// already holds a live session is refused, so none is ever orphaned.
func (r *sessions) create(path dbus.ObjectPath, kind sessionKind, appID string) (*session, error) {
	if _, live := r.get(path); live {
		return nil, fmt.Errorf("a session is already live at %s", path)
	}
	s := &session{path: path, kind: kind, appID: appID, cleanups: []func(){}}
	unexport, err := dbusx.Export(r.conn, path, dbusx.Interface{
		Name:       SessionIface,
		Methods:    sessionObject{r: r, s: s},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	})
	if err != nil {
		return nil, err
	}
	s.unexport = unexport
	r.mu.Lock()
	r.live[path] = s
	r.mu.Unlock()
	return s, nil
}

// lookup finds the live session at path created by one of kinds for
// appID; anything else is an error, so a session cannot be driven
// through an interface, or by an app, it does not belong to.
func (r *sessions) lookup(path dbus.ObjectPath, appID string, kinds ...sessionKind) (*session, error) {
	r.mu.Lock()
	s, ok := r.live[path]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no session at %s", path)
	}
	if s.appID != appID {
		return nil, fmt.Errorf("session %s belongs to %q, not %q", path, s.appID, appID)
	}
	if !slices.Contains(kinds, s.kind) {
		return nil, fmt.Errorf("session %s is a %s session", path, s.kind)
	}
	return s, nil
}

// get finds the live session at path, of any kind or app (for methods
// the frontend calls without an app id).
func (r *sessions) get(path dbus.ObjectPath) (*session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.live[path]
	return s, ok
}

// each lists the live sessions keep selects.
func (r *sessions) each(keep func(*session) bool) []*session {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*session
	for _, s := range r.live {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// close ends a session on the backend's side, when it cannot go on (a
// stream's source vanished, the compositor connection ended, shutdown):
// the session ends and Closed tells the frontend.
func (r *sessions) close(s *session) { r.end(s, true) }

// end runs a session's cleanups, forgets it and removes the object,
// once; announce emits Closed, which the interface reserves for closes
// the implementation made.
func (r *sessions) end(s *session, announce bool) {
	s.once.Do(func() {
		r.mu.Lock()
		delete(r.live, s.path)
		r.mu.Unlock()
		s.mu.Lock()
		cleanups := s.cleanups
		s.cleanups = nil
		s.mu.Unlock()
		for _, fn := range cleanups {
			fn()
		}
		if announce {
			_ = r.conn.Emit(s.path, SessionIface+".Closed")
		}
		s.unexport()
	})
}

// clearAll closes every live session (process shutdown).
func (r *sessions) clearAll() {
	for _, s := range r.each(func(*session) bool { return true }) {
		r.close(s)
	}
}

// sessionObject carries the Session's D-Bus methods.
type sessionObject struct {
	r *sessions
	s *session
}

// Close ends the session at the app's request.
func (o sessionObject) Close() *dbus.Error {
	o.r.end(o.s, false)
	return nil
}
