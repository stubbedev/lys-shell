package bar

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/swaytest"
)

// walkKeymap numbers the keys a bar walk uses as evdev does.
const walkKeymap = `xkb_keymap {
xkb_keycodes "walk" { minimum = 8; maximum = 255; <ESC> = 9; <RTRN> = 36; <SPCE> = 65; <LEFT> = 113; <RGHT> = 114; };
xkb_types "walk" { type "ONE_LEVEL" { modifiers = none; level_name[Level1] = "Any"; }; };
xkb_compat "walk" { };
xkb_symbols "walk" {
  key <ESC> { [ Escape ] };
  key <RTRN> { [ Return ] };
  key <SPCE> { [ space ] };
  key <LEFT> { [ Left ] };
  key <RGHT> { [ Right ] };
};
};
`

const (
	walkEsc   = 1
	walkEnter = 28
	walkLeft  = 105
	walkRight = 106
)

// `panel focus` on a real compositor: the bar takes the keyboard and
// rings its first module, Right, Right and Enter open the third
// module's dropdown with the keyboard inside it and the ring on its
// first control at once, the first Esc closes the dropdown back to the bar and the second
// gives the keyboard back.
func TestHeadlessPanelFocusWalksTheModules(t *testing.T) {
	swaytest.Start(t)
	clocks := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "clock"}, {Module: "clock"}}}
	h := openHeadlessBar(t, config.Defaults(), clocks)
	buttons := h.buttons(3)
	ringed := func(want int) func() error {
		return func() error {
			var at []int
			h.onLoop(func() {
				for i, b := range buttons {
					if widget.FocusVisible(b) {
						at = append(at, i)
					}
				}
			})
			if want < 0 && len(at) == 0 || len(at) == 1 && at[0] == want {
				return nil
			}
			return fmt.Errorf("the ring is on modules %v, want %d", at, want)
		}
	}
	kb, err := vinput.Dial("", vinput.Options{Keyboard: true, FallbackKeymap: []byte(walkKeymap)})
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	tap := func(code uint32) {
		t.Helper()
		for _, step := range []error{kb.Key(code, true), kb.Key(code, false), kb.Roundtrip()} {
			if step != nil {
				t.Fatal(step)
			}
		}
	}

	if err := answerOnLoop(h.app, "focus the bar", func() error { return h.set.walk("", h.output.Name) }); err != nil {
		t.Fatal(err)
	}
	h.wait("the first module ringed", ringed(0))
	h.wait("the bar holding the keyboard", func() error {
		var held bool
		h.onLoop(func() { held = h.holdsKeyboard() })
		if !held {
			return errors.New("the compositor focuses another surface")
		}
		return nil
	})
	tap(walkRight)
	h.wait("Right on the second module", ringed(1))
	tap(walkRight)
	h.wait("Right again on the third", ringed(2))
	tap(walkLeft)
	h.wait("Left back on the second", ringed(1))
	tap(walkRight)
	h.wait("Right on the third once more", ringed(2))

	var inside widget.Widget
	focusedInside := make(chan bool, 4)
	h.onLoop(func() {
		h.rt.ctx.Dropdowns.mu.Lock()
		build := h.rt.ctx.Dropdowns.builders["calendar"]
		h.rt.ctx.Dropdowns.mu.Unlock()
		content := h.rt.ctx.Dropdowns.instance("calendar", build)
		inside = widget.FirstFocusable(content)
		if inside != nil {
			inside.(interface{ SetOnFocusChanged(func(bool)) }).SetOnFocusChanged(func(on bool) { focusedInside <- on })
		}
	})
	if inside == nil {
		t.Fatal("the calendar has nothing the keyboard can focus")
	}
	tap(walkEnter)
	h.wait("Enter opening the third module's calendar", func() error {
		var name string
		var checked []bool
		h.onLoop(func() {
			h.rt.ctx.Dropdowns.mu.Lock()
			name = h.rt.ctx.Dropdowns.openName[h.output.Name]
			h.rt.ctx.Dropdowns.mu.Unlock()
			for _, b := range buttons {
				checked = append(checked, b.HasState(widget.StateChecked))
			}
		})
		if name != "calendar" || checked[0] || checked[1] || !checked[2] {
			return fmt.Errorf("open %q, buttons checked %v", name, checked)
		}
		return nil
	})
	select {
	case on := <-focusedInside:
		if !on {
			t.Fatal("the calendar's first control lost the focus it never had")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the calendar opened without the keyboard inside it")
	}
	h.wait("the ring on the calendar's first control before any Tab", func() error {
		var ringed bool
		h.onLoop(func() { ringed = widget.FocusVisible(inside) })
		if !ringed {
			return errors.New("the focused control shows no ring")
		}
		return nil
	})

	tap(walkEsc)
	h.wait("the first Esc closing the calendar", func() error {
		var open bool
		h.onLoop(func() {
			h.rt.ctx.Dropdowns.mu.Lock()
			pop := h.rt.ctx.Dropdowns.openPop[h.output.Name]
			h.rt.ctx.Dropdowns.mu.Unlock()
			open = pop != nil && !pop.Closed()
		})
		if open {
			return errors.New("the calendar is still open")
		}
		return nil
	})
	if err := ringed(2)(); err != nil {
		t.Errorf("back on the bar: %v", err)
	}
	tap(walkEsc)
	h.wait("the second Esc giving the keyboard back", func() error {
		var walking bool
		var mode app.KeyboardMode
		h.onLoop(func() { walking, mode = h.bar.layer.Walking(), h.bar.layer.KeyboardMode() })
		if walking || mode != app.KeyboardNone {
			return fmt.Errorf("walking %v in keyboard mode %v", walking, mode)
		}
		return ringed(-1)()
	})
}
