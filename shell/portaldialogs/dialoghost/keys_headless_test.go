package dialoghost

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
)

// Escape denies an access dialog on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the dialog open.
func TestHeadlessEscapeDeniesByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	h := headlessDialogs(t, a)
	const handle = "/r/headless"
	t.Cleanup(func() { h.Dismiss(handle) })
	answers := make(chan bool, 1)
	go func() {
		ok, _ := h.Access(portaldialogs.Caller{Handle: handle}, portaldialogs.AccessRequest{Title: "Allow?", GrantLabel: "Allow", DenyLabel: "Deny"})
		answers <- ok
	}()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if ok := apptest.Answer(t, "the dialog after Escape", answers); ok {
		t.Fatal("Escape granted the access")
	}
}

// headlessDialogs is the portal dialogs on a's compositor, with
// animations off.
func headlessDialogs(t *testing.T, a *app.Application) *Dialogs {
	t.Helper()
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	return New(Deps{
		Config:   func() *config.Config { return cfg },
		Open:     func(s dialogsurface.Spec) (Window, error) { return dialogsurface.Open(a, s) },
		Invoke:   a.Invoke,
		KeySym:   a.KeySym,
		Font:     treetest.Font(t),
		Describe: func(string) (string, string) { return "App", "" },
	})
}

// The shortcuts editor records a combination from a real keyboard on a
// real compositor: Return on the focused recorder starts recording,
// Ctrl+Alt+L is taken whole by the recorder rather than the dialog, and
// Save answers it.
func TestHeadlessShortcutsEditorRecordsAKeyboardChord(t *testing.T) {
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	opened := make(chan widget.Widget, 1)
	h := New(Deps{
		Config: func() *config.Config { return cfg },
		Open: func(s dialogsurface.Spec) (Window, error) {
			build := s.Root
			s.Root = func(surface dialogsurface.Surface) widget.Widget {
				root := build(surface)
				opened <- root
				return root
			}
			return dialogsurface.Open(a, s)
		},
		Invoke:   a.Invoke,
		KeySym:   a.KeySym,
		Font:     treetest.Font(t),
		Describe: func(string) (string, string) { return "App", "" },
	})
	const handle = "/r/headless-keys"
	t.Cleanup(func() { h.Dismiss(handle) })
	type answer struct {
		ok       bool
		triggers []portaldialogs.ShortcutTrigger
	}
	answers := make(chan answer, 1)
	go func() {
		ok, triggers := h.BindShortcuts(portaldialogs.Caller{Handle: handle}, portaldialogs.ShortcutsRequest{
			Title: "Global shortcuts", Rows: []portaldialogs.ShortcutRow{{ID: "shot", Description: "Take a screenshot", Trigger: "Ctrl+Alt+P"}},
		})
		answers <- answer{ok, triggers}
	}()
	root := apptest.Answer(t, "the editor opening", opened)
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	var rec *widget.ShortcutRecorder
	apptest.Until(t, a, "the recorder", func() bool {
		recs := treetest.All[*widget.ShortcutRecorder](root)
		if len(recs) == 1 {
			rec = recs[0]
		}
		return rec != nil
	})
	kb.Tap(keytest.Return)
	apptest.Until(t, a, "recording after Return", rec.Recording)
	if err := kb.Modifiers(ctrlAltMask, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	kb.Tap(keytest.L)
	if err := kb.Modifiers(0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	apptest.Until(t, a, "the chord recorded", func() bool { return !rec.Recording() && rec.Accel() == "Ctrl+Alt+L" })
	apptest.Never(t, "the dialog answering on the chord", answers)
	a.Invoke(func() { treetest.Button(t, root, "Save").OnClick() })
	got := apptest.Answer(t, "the editor after Save", answers)
	if want := []portaldialogs.ShortcutTrigger{{ID: "shot", Trigger: "Ctrl+Alt+L"}}; !got.ok || !slices.Equal(got.triggers, want) {
		t.Errorf("saved %+v, want %+v", got, want)
	}
}

// ctrlAltMask is the Control and Mod1 (Alt) real modifiers.
const ctrlAltMask = 1<<2 | 1<<3
