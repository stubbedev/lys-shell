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

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/internal/version"
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
		now:  func() uint64 { return intconv.Clamp[uint64](time.Since(started).Milliseconds()) },
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
func shortcutKey(name, id string) string { return name + "\x1f" + id }

// shortcutOwner is whose shortcuts a session binds. An identified app
// owns them by its id: its sessions share the registrations and its
// later sessions find them allowed. A caller the frontend could not
// identify (app id "") has no identity to share or remember them under,
// so its session alone owns them, under a name of its own, and the user
// is asked each time.
type shortcutOwner struct {
	// appID is the app's id, "" for an unidentified caller.
	appID string
	// name is the app id the compositor registers the shortcuts under
	// and the user binds them by.
	name string
}

func ownerOf(s *session) shortcutOwner {
	if s.appID != "" {
		return shortcutOwner{appID: s.appID, name: s.appID}
	}
	parts := strings.Split(string(s.path), "/")
	return shortcutOwner{name: "unidentified-" + strings.Join(parts[max(len(parts)-2, 0):], "-")}
}

// identified reports an owner with an app id to remember shortcuts by.
func (o shortcutOwner) identified() bool { return o.appID != "" }

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

// register binds the shortcuts for the session: an owner's shortcut
// registered for another of its sessions is shared, any other is
// registered with the compositor.
func (g *globalShortcuts) register(dev shortcutDevice, session dbus.ObjectPath, owner shortcutOwner, shortcuts []shortcut) error {
	for _, s := range shortcuts {
		key := shortcutKey(owner.name, s.ID)
		g.mu.Lock()
		reg, ok := g.regs[key]
		if ok && !slices.Contains(reg.sessions, session) {
			reg.sessions = append(reg.sessions, session)
		}
		g.mu.Unlock()
		if ok {
			continue
		}
		unregister, err := dev.register(key, s.ID, owner.name, stringOr(s.Props, "description", ""), stringOr(s.Props, "preferred_trigger", ""))
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
	dir, ok := xdg.StateDir(version.Name)
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

// remember keeps an identified owner's bound shortcuts for its later
// sessions; an unidentified one's are not kept.
func (g *globalShortcuts) remember(owner shortcutOwner, shortcuts []shortcut) error {
	if !owner.identified() {
		return nil
	}
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
	all[owner.appID] = kept
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.storePath), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(g.storePath, data, 0o600)
}

// boundBefore is an identified owner's shortcuts from earlier
// sessions; an unidentified one has none.
func (g *globalShortcuts) boundBefore(owner shortcutOwner) ([]shortcut, error) {
	if !owner.identified() {
		return nil, nil
	}
	g.storeMu.Lock()
	all, err := g.stored()
	g.storeMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]shortcut, 0, len(all[owner.appID]))
	for _, s := range all[owner.appID] {
		out = append(out, shortcut{ID: s.ID, Props: Vardict{"description": dbus.MakeVariant(s.Description)}})
	}
	return out, nil
}

// triggerDescription tells the user how the shortcut is triggered: by
// the bind they give it in the compositor's config, with Hyprland's
// `global` dispatcher.
func triggerDescription(name, id string) string {
	return "Bind in your compositor with global, " + name + ":" + id
}

// described are the shortcuts as the results carry them: the
// description and the trigger description, never the app's preferred
// trigger, which the compositor's binds decide.
func described(name string, shortcuts []shortcut) []shortcut {
	out := make([]shortcut, len(shortcuts))
	for i, s := range shortcuts {
		out[i] = shortcut{ID: s.ID, Props: Vardict{
			"description":         dbus.MakeVariant(stringOr(s.Props, "description", "")),
			"trigger_description": dbus.MakeVariant(triggerDescription(name, s.ID)),
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
func shortcutLines(name string, shortcuts []shortcut) string {
	lines := make([]string, len(shortcuts))
	for i, s := range shortcuts {
		lines[i] = "• " + stringOr(s.Props, "description", s.ID) + ": " + triggerDescription(name, s.ID)
	}
	return strings.Join(lines, "\n")
}

// unidentifiedNote tells the user an unidentified app's shortcuts last
// for this session only.
const unidentifiedNote = "\n\nThis app could not be identified, so they last only until it closes this session."

// consentBody lists the shortcuts to allow, noting when they will not
// be remembered.
func consentBody(owner shortcutOwner, shortcuts []shortcut) string {
	body := shortcutLines(owner.name, shortcuts)
	if !owner.identified() {
		body += unidentifiedNote
	}
	return body
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
	owner := ownerOf(s)
	before, err := o.g.boundBefore(owner)
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
			Body: consentBody(owner, fresh), GrantLabel: "Allow", DenyLabel: "Deny", Icon: shortcutsIcon,
		})
		if code := c.response("global shortcuts", allowed, err); code != ResponseSuccess {
			return code, Vardict{}, nil
		}
	}
	if err := o.g.register(dev, session, owner, shortcuts); err != nil {
		warnf("global shortcuts: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	s.shortcuts.bound = true
	s.shortcuts.shortcuts = mergeShortcuts(s.shortcuts.shortcuts, shortcuts)
	merged := slices.Clone(s.shortcuts.shortcuts)
	s.mu.Unlock()
	if err := o.g.remember(owner, mergeShortcuts(before, shortcuts)); err != nil {
		warnf("global shortcuts: keeping %s's shortcuts: %v", owner.name, err)
	}
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(described(owner.name, merged))}, nil
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
	owner := ownerOf(s)
	if !bound {
		if list, err = o.g.boundBefore(owner); err != nil {
			warnf("global shortcuts: %v", err)
			return ResponseOther, Vardict{}, nil
		}
	}
	return ResponseSuccess, Vardict{"shortcuts": dbus.MakeVariant(described(owner.name, list))}, nil
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
		body = shortcutLines(ownerOf(s).name, list)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), configureDialogTimeout)
		defer cancel()
		_, _, err := portaldialogs.NewClient(o.g.conn).Access(ctx, portalreq.Caller{AppID: s.appID, ParentWindow: parent}, portaldialogs.AccessRequest{
			Title: "Global shortcuts", Subtitle: "Their keys are set in your compositor's binds.",
			Body: body, GrantLabel: "Done", DenyLabel: "Close", Icon: shortcutsIcon,
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
