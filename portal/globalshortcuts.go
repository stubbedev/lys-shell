package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/atomicfile"
	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// GlobalShortcutsIface bridges org.freedesktop.impl.portal.GlobalShortcuts
// to the compositor's hyprland-global-shortcuts-v1, as
// xdg-desktop-portal-hyprland does: shortcuts
// the user allows are registered there, and their presses come back as
// Activated / Deactivated. The compositor owns the triggers: the user
// binds each shortcut in its config (Hyprland's `global` dispatcher).
// Without the protocol nothing can be bound.
const GlobalShortcutsIface = "org.freedesktop.impl.portal.GlobalShortcuts"

// shortcutDevice is the compositor side (waylandShortcuts; a fake in
// tests).
type shortcutDevice interface {
	// register binds id for appID under key, the route its events carry,
	// with the app's preferred trigger as a hint; the returned func
	// unregisters it.
	register(key, id, appID, description, trigger string) (unregister func(), err error)
}

// shortcut is one (sa{sv}): the id and its properties.
type shortcut struct {
	ID    string
	Props Vardict
}

// shortcutState is a GlobalShortcuts session's shortcuts, once it bound
// some; it lives on the session, guarded by its mu.
type shortcutState struct {
	bound     bool
	shortcuts []shortcut
}

// registration is one compositor registration of an app's shortcut,
// shared by every session of the app that bound it: the compositor
// refuses a second registration of the same app id and id.
type registration struct {
	id         string
	unregister func()
	sessions   []dbus.ObjectPath
}

// globalShortcuts is the interface: the compositor registrations, and
// the shortcuts each app bound before, kept across sessions; each
// session's binds live on its record.
type globalShortcuts struct {
	conn     *dbus.Conn
	sessions *sessions
	// start brings the device up on first use; onEvent gets every
	// press and release.
	start func(onEvent func(key string, pressed bool)) (shortcutDevice, error)
	// now is the signals' millisecond clock (of an undefined base, as
	// the spec allows: monotonic since the portal started).
	now func() uint64
	// storePath keeps each app's bound shortcuts, "" without a state
	// directory; storeMu serializes its reads and writes.
	storePath string
	storeMu   sync.Mutex

	mu   sync.Mutex
	dev  shortcutDevice
	regs map[string]*registration
}

func newGlobalShortcuts(conn *dbus.Conn, s *sessions, storePath string, start func(func(string, bool)) (shortcutDevice, error)) *globalShortcuts {
	started := time.Now()
	return &globalShortcuts{
		conn: conn, sessions: s, start: start, storePath: storePath,
		now:  func() uint64 { return uint64(time.Since(started).Milliseconds()) },
		regs: map[string]*registration{},
	}
}

func (g *globalShortcuts) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       GlobalShortcutsIface,
		Methods:    globalShortcutsObject{g},
		Properties: dbusx.Getters{"version": func() any { return uint32(2) }},
	}
}

// shortcutKey ties a compositor registration to its route; the unit
// separator keeps ("ab", "") apart from ("a", "b").
func shortcutKey(appID, id string) string { return appID + "\x1f" + id }

// device starts the device once.
func (g *globalShortcuts) device() (shortcutDevice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dev != nil {
		return g.dev, nil
	}
	dev, err := g.start(g.event)
	if err != nil {
		return nil, fmt.Errorf("no global shortcuts on this compositor: %w", err)
	}
	g.dev = dev
	return dev, nil
}

// event signals a press or release to every session that bound the
// shortcut.
func (g *globalShortcuts) event(key string, pressed bool) {
	stamp := g.now()
	g.mu.Lock()
	var id string
	var sessions []dbus.ObjectPath
	if reg, ok := g.regs[key]; ok {
		id, sessions = reg.id, slices.Clone(reg.sessions)
	}
	g.mu.Unlock()
	member := ".Deactivated"
	if pressed {
		member = ".Activated"
	}
	for _, s := range sessions {
		if err := g.conn.Emit(ObjectPath, GlobalShortcutsIface+member, s, id, stamp, Vardict{}); err != nil {
			warnf("global shortcuts: %s for %s: %v", member[1:], s, err)
		}
	}
}

// register binds the shortcuts for the session: an app's shortcut
// registered for another session of it is shared, any other is
// registered with the compositor.
func (g *globalShortcuts) register(dev shortcutDevice, session dbus.ObjectPath, appID string, shortcuts []shortcut) error {
	for _, s := range shortcuts {
		key := shortcutKey(appID, s.ID)
		g.mu.Lock()
		reg, ok := g.regs[key]
		if ok && !slices.Contains(reg.sessions, session) {
			reg.sessions = append(reg.sessions, session)
		}
		g.mu.Unlock()
		if ok {
			continue
		}
		unregister, err := dev.register(key, s.ID, appID, stringOr(s.Props, "description", ""), stringOr(s.Props, "preferred_trigger", ""))
		if err != nil {
			return fmt.Errorf("register %s: %w", s.ID, err)
		}
		g.mu.Lock()
		g.regs[key] = &registration{id: s.ID, unregister: unregister, sessions: []dbus.ObjectPath{session}}
		g.mu.Unlock()
	}
	return nil
}

// closeSession drops the session from its registrations, unregistering
// the ones no other session shares.
func (g *globalShortcuts) closeSession(session dbus.ObjectPath) {
	g.mu.Lock()
	var stale []func()
	for key, reg := range g.regs {
		reg.sessions = slices.DeleteFunc(reg.sessions, func(s dbus.ObjectPath) bool { return s == session })
		if len(reg.sessions) == 0 {
			stale = append(stale, reg.unregister)
			delete(g.regs, key)
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
	g.regs = map[string]*registration{}
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

// shortcutStorePath is $XDG_STATE_HOME/lys-shell/portal-shortcuts.json,
// "" without a state directory.
func shortcutStorePath() string {
	dir, ok := xdg.StateDir()
	if !ok {
		return ""
	}
	return filepath.Join(dir, "portal-shortcuts.json")
}

// storedShortcut is one shortcut an app bound, as the store keeps it.
type storedShortcut struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// stored is the shortcuts each app bound before, by app id; the caller
// holds storeMu.
func (g *globalShortcuts) stored() (map[string][]storedShortcut, error) {
	all := map[string][]storedShortcut{}
	if g.storePath == "" {
		return all, nil
	}
	data, err := os.ReadFile(g.storePath)
	if errors.Is(err, fs.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", g.storePath, err)
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse %s: %w", g.storePath, err)
	}
	return all, nil
}

// remember keeps the app's bound shortcuts for its later sessions.
func (g *globalShortcuts) remember(appID string, shortcuts []shortcut) error {
	if g.storePath == "" {
		return errors.New("no state directory to keep the shortcuts in")
	}
	g.storeMu.Lock()
	defer g.storeMu.Unlock()
	all, err := g.stored()
	if err != nil {
		return err
	}
	kept := make([]storedShortcut, len(shortcuts))
	for i, s := range shortcuts {
		kept[i] = storedShortcut{ID: s.ID, Description: stringOr(s.Props, "description", "")}
	}
	all[appID] = kept
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.storePath), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(g.storePath, data, 0o600)
}

// boundBefore is the app's shortcuts from earlier sessions.
func (g *globalShortcuts) boundBefore(appID string) ([]shortcut, error) {
	g.storeMu.Lock()
	all, err := g.stored()
	g.storeMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]shortcut, 0, len(all[appID]))
	for _, s := range all[appID] {
		out = append(out, shortcut{ID: s.ID, Props: Vardict{"description": dbus.MakeVariant(s.Description)}})
	}
	return out, nil
}

// triggerDescription tells the user how the shortcut is triggered: by
// the bind they give it in the compositor's config, with Hyprland's
// `global` dispatcher.
func triggerDescription(appID, id string) string {
	return "Bind in your compositor with global, " + appID + ":" + id
}

// described are the shortcuts as the results carry them: the
// description and the trigger description, never the app's preferred
// trigger, which the compositor's binds decide.
func described(appID string, shortcuts []shortcut) []shortcut {
	out := make([]shortcut, len(shortcuts))
	for i, s := range shortcuts {
		out[i] = shortcut{ID: s.ID, Props: Vardict{
			"description":         dbus.MakeVariant(stringOr(s.Props, "description", "")),
			"trigger_description": dbus.MakeVariant(triggerDescription(appID, s.ID)),
		}}
	}
	return out
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

// shortcutLines lists shortcuts for a dialog, one per line.
func shortcutLines(appID string, shortcuts []shortcut) string {
	lines := make([]string, len(shortcuts))
	for i, s := range shortcuts {
		lines[i] = "• " + stringOr(s.Props, "description", s.ID) + ": " + triggerDescription(appID, s.ID)
	}
	return strings.Join(lines, "\n")
}

// globalShortcutsObject carries the interface's D-Bus methods.
type globalShortcutsObject struct{ g *globalShortcuts }

// CreateSession opens a session; its shortcuts come with BindShortcuts.
func (o globalShortcutsObject) CreateSession(_, session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.sessions.create(session, kindGlobalShortcuts, appID)
	if err != nil {
		warnf("global shortcuts: cannot mount session: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	s.shortcuts = &shortcutState{}
	s.mu.Unlock()
	s.onClose(func() { o.g.closeSession(session) })
	return ResponseSuccess, Vardict{}, nil
}

// BindShortcuts registers the shortcuts with the compositor and replies
// the session's set. Shortcuts the app did not bind before are listed
// for the user to allow first, with how to give them keys; a compositor
// without the protocol fails the call.
func (o globalShortcutsObject) BindShortcuts(handle, session dbus.ObjectPath, shortcuts []shortcut, parent string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.session(session)
	if err != nil {
		warnf("global shortcuts: BindShortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	dev, err := o.g.device()
	if err != nil {
		warnf("global shortcuts: BindShortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	before, err := o.g.boundBefore(s.appID)
	if err != nil {
		warnf("global shortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	fresh := slices.DeleteFunc(slices.Clone(shortcuts), func(sc shortcut) bool {
		return slices.ContainsFunc(before, func(b shortcut) bool { return b.ID == sc.ID })
	})
	dialogs := portaldialogs.NewClient(o.g.conn)
	c, ok := beginOr(o.g.conn, handle, s.appID, parent, dialogs.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	if len(fresh) > 0 {
		allowed, _, err := dialogs.Access(c.ctx, c.caller, portaldialogs.AccessRequest{
			Title: "Allow global shortcuts?", Subtitle: "They work while the app is in the background.",
			Body: shortcutLines(s.appID, fresh), GrantLabel: "Allow", DenyLabel: "Deny", Icon: "preferences-desktop-keyboard-shortcuts-symbolic",
		})
		if code := c.response("global shortcuts", allowed, err); code != ResponseSuccess {
			return code, Vardict{}, nil
		}
	}
	if err := o.g.register(dev, session, s.appID, shortcuts); err != nil {
		warnf("global shortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	s.shortcuts.bound = true
	s.shortcuts.shortcuts = mergeShortcuts(s.shortcuts.shortcuts, shortcuts)
	merged := slices.Clone(s.shortcuts.shortcuts)
	s.mu.Unlock()
	if err := o.g.remember(s.appID, mergeShortcuts(before, shortcuts)); err != nil {
		warnf("global shortcuts: keeping %s's shortcuts: %v", s.appID, err)
	}
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(described(s.appID, merged))}, nil
}

// ListShortcuts replies the session's shortcuts once it bound some,
// else the ones the app bound in earlier sessions; an unknown session
// fails.
func (o globalShortcutsObject) ListShortcuts(_, session dbus.ObjectPath) (uint32, Vardict, *dbus.Error) {
	s, err := o.g.session(session)
	if err != nil {
		warnf("global shortcuts: ListShortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	bound, list := s.shortcuts.bound, slices.Clone(s.shortcuts.shortcuts)
	s.mu.Unlock()
	if !bound {
		if list, err = o.g.boundBefore(s.appID); err != nil {
			warnf("global shortcuts: %v", err)
			return ResponseOther, Vardict{}, nil
		}
	}
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(described(s.appID, list))}, nil
}

// ConfigureShortcuts shows the session's shortcuts and how to give
// them keys: the compositor's binds are where they are configured. The
// dialog stays up on its own; the call returns at once.
func (o globalShortcutsObject) ConfigureShortcuts(session dbus.ObjectPath, parent string, _ Vardict) *dbus.Error {
	s, err := o.g.session(session)
	if err != nil {
		return dbusx.Failed(err.Error())
	}
	s.mu.Lock()
	list := slices.Clone(s.shortcuts.shortcuts)
	s.mu.Unlock()
	body := "This app has no shortcuts bound."
	if len(list) > 0 {
		body = shortcutLines(s.appID, list)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), configureDialogTimeout)
		defer cancel()
		_, _, err := portaldialogs.NewClient(o.g.conn).Access(ctx, portalreq.Caller{AppID: s.appID, ParentWindow: parent}, portaldialogs.AccessRequest{
			Title: "Global shortcuts", Subtitle: "Their keys are set in your compositor's binds.",
			Body: body, GrantLabel: "Done", DenyLabel: "Close", Icon: "preferences-desktop-keyboard-shortcuts-symbolic",
		})
		if err != nil {
			warnf("global shortcuts: configure dialog: %v", err)
		}
	}()
	return nil
}

// configureDialogTimeout bounds how long the configure dialog's call
// waits on the user.
const configureDialogTimeout = 30 * time.Minute
