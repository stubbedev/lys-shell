package portal

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/portal/permissions"
)

// fakeHotkeys is a compositor offering xx_hotkey_v1: it binds any
// trigger nobody holds, and a press of a trigger reaches whichever
// route holds it.
type fakeHotkeys struct {
	events dbustest.Var[shortcutEvents]
	// held are the triggers a compositor bind or another app holds.
	held dbustest.Var[[]string]
	// bound is each route's trigger.
	bound dbustest.Var[map[string]string]
	binds dbustest.Var[int]
}

func (*fakeHotkeys) shortcutDevice() {}

func (f *fakeHotkeys) take(key string, trigger app.Accel) error {
	label := trigger.String()
	if slices.Contains(f.held.Load(), label) {
		return &app.HotkeyDeniedError{Reason: app.HotkeyAlreadyBound}
	}
	f.bound.Update(func(b map[string]string) map[string]string {
		b = maps.Clone(b)
		if b == nil {
			b = map[string]string{}
		}
		b[key] = label
		return b
	})
	return nil
}

func (f *fakeHotkeys) bind(key, _, _ string, trigger app.Accel) (hotkey, error) {
	f.binds.Update(func(n int) int { return n + 1 })
	if err := f.take(key, trigger); err != nil {
		return nil, err
	}
	return fakeHotkey{f, key}, nil
}

// press presses and releases trigger on the compositor.
func (f *fakeHotkeys) press(trigger string) {
	for key, label := range f.bound.Load() {
		if label == trigger {
			f.events.Load().pressed(key, true)
			f.events.Load().pressed(key, false)
		}
	}
}

type fakeHotkey struct {
	f   *fakeHotkeys
	key string
}

func (h fakeHotkey) rebind(trigger app.Accel) error { return h.f.take(h.key, trigger) }

func (h fakeHotkey) unbind() {
	h.f.bound.Update(func(b map[string]string) map[string]string {
		b = maps.Clone(b)
		delete(b, h.key)
		return b
	})
}

// hotkeysRig is shortcutsRig over xx_hotkey_v1, keeping the store at the
// path it returns.
func hotkeysRig(t *testing.T) (*rig, *fakeHotkeys, *fakeDialogs, string) {
	t.Helper()
	f := &fakeHotkeys{}
	store := filepath.Join(t.TempDir(), "portal-shortcuts.json")
	r := newRig(t, func(b *Backend) {
		b.shortcuts = newGlobalShortcuts(b.conn, b.sessions, store, func(events shortcutEvents) (shortcutDevice, error) {
			f.events.Store(events)
			return f, nil
		})
		b.shortcuts.now = func() uint64 { return 1234 }
	})
	return r, f, serveDialogs(t, r, true), store
}

func preferring(id, description, trigger string) shortcut {
	return shortcut{id, Vardict{"description": dbus.MakeVariant(description), "preferred_trigger": dbus.MakeVariant(trigger)}}
}

func triggers(list []shortcut) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID+"="+stringOr(s.Props, "trigger_description", "?"))
	}
	return out
}

// activations are the shortcut ids of the Activated signals of a short
// wait.
func (r *rig) activations(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, s := range r.shortcutSignals(t) {
		if s.Name == GlobalShortcutsIface+".Activated" {
			ids = append(ids, s.Body[1].(string))
		}
	}
	return ids
}

// editors are the rows of each shortcuts editor shown, in order.
func editors(d *fakeDialogs) [][]portaldialogs.ShortcutRow {
	var out [][]portaldialogs.ShortcutRow
	for _, e := range d.editors.Load() {
		out = append(out, e.Rows)
	}
	return out
}

// The app's preferred trigger, accepted unchanged in the editor, is
// bound as is: the reply describes it as its accelerator, and pressing
// it activates the shortcut. No yes/no prompt is shown.
func TestHotkeysBindThePreferredTrigger(t *testing.T) {
	r, f, d, _ := hotkeysRig(t)
	r.shortcutSession(t, sessA)
	code, bound := r.bind(t, sessA, preferring("screenshot", "Take a screenshot", "CTRL+ALT+p"))
	if code != ResponseSuccess || !slices.Equal(triggers(bound), []string{"screenshot=Ctrl+Alt+P"}) {
		t.Fatalf("bind = %d %v", code, triggers(bound))
	}
	want := [][]portaldialogs.ShortcutRow{{{ID: "screenshot", Description: "Take a screenshot", Trigger: "Ctrl+Alt+P"}}}
	if got := editors(d); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("the editors showed %+v, want %+v", got, want)
	}
	if asked := d.asked.Load(); len(d.callers.Load()) != 1 || len(asked) != 1 {
		t.Errorf("asked %d dialogs, last %v; want the editor alone", len(d.callers.Load()), asked)
	}
	if _, has := bound[0].Props["preferred_trigger"]; has {
		t.Error("the reply carries the app's preferred trigger")
	}
	f.press("Ctrl+Alt+P")
	if got := r.activations(t); !slices.Equal(got, []string{"screenshot"}) {
		t.Errorf("pressing Ctrl+Alt+P activated %v", got)
	}
	f.press("Ctrl+Alt+Q")
	if got := r.activations(t); len(got) != 0 {
		t.Errorf("another key activated %v", got)
	}
	if got := triggers(r.listShortcuts(t, sessA)); !slices.Equal(got, []string{"screenshot=Ctrl+Alt+P"}) {
		t.Errorf("listed %v", got)
	}
}

// A preferred trigger the compositor reports already bound comes back
// on its row as a conflict, and nothing is bound for the shortcut until
// the user picks another key.
func TestHotkeysAConflictWaitsForAnotherKey(t *testing.T) {
	r, f, d, _ := hotkeysRig(t)
	f.held.Store([]string{"Ctrl+Alt+P"})
	r.shortcutSession(t, sessA)
	d.editor.Store(func(n int) (map[string]string, bool) {
		if n == 1 {
			if len(f.bound.Load()) != 0 {
				t.Errorf("bound %v while the conflict was shown", f.bound.Load())
			}
			return map[string]string{"screenshot": "Super+F9"}, true
		}
		return nil, true
	})
	code, bound := r.bind(t, sessA, preferring("screenshot", "Take a screenshot", "CTRL+ALT+p"))
	if code != ResponseSuccess || !slices.Equal(triggers(bound), []string{"screenshot=Super+F9"}) {
		t.Fatalf("bind = %d %v", code, triggers(bound))
	}
	shown := editors(d)
	if len(shown) != 2 {
		t.Fatalf("showed %d editors, want the conflict shown once more", len(shown))
	}
	conflict := shown[1][0]
	if conflict.Trigger != "Ctrl+Alt+P" || !strings.Contains(conflict.Refusal, "already uses Ctrl+Alt+P") {
		t.Errorf("the conflict's row is %+v", conflict)
	}
	if got := f.bound.Load(); !maps.Equal(got, map[string]string{shortcutKey("org.app", "screenshot"): "Super+F9"}) {
		t.Errorf("bound %v", got)
	}
	f.press("Ctrl+Alt+P")
	if got := r.activations(t); len(got) != 0 {
		t.Errorf("the refused key activated %v", got)
	}
}

// Cancelling the editor once a conflict shows binds nothing, not even
// the shortcuts that took their keys.
func TestHotkeysCancellingAConflictBindsNothing(t *testing.T) {
	r, f, d, _ := hotkeysRig(t)
	f.held.Store([]string{"Ctrl+Alt+P"})
	r.shortcutSession(t, sessA)
	d.editor.Store(func(n int) (map[string]string, bool) { return nil, n == 0 })
	code, _ := r.bind(t, sessA, preferring("screenshot", "Shot", "CTRL+ALT+p"), preferring("record", "Record", "CTRL+ALT+r"))
	if code != ResponseCancelled {
		t.Fatalf("bind = %d, want cancelled", code)
	}
	if got := f.bound.Load(); len(got) != 0 {
		t.Errorf("a cancelled bind left %v bound", got)
	}
	if got := r.listShortcuts(t, sessA); len(got) != 0 {
		t.Errorf("a cancelled bind lists %v", triggers(got))
	}
}

// ConfigureShortcuts reopens the editor over the session's triggers;
// rebinding to Super+F9 emits ShortcutsChanged with the new trigger,
// and the old key no longer fires.
func TestHotkeysConfigureRebinds(t *testing.T) {
	r, f, d, store := hotkeysRig(t)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, preferring("screenshot", "Take a screenshot", "CTRL+ALT+p"))
	d.editor.Store(func(int) (map[string]string, bool) { return map[string]string{"screenshot": "Super+F9"}, true })
	if err := r.obj.Call(GlobalShortcutsIface+".ConfigureShortcuts", 0, sessA, "", Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	changed := r.shortcutsChanged(t)
	if len(changed) != 1 || changed[0].Body[0] != sessA {
		t.Fatalf("ShortcutsChanged = %v", changed)
	}
	var list []shortcut
	if err := dbus.Store(changed[0].Body[1:], &list); err != nil {
		t.Fatal(err)
	}
	if got := triggers(list); !slices.Equal(got, []string{"screenshot=Super+F9"}) {
		t.Errorf("ShortcutsChanged carries %v", got)
	}
	if shown := editors(d); len(shown) != 2 || shown[1][0].Trigger != "Ctrl+Alt+P" {
		t.Errorf("configure showed %+v, want the editor over the bound trigger", shown)
	}
	f.press("Ctrl+Alt+P")
	if got := r.activations(t); len(got) != 0 {
		t.Errorf("the old key still activated %v", got)
	}
	f.press("Super+F9")
	if got := r.activations(t); !slices.Equal(got, []string{"screenshot"}) {
		t.Errorf("the new key activated %v", got)
	}
	kept, err := permissions.ReadShortcuts(store)
	if err != nil || len(kept["org.app"]) != 1 || kept["org.app"][0].Trigger != "Super+F9" {
		t.Errorf("kept %v, %v", kept, err)
	}

	d.editor.Store(func(int) (map[string]string, bool) { return nil, false })
	if err := r.obj.Call(GlobalShortcutsIface+".ConfigureShortcuts", 0, sessA, "", Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(d.editors.Load()) == 3 })
	if changed := r.shortcutsChanged(t); len(changed) != 0 {
		t.Errorf("a configure closed unchanged emitted %v", changed)
	}
}

// A second session of the app binds its kept triggers with no dialog,
// and they fire.
func TestHotkeysASecondSessionNeedsNoDialog(t *testing.T) {
	r, f, d, _ := hotkeysRig(t)
	r.shortcutSession(t, sessA)
	d.editor.Store(func(int) (map[string]string, bool) { return map[string]string{"mute": ""}, true })
	r.bind(t, sessA, preferring("screenshot", "Shot", "CTRL+ALT+p"), preferring("mute", "Mute", "CTRL+ALT+m"))
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if got := f.bound.Load(); len(got) != 0 {
		t.Fatalf("closing the only session left %v bound", got)
	}
	asked := len(d.callers.Load())
	r.shortcutSession(t, sessB)
	if got := triggers(r.listShortcuts(t, sessB)); !slices.Equal(got, []string{"screenshot=Ctrl+Alt+P", "mute="}) {
		t.Errorf("a fresh session lists %v", got)
	}
	code, bound := r.bind(t, sessB, preferring("screenshot", "Shot", "CTRL+ALT+p"), preferring("mute", "Mute", "CTRL+ALT+m"))
	if code != ResponseSuccess || !slices.Equal(triggers(bound), []string{"screenshot=Ctrl+Alt+P", "mute="}) {
		t.Fatalf("bind = %d %v", code, triggers(bound))
	}
	if n := len(d.callers.Load()) - asked; n != 0 {
		t.Errorf("the second session asked %d dialogs, want 0", n)
	}
	f.press("Ctrl+Alt+P")
	f.press("Ctrl+Alt+M")
	if got := r.activations(t); !slices.Equal(got, []string{"screenshot"}) {
		t.Errorf("activated %v; the unassigned shortcut must not fire", got)
	}
}

// A kept trigger the compositor now refuses goes to the editor with
// the refusal instead of being lost.
func TestHotkeysAKeptTriggerTakenSinceGoesToTheEditor(t *testing.T) {
	r, f, d, _ := hotkeysRig(t)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, preferring("screenshot", "Shot", "CTRL+ALT+p"))
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	f.held.Store([]string{"Ctrl+Alt+P"})
	d.editor.Store(func(int) (map[string]string, bool) { return map[string]string{"screenshot": "Super+F9"}, true })
	r.shortcutSession(t, sessB)
	if code, bound := r.bind(t, sessB, preferring("screenshot", "Shot", "CTRL+ALT+p")); code != ResponseSuccess || !slices.Equal(triggers(bound), []string{"screenshot=Super+F9"}) {
		t.Fatalf("bind = %d %v", code, triggers(bound))
	}
	if shown := editors(d); len(shown) != 2 || shown[1][0].Refusal == "" {
		t.Errorf("editors %+v, want the taken trigger shown with its refusal", shown)
	}
}

// A binding the compositor withdraws is unassigned: the app hears
// ShortcutsChanged, and its next session does not commit the key
// again.
func TestHotkeysARevokedBindingIsUnassigned(t *testing.T) {
	r, f, d, store := hotkeysRig(t)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, preferring("screenshot", "Shot", "CTRL+ALT+p"))
	f.events.Load().revoked(shortcutKey("org.app", "screenshot"), "taken back by a bind")
	changed := r.shortcutsChanged(t)
	if len(changed) != 1 {
		t.Fatalf("ShortcutsChanged = %v", changed)
	}
	var list []shortcut
	if err := dbus.Store(changed[0].Body[1:], &list); err != nil {
		t.Fatal(err)
	}
	if got := triggers(list); !slices.Equal(got, []string{"screenshot="}) {
		t.Errorf("after the revocation %v", got)
	}
	kept, err := permissions.ReadShortcuts(store)
	if err != nil || kept["org.app"][0].Trigger != "" {
		t.Errorf("kept %v, %v", kept, err)
	}
	binds := f.binds.Load()
	asked := len(d.callers.Load())
	r.shortcutSession(t, sessB)
	r.bind(t, sessB, preferring("screenshot", "Shot", "CTRL+ALT+p"))
	if f.binds.Load() != binds || len(d.callers.Load()) != asked {
		t.Error("the revoked key was committed again behind the user's back")
	}
}

// Over hyprland-global-shortcuts the store keeps no triggers: its file
// reads exactly as before xx_hotkey_v1.
func TestConfigBindsStoreNoTrigger(t *testing.T) {
	f := &fakeShortcuts{}
	store := filepath.Join(t.TempDir(), "portal-shortcuts.json")
	r := newRig(t, func(b *Backend) {
		b.shortcuts = newGlobalShortcuts(b.conn, b.sessions, store, func(events shortcutEvents) (shortcutDevice, error) {
			f.onEvent.Store(events.pressed)
			return f, nil
		})
	})
	serveDialogs(t, r, true)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, preferring("mic", "Mic", "CTRL+M"))
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"org.app\": [\n    {\n      \"id\": \"mic\",\n      \"description\": \"Mic\"\n    }\n  ]\n}"; string(data) != want {
		t.Errorf("the store reads %q, want %q", data, want)
	}
}

// shortcutsChanged collects the ShortcutsChanged signals of a short
// wait.
func (r *rig) shortcutsChanged(t *testing.T) []*dbus.Signal {
	t.Helper()
	var out []*dbus.Signal
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case s := <-r.signals:
			if s.Name == GlobalShortcutsIface+".ShortcutsChanged" {
				out = append(out, s)
			}
		case <-deadline:
			return out
		}
	}
}
