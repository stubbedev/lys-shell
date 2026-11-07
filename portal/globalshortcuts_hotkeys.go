package portal

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/portal/permissions"
)

// hotkeyDevice binds the trigger the user chose (waylandHotkeys,
// xx_hotkey_v1).
type hotkeyDevice interface {
	shortcutDevice
	// bind binds a shortcut of appID under key, the route its events
	// carry, to trigger once the compositor accepted it; a refused
	// trigger binds nothing and is an *app.HotkeyDeniedError.
	bind(key, appID, description string, trigger app.Accel) (hotkey, error)
}

// hotkey is one shortcut bound on a hotkeyDevice.
type hotkey interface {
	// rebind moves it to trigger; refused, it keeps its old trigger and
	// the error is an *app.HotkeyDeniedError.
	rebind(trigger app.Accel) error
	unbind()
}

// shortcutsTitle heads the shortcuts editor and configure dialog.
const shortcutsTitle = "Global shortcuts"

// shortcutsInBackground says what global shortcuts are for.
const shortcutsInBackground = "They work while the app is in the background."

// shortcutsNote tells the user what the shortcuts do, noting when they
// will not be remembered.
func shortcutsNote(owner shortcutOwner) string {
	note := shortcutsInBackground
	if !owner.identified() {
		note += unidentifiedNote
	}
	return note
}

// storedTrigger is the trigger kept for id, "" when none is.
func storedTrigger(before []permissions.Shortcut, id string) string {
	if i := slices.IndexFunc(before, func(s permissions.Shortcut) bool { return s.ID == id }); i >= 0 {
		return before[i].Trigger
	}
	return ""
}

// hotkeyTriggers is each of the owner's shortcuts' accelerator label
// over a hotkeyDevice: the one assigned now, else the one kept from
// before, "" while unassigned.
func (g *globalShortcuts) hotkeyTriggers(owner shortcutOwner, before []permissions.Shortcut) func(string) string {
	return func(id string) string {
		g.mu.Lock()
		reg, ok := g.regs[shortcutKey(owner.name, id)]
		trigger := ""
		if ok {
			trigger = reg.trigger
		}
		g.mu.Unlock()
		if ok {
			return trigger
		}
		return storedTrigger(before, id)
	}
}

// preferredTrigger is the app's preferred_trigger, in the XDG shortcut
// syntax ("CTRL+ALT+p"), as an accelerator label; "" when it gave none
// or one that names no key.
func preferredTrigger(sc shortcut) string {
	preferred := stringOr(sc.Props, "preferred_trigger", "")
	if preferred == "" {
		return ""
	}
	accel, err := app.ParseAccel(preferred)
	if err != nil {
		warnf("global shortcuts: %s's preferred trigger %q: %v", sc.ID, preferred, err)
		return ""
	}
	return accel.String()
}

// shownAs is how the editor and the compositor name a shortcut: its
// description, else its id.
func shownAs(sc shortcut) string {
	if d := stringOr(sc.Props, "description", ""); d != "" {
		return d
	}
	return sc.ID
}

// refusal is why the compositor refused trigger, for its row.
func refusal(trigger string, denied *app.HotkeyDeniedError) string {
	switch {
	case denied.Message != "":
		return denied.Message
	case denied.Reason == app.HotkeyAlreadyBound:
		return "Another shortcut already uses " + trigger + "."
	}
	return trigger + " cannot be a global shortcut."
}

// tryAssign is assign with a refusal as the reason to show on the
// shortcut's row ("" when the trigger was bound) rather than an error;
// the error is a failure of the compositor connection.
func (g *globalShortcuts) tryAssign(dev hotkeyDevice, session dbus.ObjectPath, owner shortcutOwner, id, description, trigger string) (string, error) {
	err := g.assign(dev, session, owner, id, description, trigger)
	var denied *app.HotkeyDeniedError
	var unparsed *triggerError
	switch {
	case errors.As(err, &denied):
		return refusal(trigger, denied), nil
	case errors.As(err, &unparsed):
		return trigger + " is not a key a shortcut can use.", nil
	}
	return "", err
}

// triggerError is a trigger label that names no key.
type triggerError struct{ err error }

func (e *triggerError) Error() string { return e.err.Error() }
func (e *triggerError) Unwrap() error { return e.err }

// assign gives the owner's shortcut id the trigger label ("" leaves it
// unassigned) on the compositor, shared by every session of the owner,
// and adds session to it. A refused trigger changes nothing: a new
// shortcut stays unregistered, a bound one keeps its old trigger.
func (g *globalShortcuts) assign(dev hotkeyDevice, session dbus.ObjectPath, owner shortcutOwner, id, description, trigger string) error {
	key := shortcutKey(owner.name, id)
	g.mu.Lock()
	defer g.mu.Unlock()
	reg, ok := g.regs[key]
	if !ok {
		reg = &registration{id: id, owner: owner}
	}
	if !ok || reg.trigger != trigger {
		hk, err := retrigger(dev, key, owner.name, description, reg.hotkey, trigger)
		if err != nil {
			return err
		}
		reg.hotkey, reg.trigger, reg.unregister = hk, trigger, nil
		if hk != nil {
			reg.unregister = hk.unbind
		}
	}
	g.regs[key] = reg
	if !slices.Contains(reg.sessions, session) {
		reg.sessions = append(reg.sessions, session)
	}
	return nil
}

// retrigger moves hk (nil when unbound) to trigger: binds it, rebinds
// it, or with "" unbinds it.
func retrigger(dev hotkeyDevice, key, appID, description string, hk hotkey, trigger string) (hotkey, error) {
	if trigger == "" {
		if hk != nil {
			hk.unbind()
		}
		return nil, nil
	}
	accel, err := app.ParseAccel(trigger)
	if err != nil {
		return hk, &triggerError{fmt.Errorf("trigger %q: %w", trigger, err)}
	}
	if hk == nil {
		return dev.bind(key, appID, description, accel)
	}
	return hk, hk.rebind(accel)
}

// revoked drops a binding the compositor withdrew: the shortcut is
// unassigned, so its trigger is not committed again behind the user's
// back, and every session of its app hears ShortcutsChanged.
func (g *globalShortcuts) revoked(key, message string) {
	g.mu.Lock()
	reg, ok := g.regs[key]
	if !ok || reg.hotkey == nil {
		g.mu.Unlock()
		return
	}
	reg.hotkey, reg.trigger, reg.unregister = nil, "", nil
	owner, id := reg.owner, reg.id
	g.mu.Unlock()
	warnf("global shortcuts: the compositor withdrew %s's %s: %s", owner.name, id, message)
	g.triggersChanged(owner)
}

// triggersChanged keeps the owner's triggers and tells each of its
// bound sessions its shortcuts with ShortcutsChanged.
func (g *globalShortcuts) triggersChanged(owner shortcutOwner) {
	before, err := g.boundBefore(owner)
	if err != nil {
		warnf("global shortcuts: %v", err)
	}
	if err := g.remember(owner, keptAs(asShortcuts(before), g.hotkeyTriggers(owner, before))); err != nil {
		warnf("global shortcuts: keeping %s's shortcuts: %v", owner.name, err)
	}
	triggerOf := g.hotkeyTriggers(owner, before)
	for _, s := range g.sessions.each(func(s *session) bool { return s.kind == kindGlobalShortcuts && ownerOf(s) == owner }) {
		s.mu.Lock()
		bound, list := s.shortcuts.bound, slices.Clone(s.shortcuts.shortcuts)
		s.mu.Unlock()
		if !bound {
			continue
		}
		if err := g.conn.Emit(ObjectPath, GlobalShortcutsIface+".ShortcutsChanged", s.path, described(list, triggerOf)); err != nil {
			warnf("global shortcuts: ShortcutsChanged for %s: %v", s.path, err)
		}
	}
}

// editTriggers shows rows in the shortcuts editor until every trigger
// the user leaves is bound: each pass assigns what the user confirmed,
// and the triggers the compositor refused come back on their rows with
// why. ask shows one pass and reports whether the user confirmed it;
// editTriggers reports whether every pass was, and fails when the
// compositor connection does.
func (g *globalShortcuts) editTriggers(dev hotkeyDevice, session dbus.ObjectPath, owner shortcutOwner, rows []portaldialogs.ShortcutRow, ask func([]portaldialogs.ShortcutRow) ([]portaldialogs.ShortcutTrigger, bool)) (bool, error) {
	descriptions := make(map[string]string, len(rows))
	for _, r := range rows {
		descriptions[r.ID] = r.Description
	}
	for len(rows) > 0 {
		triggers, ok := ask(rows)
		if !ok {
			return false, nil
		}
		var refused []portaldialogs.ShortcutRow
		for _, t := range triggers {
			why, err := g.tryAssign(dev, session, owner, t.ID, descriptions[t.ID], t.Trigger)
			if err != nil {
				return false, err
			}
			if why != "" {
				refused = append(refused, portaldialogs.ShortcutRow{ID: t.ID, Description: descriptions[t.ID], Trigger: t.Trigger, Refusal: why})
			}
		}
		rows = refused
	}
	return true, nil
}

// bindHotkeys binds the session's shortcuts on the hotkeyDevice: the
// ones the owner bound before take their kept triggers with no dialog;
// the rest, and kept ones the compositor now refuses, go to the editor,
// starting from the app's preferred trigger. A cancel or a failure
// leaves the shortcuts this call bound unbound again.
func (o globalShortcutsObject) bindHotkeys(dev hotkeyDevice, dialogs *portaldialogs.Client, c *call, s *session, owner shortcutOwner, before []permissions.Shortcut, shortcuts []shortcut) (uint32, func(string) string) {
	joined := o.g.unjoined(s.path, owner, shortcuts)
	fail := func(code uint32) (uint32, func(string) string) {
		o.g.release(s.path, func(key string) bool { return joined[key] })
		return code, nil
	}
	var rows []portaldialogs.ShortcutRow
	for _, sc := range shortcuts {
		kept := slices.IndexFunc(before, func(b permissions.Shortcut) bool { return b.ID == sc.ID })
		if kept < 0 {
			rows = append(rows, portaldialogs.ShortcutRow{ID: sc.ID, Description: shownAs(sc), Trigger: preferredTrigger(sc)})
			continue
		}
		trigger := before[kept].Trigger
		why, err := o.g.tryAssign(dev, s.path, owner, sc.ID, shownAs(sc), trigger)
		if err != nil {
			warnf("global shortcuts: %v", err)
			return fail(ResponseOther)
		}
		if why != "" {
			rows = append(rows, portaldialogs.ShortcutRow{ID: sc.ID, Description: shownAs(sc), Trigger: trigger, Refusal: why})
		}
	}
	code := ResponseSuccess
	confirmed, err := o.g.editTriggers(dev, s.path, owner, rows, func(rows []portaldialogs.ShortcutRow) ([]portaldialogs.ShortcutTrigger, bool) {
		ok, triggers, err := dialogs.BindShortcuts(c.on(portaldialogs.ServiceName), c.caller, portaldialogs.ShortcutsRequest{
			Title: shortcutsTitle, Note: shortcutsNote(owner), Rows: rows,
		})
		code = c.response("global shortcuts", ok, err)
		return triggers, code == ResponseSuccess
	})
	if err != nil {
		warnf("global shortcuts: %v", err)
		return fail(ResponseOther)
	}
	if !confirmed {
		return fail(code)
	}
	return ResponseSuccess, o.g.hotkeyTriggers(owner, before)
}

// unjoined are the routes of the shortcuts session is not yet in.
func (g *globalShortcuts) unjoined(session dbus.ObjectPath, owner shortcutOwner, shortcuts []shortcut) map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]bool{}
	for _, sc := range shortcuts {
		key := shortcutKey(owner.name, sc.ID)
		if reg, ok := g.regs[key]; !ok || !slices.Contains(reg.sessions, session) {
			out[key] = true
		}
	}
	return out
}

// configureHotkeys reopens the editor over the session's shortcuts and
// their triggers; what the user changes is bound, kept, and announced
// to every session of the app with ShortcutsChanged.
func (g *globalShortcuts) configureHotkeys(dev hotkeyDevice, caller portaldialogs.Caller, s *session, list []shortcut) {
	owner := ownerOf(s)
	triggerOf := g.hotkeyTriggers(owner, nil)
	rows := make([]portaldialogs.ShortcutRow, len(list))
	was := make(map[string]string, len(list))
	for i, sc := range list {
		was[sc.ID] = triggerOf(sc.ID)
		rows[i] = portaldialogs.ShortcutRow{ID: sc.ID, Description: shownAs(sc), Trigger: was[sc.ID]}
	}
	ctx, cancel := context.WithTimeout(context.Background(), configureDialogTimeout)
	defer cancel()
	dialogs := portaldialogs.NewClient(g.conn)
	if _, err := g.editTriggers(dev, s.path, owner, rows, func(rows []portaldialogs.ShortcutRow) ([]portaldialogs.ShortcutTrigger, bool) {
		ok, triggers, err := dialogs.BindShortcuts(ctx, caller, portaldialogs.ShortcutsRequest{Title: shortcutsTitle, Note: shortcutsNote(owner), Rows: rows})
		if err != nil {
			warnf("global shortcuts: configure dialog: %v", err)
		}
		return triggers, ok
	}); err != nil {
		warnf("global shortcuts: configure: %v", err)
	}
	for id, trigger := range was {
		if triggerOf(id) != trigger {
			g.triggersChanged(owner)
			return
		}
	}
}
