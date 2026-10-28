package sharepicker

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Escape cancels the picker on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the picker open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	a := keytest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	p := New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, nil)
	p.windows = func() []sharepreview.Toplevel { return nil }
	p.outputs = func() []outputInfo { return []outputInfo{{name: "HEADLESS-1", width: 1920, height: 1080, scale: 1}} }
	t.Cleanup(func() { p.Dismiss("/r/headless") })
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/headless"}, Request{Sources: AllSources}) }()
	kb := keytest.Dial(t)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	keytest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if got := keytest.Answer(t, "the picker after Escape", answers); got != "" {
		t.Fatalf("Escape answered %q, want a cancel", got)
	}
}
