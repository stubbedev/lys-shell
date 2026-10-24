package portal

import (
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/toplevels"
)

// BackgroundIface is org.freedesktop.impl.portal.Background.
const BackgroundIface = "org.freedesktop.impl.portal.Background"

// GetAppState's states for an app with a window; one with none is
// background (0), which the frontend assumes for an app not listed.
const (
	appRunning uint32 = 1
	appActive  uint32 = 2
)

// NotifyBackground's results.
const (
	backgroundForbid    uint32 = 0
	backgroundAllow     uint32 = 1
	backgroundAllowOnce uint32 = 2
)

// The actions the background notification offers, [key, label, ...],
// and the result each key stands for.
var (
	backgroundActions = []string{"forbid", "Forbid", "allow", "Allow", "allow-once", "Allow once"}
	backgroundResults = map[string]uint32{"forbid": backgroundForbid, "allow": backgroundAllow, "allow-once": backgroundAllowOnce}
)

// background is org.freedesktop.impl.portal.Background (background.rs):
// the apps' states come from the compositor's windows, and an app the
// frontend finds running with none is the user's call, asked in a
// notification.
type background struct {
	conn     *dbus.Conn
	notifier *notifier
	windows  windowSource
}

// windowSource is the compositor's open windows, followed until close.
type windowSource interface {
	windows() ([]toplevels.Window, error)
	close()
}

func (b background) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       BackgroundIface,
		Methods:    b,
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

// GetAppState reports every app with a window: running, or active
// when one of its windows is the activated one. No window list is an error, never an empty one: the frontend would take
// every app for a background one.
func (b background) GetAppState() (Vardict, *dbus.Error) {
	windows, err := b.windows.windows()
	if err != nil {
		return nil, dbusx.Failed("background: no window list: " + err.Error())
	}
	states := Vardict{}
	for id, state := range appStates(windows) {
		states[id] = dbus.MakeVariant(state)
	}
	return states, nil
}

// appStates folds the windows into each app id's state; windows with
// no app id name no app.
func appStates(windows []toplevels.Window) map[string]uint32 {
	states := map[string]uint32{}
	for _, w := range windows {
		if w.AppID == "" {
			continue
		}
		state := appRunning
		if w.Activated {
			state = appActive
		}
		states[w.AppID] = max(states[w.AppID], state)
	}
	return states
}

// NotifyBackground asks the user, in a notification, whether the app
// may keep running with no window, and replies their answer once they
// give it: Forbid, Allow, or Allow once. A notification dismissed
// unanswered allows this instance only, so nothing permanent is decided
// without the user. The frontend closing the request withdraws it.
func (b background) NotifyBackground(handle dbus.ObjectPath, appID, name string) (uint32, Vardict, *dbus.Error) {
	c, ok := beginOr(b.conn, handle, appID, "", nil)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	if name == "" {
		name = appID
	}
	key, err := b.notifier.ask(c.ctx, appID, name+" is running in the background",
		"It has no open window. Allow it to keep running?", backgroundActions)
	if c.closed() {
		return ResponseCancelled, Vardict{}, nil
	}
	if err != nil {
		warnf("background: asking about %s: %v", appID, err)
		return ResponseOther, Vardict{}, nil
	}
	result, answered := backgroundResults[key]
	if !answered {
		result = backgroundAllowOnce
	}
	return ResponseSuccess, Vardict{"result": dbus.MakeVariant(result)}, nil
}

// windowList follows the compositor's windows from the first ask on,
// announcing changes as RunningApplicationsChanged; a compositor that
// went away is reconnected on the next ask.
type windowList struct {
	conn  *dbus.Conn
	watch func(changed func()) (*toplevels.Watcher, error)
	mu    sync.Mutex
	w     *toplevels.Watcher
}

func (l *windowList) windows() ([]toplevels.Window, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		select {
		case <-l.w.Ended():
			warnf("background: the window list ended: %v", l.w.Err())
			l.w = nil
		default:
		}
	}
	if l.w == nil {
		w, err := l.watch(l.changed)
		if err != nil {
			return nil, err
		}
		l.w = w
	}
	return l.w.Windows(), nil
}

func (l *windowList) changed() {
	if err := l.conn.Emit(ObjectPath, BackgroundIface+".RunningApplicationsChanged"); err != nil {
		warnf("background: RunningApplicationsChanged: %v", err)
	}
}

// close stops following the windows.
func (l *windowList) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		l.w.Close()
		l.w = nil
	}
}
