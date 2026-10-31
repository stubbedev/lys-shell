package filechooser

import (
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The chooser's keys on a real compositor, read through the keymap the
// keyboard sent: Ctrl with the key carrying h shows hidden files and
// with the one carrying l opens the location entry, each on the code a
// physical keyboard gives the other letter; Escape cancels from the
// code a physical keyboard gives Return, and that keymap's Return, on
// a physical keyboard's Escape code, does not. The chooser is closed
// once the loop has ended, as Close requires.
func TestHeadlessKeysFollowTheKeymap(t *testing.T) {
	var c *Chooser
	t.Cleanup(func() {
		if c != nil {
			c.Close()
		}
	})
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	home := t.TempDir()
	c = New(Deps{
		Config:    func() *config.Config { return cfg },
		Open:      func(lc app.LayerConfig) (Window, error) { return a.NewLayer(lc) },
		Invoke:    a.Invoke,
		KeySym:    a.KeySym,
		Font:      treetest.Font(t),
		MIME:      testDB(t),
		Home:      home,
		Places:    func() []Place { return []Place{{"Home", home, "user-home-symbolic"}} },
		Locations: func() []Place { return nil },
	})
	const handle = "/r/headless"
	t.Cleanup(func() { c.Dismiss(handle) })
	answers := make(chan Result, 1)
	go func() {
		answers <- c.Open(portaldialogs.Caller{Handle: handle, Modal: true}, OpenRequest{CurrentFolder: home})
	}()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)

	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)

	kb.CtrlTap(keytest.H)
	apptest.Until(t, a, "Ctrl+H showed hidden files", func() bool { return c.hidden })
	if apptest.On(a, c.ui.location.Visible) {
		t.Fatal("Ctrl+H, on a physical keyboard's L, opened the location entry")
	}
	kb.CtrlTap(keytest.L)
	apptest.Until(t, a, "Ctrl+L opened the location entry", func() bool { return c.ui.location.Visible() })

	kb.Tap(keytest.Escape)
	if r := apptest.Answer(t, "the chooser after Escape", answers); r.URIs != nil {
		t.Fatalf("Escape answered %v, want a cancel", r.URIs)
	}
}
