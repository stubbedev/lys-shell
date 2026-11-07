// Package shellipc is the shell's own session-bus surface: the
// dev.stubbe.lys.Shell1 control interface (wayle-shell-core's shell_ipc:
// bar visibility per monitor, the lock and VPN sign-in hooks) and the
// GApplication identity dev.stubbe.lys.shell with its org.gtk.Actions
// (quit, inspector), which `lys-shell panel` and single-instance checks use.
package shellipc

import (
	"log"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// dev.stubbe.lys.Shell1 (wayle-ipc/src/shell_ipc.rs).
const (
	ServiceName       = "dev.stubbe.lys.Shell1"
	ServicePath       = dbus.ObjectPath("/dev/stubbe/lys/Shell")
	propertiesChanged = "org.freedesktop.DBus.Properties.PropertiesChanged"
)

// State is ShellIpcState: the connectors that have a bar and the
// hidden subset. Changes reach OnHidden with the new hidden set, and
// the served Shell1 object announces them as PropertiesChanged.
type State struct {
	mu         sync.Mutex
	connectors []string
	hidden     map[string]bool
	onHidden   func(hidden map[string]bool)

	// announceMu orders the announcements, so the last one sent carries
	// the latest values; announce is nil while nothing serves the state.
	announceMu sync.Mutex
	announce   func(changed map[string]dbus.Variant)
}

// NewState builds an empty state; onHidden (may be nil) observes every
// change of the hidden set.
func NewState(onHidden func(hidden map[string]bool)) *State {
	return &State{hidden: map[string]bool{}, onHidden: onHidden}
}

// SetConnectors is monitors.rs's sync_ipc_state: the bars that exist,
// with hidden entries for vanished connectors dropped. Call it on every
// change of the set, startup's and each hotplug's.
func (s *State) SetConnectors(connectors []string) {
	s.mu.Lock()
	connectorsChanged := !slices.Equal(s.connectors, connectors)
	s.connectors = slices.Clone(connectors)
	hiddenChanged := false
	for name := range s.hidden {
		if !slices.Contains(connectors, name) {
			delete(s.hidden, name)
			hiddenChanged = true
		}
	}
	s.mu.Unlock()
	s.changed(connectorsChanged, hiddenChanged)
}

// Hidden reports whether connector's bar is hidden.
func (s *State) Hidden(connector string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hidden[connector]
}

// changed tells onHidden of a new hidden set and announces the changed
// properties.
func (s *State) changed(connectors, hidden bool) {
	if hidden && s.onHidden != nil {
		s.mu.Lock()
		snapshot := make(map[string]bool, len(s.hidden))
		for k := range s.hidden {
			snapshot[k] = true
		}
		s.mu.Unlock()
		s.onHidden(snapshot)
	}
	if !connectors && !hidden {
		return
	}
	s.announceMu.Lock()
	defer s.announceMu.Unlock()
	if s.announce == nil {
		return
	}
	props := make(map[string]dbus.Variant, 2)
	if connectors {
		props["Connectors"] = dbus.MakeVariant(s.connectorList())
	}
	if hidden {
		props["BarHidden"] = dbus.MakeVariant(s.hiddenSorted())
	}
	s.announce(props)
}

// announceTo has every later change announced through announce, nil
// for none.
func (s *State) announceTo(announce func(changed map[string]dbus.Variant)) {
	s.announceMu.Lock()
	defer s.announceMu.Unlock()
	s.announce = announce
}

// update applies fn under the lock and notifies when it reports a
// change.
func (s *State) update(fn func() bool) {
	s.mu.Lock()
	changed := fn()
	s.mu.Unlock()
	s.changed(false, changed)
}

// Hide is bar.rs's hide: "" hides every bar; an unknown connector is
// ignored.
func (s *State) Hide(monitor string) {
	s.update(func() bool {
		switch {
		case monitor == "":
			for _, c := range s.connectors {
				s.hidden[c] = true
			}
		case slices.Contains(s.connectors, monitor):
			s.hidden[monitor] = true
		default:
			return false
		}
		return true
	})
}

// Show is bar.rs's show: "" shows every bar.
func (s *State) Show(monitor string) {
	s.update(func() bool {
		switch {
		case monitor == "":
			s.hidden = map[string]bool{}
		case slices.Contains(s.connectors, monitor):
			delete(s.hidden, monitor)
		default:
			return false
		}
		return true
	})
}

// Toggle is bar.rs's toggle: "" hides all when none is hidden and
// shows all otherwise.
func (s *State) Toggle(monitor string) {
	s.update(func() bool {
		switch {
		case monitor == "":
			if len(s.hidden) == 0 {
				for _, c := range s.connectors {
					s.hidden[c] = true
				}
			} else {
				s.hidden = map[string]bool{}
			}
		case slices.Contains(s.connectors, monitor):
			if s.hidden[monitor] {
				delete(s.hidden, monitor)
			} else {
				s.hidden[monitor] = true
			}
		default:
			return false
		}
		return true
	})
}

func (s *State) hiddenSorted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.hidden))
	for name := range s.hidden {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *State) connectorList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.connectors)
}

// Hooks are the actions other parts of the shell provide. A nil hook
// answers as the Rust shell does before its component exists.
type Hooks struct {
	// Lock shows the lock screen; false means it is not ready.
	Lock func() bool
	// VPNSSOCallback hands a browser callback to a waiting sign-in;
	// false means none is waiting.
	VPNSSOCallback  func(uri string) bool
	DashboardToggle func(monitor string) error
	// BarReveal shows an auto-hiding bar ("" every bar) for a few seconds.
	BarReveal func(monitor string)
	// BarFocus hands a bar ("" the focused output's) the keyboard.
	BarFocus func(monitor string) error
}

// daemon is the dev.stubbe.lys.Shell1 object (ShellIpcDaemon).
type daemon struct {
	state *State
	hooks Hooks
}

// BarHide hides a bar ("" for all).
func (d *daemon) BarHide(monitor string) *dbus.Error {
	d.state.Hide(monitor)
	return nil
}

// BarShow shows a bar ("" for all).
func (d *daemon) BarShow(monitor string) *dbus.Error {
	d.state.Show(monitor)
	return nil
}

// BarToggle toggles a bar ("" for all).
func (d *daemon) BarToggle(monitor string) *dbus.Error {
	d.state.Toggle(monitor)
	return nil
}

// BarReveal shows an auto-hiding bar ("" every bar) for a few seconds.
func (d *daemon) BarReveal(monitor string) *dbus.Error {
	if d.hooks.BarReveal == nil {
		return dbusx.Failed("bars not ready (shell UI not initialized)")
	}
	d.hooks.BarReveal(monitor)
	return nil
}

// BarFocus hands a bar ("" the focused output's) the keyboard: its
// modules are walked with the arrows, pressed with Enter, and Esc gives
// the keyboard back.
func (d *daemon) BarFocus(monitor string) *dbus.Error {
	if d.hooks.BarFocus == nil {
		return dbusx.Failed("bars not ready (shell UI not initialized)")
	}
	if err := d.hooks.BarFocus(monitor); err != nil {
		return dbusx.Failed(err.Error())
	}
	return nil
}

// Lock asks for the lock screen.
func (d *daemon) Lock() *dbus.Error {
	if d.hooks.Lock != nil && d.hooks.Lock() {
		return nil
	}
	return dbusx.Failed("lock screen not ready (shell UI not initialized)")
}

func (d *daemon) DashboardToggle(monitor string) *dbus.Error {
	if d.hooks.DashboardToggle == nil {
		return dbusx.Failed("dashboard not ready (shell UI not initialized)")
	}
	if err := d.hooks.DashboardToggle(monitor); err != nil {
		return dbusx.Failed(err.Error())
	}
	return nil
}

// VpnSsoCallback delivers a browser sign-in callback.
func (d *daemon) VpnSsoCallback(uri string) *dbus.Error {
	if d.hooks.VPNSSOCallback != nil && d.hooks.VPNSSOCallback(uri) {
		return nil
	}
	return dbusx.Failed("no VPN browser sign-in is waiting for a callback")
}

// Serve exports dev.stubbe.lys.Shell1 over state; from then until the
// release, each change of its Connectors or BarHidden is announced with
// PropertiesChanged.
func Serve(conn *dbus.Conn, state *State, hooks Hooks) (func(), error) {
	d := &daemon{state: state, hooks: hooks}
	release, err := dbusx.Serve(conn, dbusx.Service{
		Name:      ServiceName,
		Path:      ServicePath,
		Interface: ServiceName,
		Methods:   d,
		Properties: dbusx.Getters{
			"BarHidden":  func() any { return state.hiddenSorted() },
			"Connectors": func() any { return state.connectorList() },
		},
	})
	if err != nil {
		return nil, err
	}
	state.announceTo(func(changed map[string]dbus.Variant) {
		if err := conn.Emit(ServicePath, propertiesChanged, ServiceName, changed, []string{}); err != nil {
			log.Printf("shell ipc: announce %v: %v", slices.Sorted(maps.Keys(changed)), err)
		}
	})
	return func() {
		state.announceTo(nil)
		release()
	}, nil
}
