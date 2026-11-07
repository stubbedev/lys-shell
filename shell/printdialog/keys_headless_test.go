package printdialog

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Escape cancels the print dialog on a real compositor by the symbol
// the keyboard's keymap gives it; the code a physical keyboard gives
// Escape, Return in that keymap, leaves the dialog open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	h := headlessDialog(t, a)
	const handle = "/r/headless"
	t.Cleanup(func() { h.Dismiss(handle) })
	answers := make(chan bool, 1)
	go func() {
		ok, _, _, _ := h.Prepare(portaldialogs.Caller{Handle: handle}, PrepareRequest{Title: "Doc"})
		answers <- ok
	}()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if ok := apptest.Answer(t, "the dialog after Escape", answers); ok {
		t.Fatal("Escape confirmed the print")
	}
}

// headlessDialog is a print dialog on a's compositor over one idle
// printer, with animations off.
func headlessDialog(t *testing.T, a *app.Application) *Dialog {
	t.Helper()
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	return New(Deps{
		Config:       func() *config.Config { return cfg },
		Open:         func(s dialogsurface.Spec) (Window, error) { return dialogsurface.Open(a, s) },
		Invoke:       a.Invoke,
		KeySym:       a.KeySym,
		Font:         treetest.Font(t),
		CUPS:         &fakeScheduler{printers: []ipp.Printer{{Name: "office", State: ipp.StateIdle, Accepting: true}}},
		RefreshEvery: time.Hour,
		PickOutput:   func(string, func(string)) {},
		Home:         t.TempDir(),
		User:         "u",
	})
}
