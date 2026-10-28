package portal

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// fakeShortcuts is the compositor's shortcut manager.
type fakeShortcuts struct {
	onEvent      dbustest.Var[func(string, bool)]
	registered   dbustest.Var[[]string]
	unregistered dbustest.Var[[]string]
}

func (f *fakeShortcuts) register(key, id, appID, description, trigger string) (func(), error) {
	f.registered.Update(func(r []string) []string { return append(r, appID+"/"+id+"|"+description+"|"+trigger) })
	return func() {
		f.unregistered.Update(func(u []string) []string { return append(u, key) })
	}, nil
}

// shortcutsRig keeps the shortcuts store in a temporary file, the
// clock at 1234 ms, and a consenting shell.
func shortcutsRig(t *testing.T, startErr error) (*rig, *fakeShortcuts, *fakeDialogs) {
	t.Helper()
	f := &fakeShortcuts{}
	store := filepath.Join(t.TempDir(), "portal-shortcuts.json")
	r := newRig(t, func(b *Backend) {
		b.shortcuts = newGlobalShortcuts(b.conn, b.sessions, store, func(on func(string, bool)) (shortcutDevice, error) {
			if startErr != nil {
				return nil, startErr
			}
			f.onEvent.Store(on)
			return f, nil
		})
		b.shortcuts.now = func() uint64 { return 1234 }
	})
	return r, f, serveDialogs(t, r, true)
}

func props(desc string) Vardict { return Vardict{"description": dbus.MakeVariant(desc)} }

func (r *rig) shortcutSession(t *testing.T, session dbus.ObjectPath) {
	t.Helper()
	r.shortcutSessionAs(t, session, "org.app")
}

// shortcutSessionAs opens a GlobalShortcuts session for appID, "" for
// a caller the frontend could not identify.
func (r *rig) shortcutSessionAs(t *testing.T, session dbus.ObjectPath, appID string) {
	t.Helper()
	if code, _ := r.interactive(t, GlobalShortcutsIface+".CreateSession", handle, session, appID, Vardict{}); code != ResponseSuccess {
		t.Fatalf("CreateSession = %d", code)
	}
}

func (r *rig) bind(t *testing.T, session dbus.ObjectPath, shortcuts ...shortcut) (uint32, []shortcut) {
	t.Helper()
	code, res := r.interactive(t, GlobalShortcutsIface+".BindShortcuts", handle, session, shortcuts, "", Vardict{})
	var bound []shortcut
	if v, ok := res["shortcuts"]; ok {
		if err := dbus.Store([]any{v.Value()}, &bound); err != nil {
			t.Fatal(err)
		}
	}
	return code, bound
}

func (r *rig) listShortcuts(t *testing.T, session dbus.ObjectPath) []shortcut {
	t.Helper()
	_, res := r.interactive(t, GlobalShortcutsIface+".ListShortcuts", handle, session)
	var got []shortcut
	if err := dbus.Store([]any{res["shortcuts"].Value()}, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func shortcutIDs(list []shortcut) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID+"="+stringOr(s.Props, "description", ""))
	}
	return ids
}

// shortcutSignals collects the Activated / Deactivated signals of a
// short wait.
func (r *rig) shortcutSignals(t *testing.T) []*dbus.Signal {
	t.Helper()
	var out []*dbus.Signal
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case s := <-r.signals:
			if s.Name == GlobalShortcutsIface+".Activated" || s.Name == GlobalShortcutsIface+".Deactivated" {
				out = append(out, s)
			}
		case <-deadline:
			return out
		}
	}
}

// A bind asks the user, registers the shortcuts, and replies them with
// a trigger description and without the app's preferred trigger; their
// presses and releases come back with a millisecond timestamp.
func TestGlobalShortcutsBindAndActivate(t *testing.T) {
	r, f, d := shortcutsRig(t, nil)
	r.shortcutSession(t, sessA)
	mic := shortcut{"mic", Vardict{"description": dbus.MakeVariant("Toggle mic"), "preferred_trigger": dbus.MakeVariant("CTRL+M")}}
	code, bound := r.bind(t, sessA, mic)
	if code != ResponseSuccess || !slices.Equal(shortcutIDs(bound), []string{"mic=Toggle mic"}) {
		t.Fatalf("bind = %d %v", code, bound)
	}
	if _, has := bound[0].Props["preferred_trigger"]; has || !strings.Contains(stringOr(bound[0].Props, "trigger_description", ""), "global, org.app:mic") {
		t.Errorf("bound props %v", bound[0].Props)
	}
	if got := f.registered.Load(); !slices.Equal(got, []string{"org.app/mic|Toggle mic|CTRL+M"}) {
		t.Errorf("registered %v", got)
	}
	if asked := d.asked.Load(); len(asked) == 0 || !strings.Contains(asked[0].(portaldialogs.AccessRequest).Body, "Toggle mic") {
		t.Errorf("consent asked %v", asked)
	}

	// A re-bind of mic updates its props without a second registration.
	if _, bound = r.bind(t, sessA, shortcut{"mic", props("Mute")}, shortcut{"next", props("Next")}); !slices.Equal(shortcutIDs(bound), []string{"mic=Mute", "next=Next"}) {
		t.Errorf("bound %v", bound)
	}
	if got := f.registered.Load(); len(got) != 2 || got[1] != "org.app/next|Next|" {
		t.Errorf("registered %v", got)
	}
	if got := shortcutIDs(r.listShortcuts(t, sessA)); !slices.Equal(got, []string{"mic=Mute", "next=Next"}) {
		t.Errorf("listed %v", got)
	}

	on := f.onEvent.Load()
	on(shortcutKey("org.app", "next"), true)
	on(shortcutKey("org.app", "next"), false)
	on(shortcutKey("org.other", "next"), true)
	signals := r.shortcutSignals(t)
	if len(signals) != 2 || signals[0].Name != GlobalShortcutsIface+".Activated" || signals[0].Body[0] != sessA || signals[0].Body[1] != "next" ||
		signals[0].Body[2] != uint64(1234) || signals[1].Name != GlobalShortcutsIface+".Deactivated" {
		t.Fatalf("signals = %v", signals)
	}

	// Closing the session unregisters its shortcuts; their keys go quiet.
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if got := f.unregistered.Load(); len(got) != 2 {
		t.Errorf("unregistered %v", got)
	}
	on(shortcutKey("org.app", "mic"), true)
	if s := r.shortcutSignals(t); len(s) != 0 {
		t.Errorf("a closed session's key signalled %v", s)
	}
	if code, _ := r.interactive(t, GlobalShortcutsIface+".ListShortcuts", handle, sessA); code != ResponseOther {
		t.Errorf("a closed session's ListShortcuts = %d, want other", code)
	}
	if code, _ := r.bind(t, sessA, shortcut{"x", props("X")}); code != ResponseOther {
		t.Errorf("a closed session's BindShortcuts = %d, want other", code)
	}
}

// Two sessions of one app share the compositor's registration: both
// hear its presses, and closing one leaves the other bound.
func TestGlobalShortcutsTwoSessionsOfOneApp(t *testing.T) {
	r, f, _ := shortcutsRig(t, nil)
	r.shortcutSession(t, sessA)
	r.shortcutSession(t, sessB)
	r.bind(t, sessA, shortcut{"mic", props("Mic")})
	r.bind(t, sessB, shortcut{"mic", props("Mic")})
	if got := f.registered.Load(); len(got) != 1 {
		t.Fatalf("registered %v, want one shared registration", got)
	}
	f.onEvent.Load()(shortcutKey("org.app", "mic"), true)
	var heard []dbus.ObjectPath
	for _, s := range r.shortcutSignals(t) {
		heard = append(heard, s.Body[0].(dbus.ObjectPath))
	}
	slices.Sort(heard)
	if !slices.Equal(heard, []dbus.ObjectPath{sessA, sessB}) {
		t.Errorf("heard by %v, want both sessions", heard)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if got := f.unregistered.Load(); len(got) != 0 {
		t.Errorf("closing one session unregistered %v", got)
	}
	f.onEvent.Load()(shortcutKey("org.app", "mic"), true)
	if s := r.shortcutSignals(t); len(s) != 1 || s[0].Body[0] != sessB {
		t.Errorf("after one closed: %v", s)
	}
}

// Shortcuts bound once are listed for the app's next session, and
// binding them again does not ask again; a new one does.
func TestGlobalShortcutsRememberTheBinds(t *testing.T) {
	r, _, d := shortcutsRig(t, nil)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, shortcut{"mic", props("Mic")})
	r.shortcutSession(t, sessB)
	if got := shortcutIDs(r.listShortcuts(t, sessB)); !slices.Equal(got, []string{"mic=Mic"}) {
		t.Errorf("a fresh session lists %v, want the earlier bind", got)
	}
	asked := len(d.callers.Load())
	if code, _ := r.bind(t, sessB, shortcut{"mic", props("Mic")}); code != ResponseSuccess || len(d.callers.Load()) != asked {
		t.Errorf("re-binding a known shortcut = %d, asked %d more times", code, len(d.callers.Load())-asked)
	}
	r.bind(t, sessB, shortcut{"next", props("Next")})
	if len(d.callers.Load()) != asked+1 {
		t.Error("a new shortcut was bound without asking")
	}
	d.yes.Store(false)
	r.shortcutSession(t, sessC)
	if code, _ := r.bind(t, sessC, shortcut{"other", props("Other")}); code != ResponseCancelled {
		t.Errorf("a denied bind = %d, want cancelled", code)
	}
}

// Callers the frontend could not identify (app id "") share nothing:
// each session registers its shortcuts under a name of its own and
// hears only its own presses, every bind asks and says the shortcuts
// will not be kept, and none are remembered for a later session. An
// identified app is untouched by them.
func TestGlobalShortcutsUnidentifiedAppsShareNothing(t *testing.T) {
	r, f, d := shortcutsRig(t, nil)
	r.shortcutSessionAs(t, sessA, "")
	r.shortcutSessionAs(t, sessB, "")
	if code, bound := r.bind(t, sessA, shortcut{"toggle", props("A")}); code != ResponseSuccess ||
		len(bound) != 1 || !strings.Contains(stringOr(bound[0].Props, "trigger_description", ""), "global, unidentified-1_2-a:toggle") {
		t.Fatalf("bind = %d %v", code, bound)
	}
	if body := d.asked.Load()[0].(portaldialogs.AccessRequest).Body; !strings.Contains(body, unidentifiedNote) {
		t.Errorf("an unidentified app's consent says %q", body)
	}
	r.bind(t, sessB, shortcut{"toggle", props("B")})
	if got := f.registered.Load(); !slices.Equal(got, []string{"unidentified-1_2-a/toggle|A|", "unidentified-1_2-b/toggle|B|"}) {
		t.Errorf("registered %v, want one per session", got)
	}
	if asked := len(d.callers.Load()); asked != 2 {
		t.Errorf("asked %d times, want each unidentified bind asked", asked)
	}
	f.onEvent.Load()(shortcutKey("unidentified-1_2-a", "toggle"), true)
	if s := r.shortcutSignals(t); len(s) != 1 || s[0].Body[0] != sessA {
		t.Errorf("a press of A's shortcut signalled %v", s)
	}

	r.shortcutSessionAs(t, sessC, "")
	if got := r.listShortcuts(t, sessC); len(got) != 0 {
		t.Errorf("a later unidentified session lists %v", shortcutIDs(got))
	}
	r.bind(t, sessC, shortcut{"toggle", props("C")})
	if asked := len(d.callers.Load()); asked != 3 {
		t.Error("an unidentified bind was taken as allowed by an earlier one")
	}
	if all, err := r.backend.shortcuts.stored(); err != nil || len(all) != 0 {
		t.Errorf("stored %v, %v: unidentified shortcuts were kept", all, err)
	}
}

// Without the protocol nothing can be bound: the bind fails.
func TestGlobalShortcutsWithoutTheProtocol(t *testing.T) {
	r, _, _ := shortcutsRig(t, errors.New("no hyprland_global_shortcuts_manager_v1"))
	r.shortcutSession(t, sessA)
	if code, bound := r.bind(t, sessA, shortcut{"mic", props("Mic")}); code != ResponseOther || len(bound) != 0 {
		t.Errorf("bind = %d %v, want other", code, bound)
	}
	if got := r.listShortcuts(t, sessA); len(got) != 0 {
		t.Errorf("listed %v after a failed bind", got)
	}
}

// ConfigureShortcuts shows the session's shortcuts and returns at once;
// an unknown session fails.
func TestGlobalShortcutsConfigure(t *testing.T) {
	r, _, d := shortcutsRig(t, nil)
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, shortcut{"mic", props("Mic")})
	if err := r.obj.Call(GlobalShortcutsIface+".ConfigureShortcuts", 0, sessA, "", Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(d.callers.Load()) == 2 })
	if body := d.asked.Load()[0].(portaldialogs.AccessRequest).Body; !strings.Contains(body, "Mic") || !strings.Contains(body, "global, org.app:mic") {
		t.Errorf("configure shows %q", body)
	}
	if err := r.obj.Call(GlobalShortcutsIface+".ConfigureShortcuts", 0, sessC, "", Vardict{}).Err; err == nil {
		t.Error("ConfigureShortcuts on an unknown session succeeded")
	}
	if v, err := r.obj.GetProperty(GlobalShortcutsIface + ".version"); err != nil || v.Value() != uint32(2) {
		t.Errorf("version = %v, %v", v, err)
	}
}

func TestShortcutKeySeparates(t *testing.T) {
	if shortcutKey("a", "b") == shortcutKey("ab", "") {
		t.Error("distinct pairs share a key")
	}
}

// When the compositor connection ends, the registrations die with it:
// every GlobalShortcuts session closes, and nothing is unregistered on
// the dead connection.
func TestGlobalShortcutsCloseWhenTheLoopEnds(t *testing.T) {
	r, f, _ := shortcutsRig(t, nil)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(SessionIface)); err != nil {
		t.Fatal(err)
	}
	r.shortcutSession(t, sessA)
	r.bind(t, sessA, shortcut{"mic", props("Mic")})
	r.backend.shortcuts.loopEnded()
	r.waitClosed(t, sessA)
	if got := f.unregistered.Load(); len(got) != 0 {
		t.Errorf("unregistered %v on a dead connection", got)
	}
	r.shortcutSession(t, sessB)
	r.bind(t, sessB, shortcut{"mic", props("Mic")})
	if got := f.registered.Load(); len(got) != 2 {
		t.Errorf("registered %v: the next session did not reach a fresh device", got)
	}
}

// waitFor polls cond until it holds, failing after two seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
	}
}
