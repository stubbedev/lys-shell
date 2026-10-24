package portal

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// GlobalShortcutsIface bridges org.freedesktop.impl.portal.GlobalShortcuts
// to the compositor's hyprland-global-shortcuts-v1, as
// xdg-desktop-portal-hyprland does (globalshortcuts/mod.rs): bound
// shortcuts are registered there and their presses come back as
// Activated / Deactivated. Compositors without the protocol (niri,
// mango) accept binds and never activate them.
const GlobalShortcutsIface = "org.freedesktop.impl.portal.GlobalShortcuts"

// shortcutDevice is the compositor side (waylandShortcuts; a fake in
// tests).
type shortcutDevice interface {
	// register binds id for appID under key, the route its events carry;
	// the returned func unregisters it.
	register(key, id, appID, description, trigger string) (unregister func(), err error)
}

// shortcut is one (sa{sv}): the id and the properties echoed back.
type shortcut struct {
	ID    string
	Props Vardict
}

// shortcutRoute is where a registration's events go.
type shortcutRoute struct {
	session dbus.ObjectPath
	id      string
}

// shortcutState is a GlobalShortcuts session's bound shortcuts; it
// lives on the session, guarded by its mu.
type shortcutState struct {
	shortcuts []shortcut
}

// globalShortcuts is the interface: the compositor registrations and
// their routes; each session's binds live on its record.
type globalShortcuts struct {
	conn     *dbus.Conn
	sessions *sessions
	// start brings the device up on first use; onEvent gets every
	// press and release.
	start func(onEvent func(key string, pressed bool, sec uint64)) (shortcutDevice, error)

	mu      sync.Mutex
	dev     shortcutDevice
	routes  map[string]shortcutRoute
	objects map[string]func()
}

func newGlobalShortcuts(conn *dbus.Conn, s *sessions, start func(func(string, bool, uint64)) (shortcutDevice, error)) *globalShortcuts {
	return &globalShortcuts{
		conn: conn, sessions: s, start: start,
		routes: map[string]shortcutRoute{}, objects: map[string]func(){},
	}
}

func (g *globalShortcuts) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       GlobalShortcutsIface,
		Methods:    globalShortcutsObject{g},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// shortcutKey ties a compositor registration to its route; the unit
// separator keeps ("ab", "") apart from ("a", "b").
func shortcutKey(appID, id string) string { return appID + "\x1f" + id }

// device starts the device once; nil without one.
func (g *globalShortcuts) device() shortcutDevice {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dev != nil {
		return g.dev
	}
	dev, err := g.start(g.event)
	if err != nil {
		warnf("global shortcuts unavailable on this compositor: %v", err)
		return nil
	}
	g.dev = dev
	return dev
}

func (g *globalShortcuts) event(key string, pressed bool, sec uint64) {
	g.mu.Lock()
	route, ok := g.routes[key]
	g.mu.Unlock()
	if !ok {
		return
	}
	member := ".Deactivated"
	if pressed {
		member = ".Activated"
	}
	_ = g.conn.Emit(ObjectPath, GlobalShortcutsIface+member, route.session, route.id, sec, Vardict{})
}

// register binds the shortcuts not yet registered for appID; a re-bind
// of a registered app_id + id reuses it, since a second registration
// of the pair is the compositor's already_taken error.
func (g *globalShortcuts) register(session dbus.ObjectPath, appID string, shortcuts []shortcut) {
	if len(shortcuts) == 0 {
		return
	}
	dev := g.device()
	if dev == nil {
		return
	}
	for _, s := range shortcuts {
		key := shortcutKey(appID, s.ID)
		g.mu.Lock()
		_, taken := g.objects[key]
		g.mu.Unlock()
		if taken {
			continue
		}
		unregister, err := dev.register(key, s.ID, appID, stringOr(s.Props, "description", ""), stringOr(s.Props, "preferred_trigger", ""))
		if err != nil {
			warnf("global shortcuts: register %s: %v", s.ID, err)
			continue
		}
		g.mu.Lock()
		g.routes[key] = shortcutRoute{session, s.ID}
		g.objects[key] = unregister
		g.mu.Unlock()
	}
}

// closeSession unregisters the session's shortcuts.
func (g *globalShortcuts) closeSession(session dbus.ObjectPath) {
	g.mu.Lock()
	var stale []func()
	for key, r := range g.routes {
		if r.session == session {
			delete(g.routes, key)
			if unregister, ok := g.objects[key]; ok {
				stale = append(stale, unregister)
				delete(g.objects, key)
			}
		}
	}
	g.mu.Unlock()
	for _, unregister := range stale {
		unregister()
	}
}

// loopEnded drops the device and its registrations, which died with
// the compositor connection, and closes every session bound on them.
func (g *globalShortcuts) loopEnded() {
	g.mu.Lock()
	g.dev = nil
	g.routes = map[string]shortcutRoute{}
	g.objects = map[string]func(){}
	g.mu.Unlock()
	for _, s := range g.sessions.each(func(s *session) bool { return s.kind == kindGlobalShortcuts }) {
		g.sessions.close(s)
	}
}

// session is the live GlobalShortcuts session at path.
func (g *globalShortcuts) session(path dbus.ObjectPath) (*session, error) {
	s, ok := g.sessions.get(path)
	if !ok || s.kind != kindGlobalShortcuts {
		return nil, fmt.Errorf("no global-shortcuts session at %s", path)
	}
	return s, nil
}

// shortcutsOf is the session's bound shortcuts.
func shortcutsOf(s *session) []shortcut {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]shortcut{}, s.shortcuts.shortcuts...)
}

// mergeShortcuts replaces the props of ids already present, in place,
// and appends the new ones, as xdph treats a re-bind as an update.
func mergeShortcuts(existing, incoming []shortcut) []shortcut {
	for _, in := range incoming {
		found := false
		for i := range existing {
			if existing[i].ID == in.ID {
				existing[i].Props = in.Props
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, in)
		}
	}
	return existing
}

// globalShortcutsObject carries the interface's D-Bus methods.
type globalShortcutsObject struct{ g *globalShortcuts }

// CreateSession opens a session and registers the shortcuts its
// options carry (xdph does too).
func (o globalShortcutsObject) CreateSession(_, session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.sessions.create(session, kindGlobalShortcuts, appID)
	if err != nil {
		warnf("global shortcuts: cannot mount session: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	var shortcuts []shortcut
	if v, ok := options["shortcuts"]; ok {
		if err := dbus.Store([]any{v.Value()}, &shortcuts); err != nil {
			shortcuts = nil
		}
	}
	s.mu.Lock()
	s.shortcuts = &shortcutState{shortcuts: shortcuts}
	s.mu.Unlock()
	s.onClose(func() { o.g.closeSession(session) })
	o.g.register(session, appID, shortcuts)
	return ResponseSuccess, Vardict{}, nil
}

// BindShortcuts registers shortcuts and replies the session's merged
// set; an unknown session fails.
func (o globalShortcutsObject) BindShortcuts(_, session dbus.ObjectPath, shortcuts []shortcut, _ string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.session(session)
	if err != nil {
		warnf("global shortcuts: BindShortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	o.g.register(session, s.appID, shortcuts)
	s.mu.Lock()
	s.shortcuts.shortcuts = mergeShortcuts(s.shortcuts.shortcuts, shortcuts)
	s.mu.Unlock()
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(shortcutsOf(s))}, nil
}

// ListShortcuts replies the session's bound shortcuts; an unknown
// session fails.
func (o globalShortcutsObject) ListShortcuts(_, session dbus.ObjectPath) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.session(session)
	if err != nil {
		warnf("global shortcuts: ListShortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(shortcutsOf(s))}, nil
}
