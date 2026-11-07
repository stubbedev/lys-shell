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
	"github.com/stubbedev/lys-shell/shell/treetest"
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
// first control at once, the first Esc closes
// the dropdown back to the bar and the second gives the keyboard back.
func TestHeadlessPanelFocusWalksTheModules(t *testing.T) {
	swaytest.Start(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	application := app.NewApplication(sess)
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.App = application
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	rt.ctx.Dropdowns = newDropdownRegistry(application, cfg, rt.font, &rt.style, rt.ctx)
	host := &barHost{app: application, rt: rt}
	var bar *barWindow
	set := newSurfaceSet(func(o *app.Output, l config.BarLayout) (surfaceLayer[config.BarLayout], error) {
		layer, err := host.open(o, l)
		if err == nil {
			bar = layer.(*barWindow)
		}
		return layer, err
	})
	outputs := sess.Outputs()
	if len(outputs) == 0 || outputs[0].Name == "" {
		t.Fatalf("the headless sway shows outputs %v", outputs)
	}
	output := outputs[0]
	clocks := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "clock"}, {Module: "clock"}}}
	set.plug(output, clocks, true)
	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	defer func() {
		application.Invoke(application.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
	}()
	onLoop := func(fn func()) {
		ran := make(chan struct{})
		application.Invoke(func() {
			fn()
			close(ran)
		})
		<-ran
	}
	wait := func(what string, cond func() error) {
		t.Helper()
		var last error
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if last = cond(); last == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %v", what, last)
			}
		}
	}
	var buttons []*barButton
	wait("the three clocks", func() error {
		buttons = nil
		onLoop(func() {
			if bar == nil {
				return
			}
			for _, w := range treetest.WithClass(bar.bar, "bar-button") {
				if b, ok := w.(*barButton); ok {
					buttons = append(buttons, b)
				}
			}
		})
		if len(buttons) != 3 {
			return fmt.Errorf("%d module buttons", len(buttons))
		}
		return nil
	})
	ringed := func(want int) func() error {
		return func() error {
			var at []int
			onLoop(func() {
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

	if err := answerOnLoop(application, "focus the bar", func() error { return set.walk("", output.Name) }); err != nil {
		t.Fatal(err)
	}
	wait("the first module ringed", ringed(0))
	wait("the bar holding the keyboard", func() error {
		var held bool
		onLoop(func() { held = sess.KeyboardFocus() == bar.layer.HostSurface() })
		if !held {
			return errors.New("the compositor focuses another surface")
		}
		return nil
	})
	tap(walkRight)
	wait("Right on the second module", ringed(1))
	tap(walkRight)
	wait("Right again on the third", ringed(2))
	tap(walkLeft)
	wait("Left back on the second", ringed(1))
	tap(walkRight)
	wait("Right on the third once more", ringed(2))

	var inside widget.Widget
	focusedInside := make(chan bool, 4)
	onLoop(func() {
		rt.ctx.Dropdowns.mu.Lock()
		build := rt.ctx.Dropdowns.builders["calendar"]
		rt.ctx.Dropdowns.mu.Unlock()
		content := rt.ctx.Dropdowns.instance("calendar", build)
		inside = widget.FirstFocusable(content)
		if inside != nil {
			inside.(interface{ SetOnFocusChanged(func(bool)) }).SetOnFocusChanged(func(on bool) { focusedInside <- on })
		}
	})
	if inside == nil {
		t.Fatal("the calendar has nothing the keyboard can focus")
	}
	tap(walkEnter)
	wait("Enter opening the third module's calendar", func() error {
		var name string
		var checked []bool
		onLoop(func() {
			rt.ctx.Dropdowns.mu.Lock()
			name = rt.ctx.Dropdowns.openName[output.Name]
			rt.ctx.Dropdowns.mu.Unlock()
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
	wait("the ring on the calendar's first control before any Tab", func() error {
		var ringed bool
		onLoop(func() { ringed = widget.FocusVisible(inside) })
		if !ringed {
			return errors.New("the focused control shows no ring")
		}
		return nil
	})

	tap(walkEsc)
	wait("the first Esc closing the calendar", func() error {
		var open bool
		onLoop(func() {
			rt.ctx.Dropdowns.mu.Lock()
			pop := rt.ctx.Dropdowns.openPop[output.Name]
			rt.ctx.Dropdowns.mu.Unlock()
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
	wait("the second Esc giving the keyboard back", func() error {
		var walking bool
		var mode app.KeyboardMode
		onLoop(func() { walking, mode = bar.layer.Walking(), bar.layer.KeyboardMode() })
		if walking || mode != app.KeyboardNone {
			return fmt.Errorf("walking %v in keyboard mode %v", walking, mode)
		}
		return ringed(-1)()
	})
}
