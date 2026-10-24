package portal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

// InhibitIface is org.freedesktop.impl.portal.Inhibit.
const InhibitIface = "org.freedesktop.impl.portal.Inhibit"

// Inhibit flags of the portal spec.
const (
	inhibitLogout     = 1
	inhibitUserSwitch = 2
	inhibitSuspend    = 4
	inhibitIdle       = 8
)

// sessionRunning is the session-state a monitor reports: lys-shell has
// no session manager that asks apps before ending the session, so the
// state never moves to Query End (2) or Ending (3).
const sessionRunning = 1

// monitorStateDelay is how long after CreateMonitor's reply the first
// StateChanged goes out: the frontend registers the session when the
// reply arrives and drops signals for sessions it does not know.
const monitorStateDelay = 100 * time.Millisecond

// shellCallTimeout bounds a call to the running shell.
const shellCallTimeout = 5 * time.Second

// screensaver is where a monitor's screensaver-active comes from:
// logind's LockedHint, which the lock screen sets.
type screensaver interface {
	screensaverActive() (bool, error)
	watchScreensaver(fn func(active bool)) (stop func(), err error)
}

// inhibitor is org.freedesktop.impl.portal.Inhibit. Inhibit holds,
// for a Request's lifetime, a logind block lock (idle, sleep, and
// shutdown for logout, since powering off ends the session too) and,
// for idle, a hold on the shell's own idle inhibitor, which idle
// daemons that ignore logind (swayidle) still obey. User switching has
// nothing to inhibit it on this desktop. CreateMonitor sessions get
// StateChanged when the screen locks or unlocks.
type inhibitor struct {
	conn     *dbus.Conn
	sessions *sessions
	// lock takes a logind lock for what ("idle:sleep:shutdown") with a
	// reason.
	lock func(what, why string) (*os.File, error)
	// holdIdle holds the shell's idle inhibitor for an app.
	holdIdle    func(app, reason string) (release func(), err error)
	screensaver screensaver

	mu      sync.Mutex
	unwatch func()
}

func newInhibitor(conn *dbus.Conn, sessions *sessions, lg *logind) *inhibitor {
	return &inhibitor{conn: conn, sessions: sessions, lock: lg.lock, holdIdle: shellIdleHold(conn), screensaver: lg}
}

func (i *inhibitor) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       InhibitIface,
		Methods:    i,
		Properties: dbusx.Getters{"version": func() any { return uint32(3) }},
	}
}

// shellIdleHold holds idle through the shell's IdleInhibit1 service,
// which releases the hold by itself should the portal leave the bus.
func shellIdleHold(conn *dbus.Conn) func(app, reason string) (func(), error) {
	return func(app, reason string) (func(), error) {
		obj := conn.Object(idleinhibit.ServiceName, idleinhibit.ServicePath)
		ctx, cancel := context.WithTimeout(context.Background(), shellCallTimeout)
		defer cancel()
		var id uint32
		if err := obj.CallWithContext(ctx, idleinhibit.ServiceName+".Hold", 0, app, reason).Store(&id); err != nil {
			return nil, fmt.Errorf("shell idle hold: %w", err)
		}
		return func() {
			ctx, cancel := context.WithTimeout(context.Background(), shellCallTimeout)
			defer cancel()
			if err := obj.CallWithContext(ctx, idleinhibit.ServiceName+".Release", 0, id).Err; err != nil {
				warnf("inhibit: releasing the shell idle hold: %v", err)
			}
		}, nil
	}
}

// Inhibit takes what it can of the flags and exports a Request at the
// handle whose Close releases it all. The Request is exported even when
// nothing could be taken, so the frontend's Close never meets an
// unknown object; the call then fails, which the app sees as response
// 2.
func (i *inhibitor) Inhibit(handle dbus.ObjectPath, appID, _ string, flags uint32, options Vardict) *dbus.Error {
	why := stringOr(options, "reason", "")
	if why == "" {
		why = "Application requested"
	}
	l := &inhibitLock{}
	var failures []error
	if what := inhibitWhat(flags); what != "" {
		fd, err := i.lock(what, why)
		if err != nil {
			failures = append(failures, err)
		}
		l.fd = fd
	}
	if flags&inhibitIdle != 0 {
		release, err := i.holdIdle(appID, why)
		if err != nil {
			failures = append(failures, err)
		}
		l.idle = release
	}
	if flags&inhibitUserSwitch != 0 {
		failures = append(failures, errors.New("user switching cannot be inhibited on this desktop"))
	}
	unexport, err := dbusx.Export(i.conn, handle, dbusx.Interface{Name: RequestIface, Methods: l})
	if err != nil {
		l.release()
		return dbusx.Failed(fmt.Sprintf("export the request: %v", err))
	}
	l.unexport = unexport
	for _, f := range failures {
		warnf("inhibit %s: %v", appID, f)
	}
	if !l.holding() {
		return dbusx.Failed(fmt.Sprintf("nothing could be inhibited: %v", errors.Join(failures...)))
	}
	return nil
}

// inhibitLock is the Request holding one inhibition.
type inhibitLock struct {
	once     sync.Once
	fd       *os.File
	idle     func()
	unexport func()
}

func (l *inhibitLock) holding() bool { return l.fd != nil || l.idle != nil }

func (l *inhibitLock) release() {
	if l.fd != nil {
		_ = l.fd.Close()
	}
	if l.idle != nil {
		l.idle()
	}
}

// Close releases the inhibition.
func (l *inhibitLock) Close() *dbus.Error {
	l.once.Do(func() {
		l.unexport()
		l.release()
	})
	return nil
}

// inhibitWhat is logind's what for the flags.
func inhibitWhat(flags uint32) string {
	var what []string
	if flags&inhibitIdle != 0 {
		what = append(what, "idle")
	}
	if flags&inhibitSuspend != 0 {
		what = append(what, "sleep")
	}
	if flags&inhibitLogout != 0 {
		what = append(what, "shutdown")
	}
	return strings.Join(what, ":")
}

// CreateMonitor opens a monitoring session: it gets the current state
// right after the reply, and again whenever the screen locks or
// unlocks, until it closes.
func (i *inhibitor) CreateMonitor(_, session dbus.ObjectPath, appID, _ string) (uint32, *dbus.Error) {
	s, err := i.sessions.create(session, kindInhibitMonitor, appID)
	if err != nil {
		warnf("inhibit: cannot mount monitor session for %s: %v", appID, err)
		return ResponseOther, nil
	}
	s.onClose(i.dropMonitor)
	i.mu.Lock()
	if i.unwatch == nil {
		unwatch, err := i.screensaver.watchScreensaver(i.broadcast)
		if err != nil {
			warnf("inhibit: screensaver state will not follow the lock screen: %v", err)
		} else {
			i.unwatch = unwatch
		}
	}
	i.mu.Unlock()
	time.AfterFunc(monitorStateDelay, func() {
		active, err := i.screensaver.screensaverActive()
		if err != nil {
			warnf("inhibit: reading the screensaver state: %v", err)
		}
		i.emitState(session, active)
	})
	return ResponseSuccess, nil
}

// QueryEndResponse acknowledges a Query End this backend never sends.
func (i *inhibitor) QueryEndResponse(dbus.ObjectPath) *dbus.Error { return nil }

// monitors is every live monitor session.
func (i *inhibitor) monitors() []*session {
	return i.sessions.each(func(s *session) bool { return s.kind == kindInhibitMonitor })
}

// dropMonitor stops the screensaver watch once no monitor is left.
func (i *inhibitor) dropMonitor() {
	if len(i.monitors()) > 0 {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.unwatch != nil {
		i.unwatch()
		i.unwatch = nil
	}
}

// broadcast sends every live monitor the new screensaver state.
func (i *inhibitor) broadcast(active bool) {
	for _, s := range i.monitors() {
		i.emitState(s.path, active)
	}
}

func (i *inhibitor) emitState(session dbus.ObjectPath, active bool) {
	if s, ok := i.sessions.get(session); !ok || s.kind != kindInhibitMonitor {
		return
	}
	state := Vardict{"screensaver-active": dbus.MakeVariant(active), "session-state": dbus.MakeVariant(uint32(sessionRunning))}
	_ = i.conn.Emit(ObjectPath, InhibitIface+".StateChanged", session, state)
}
