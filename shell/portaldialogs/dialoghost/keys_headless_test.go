package dialoghost

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Escape denies an access dialog on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the dialog open.
func TestHeadlessEscapeDeniesByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	h := New(Deps{
		Config:   func() *config.Config { return cfg },
		Open:     func(c app.LayerConfig) (Window, error) { return a.NewLayer(c) },
		Invoke:   a.Invoke,
		KeySym:   a.KeySym,
		Font:     treetest.Font(t),
		Describe: func(string) (string, string) { return "App", "" },
	})
	const handle = "/r/headless"
	t.Cleanup(func() { h.Dismiss(handle) })
	answers := make(chan bool, 1)
	go func() {
		ok, _ := h.Access(portaldialogs.Caller{Handle: handle}, portaldialogs.AccessRequest{Title: "Allow?", GrantLabel: "Allow", DenyLabel: "Deny"})
		answers <- ok
	}()
	kb := keytest.Dial(t)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if ok := apptest.Answer(t, "the dialog after Escape", answers); ok {
		t.Fatal("Escape granted the access")
	}
}
