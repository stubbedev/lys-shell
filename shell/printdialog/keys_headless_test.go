package printdialog

import (
	"context"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Escape cancels the print dialog on a real compositor by the symbol
// the keyboard's keymap gives it; the code a physical keyboard gives
// Escape, Return in that keymap, leaves the dialog open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	h := New(Deps{
		Config: func() *config.Config { return cfg },
		Open:   func(c app.LayerConfig) (Window, error) { return a.NewLayer(c) },
		Invoke: a.Invoke,
		KeySym: a.KeySym,
		Font:   treetest.Font(t),
		Printers: func(context.Context) ([]ipp.Printer, error) {
			return []ipp.Printer{{Name: "office", State: ipp.StateIdle, Accepting: true}}, nil
		},
		Spool:      func(context.Context, ipp.Job) error { return nil },
		PickOutput: func(string, func(string)) {},
		Home:       t.TempDir(),
		User:       "u",
	})
	const handle = "/r/headless"
	t.Cleanup(func() { h.Dismiss(handle) })
	answers := make(chan bool, 1)
	go func() {
		ok, _, _, _ := h.Prepare(portaldialogs.Caller{Handle: handle}, PrepareRequest{Title: "Doc"})
		answers <- ok
	}()
	kb := keytest.Dial(t)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if ok := apptest.Answer(t, "the dialog after Escape", answers); ok {
		t.Fatal("Escape confirmed the print")
	}
}
