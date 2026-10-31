package regionoverlay

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
)

const buttonLeft = 0x110

// startOverlay runs the overlay on a private headless sway and opens a
// request; the answer arrives on the returned channel.
func startOverlay(t *testing.T) (*keytest.Keyboard, chan result) {
	t.Helper()
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	o := New(a, a.Session().Outputs, nil, font, widget.NewStylesheet("", 0))
	t.Cleanup(o.Cancel)
	answers := make(chan result, 1)
	go func() {
		sel, ok := o.Request(nil)
		answers <- result{sel: sel, ok: ok}
	}()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	return kb, answers
}

// dragOn presses at (x0, y0) and releases at (x1, y1) on the output.
func dragOn(t *testing.T, kb *keytest.Keyboard, x0, y0, x1, y1 uint32) {
	t.Helper()
	w, h := kb.OutputSize()
	steps := []func() error{
		func() error { return kb.MotionAbsolute(x0, y0, uint32(w), uint32(h)) },
		func() error { return kb.Button(buttonLeft, true) },
		func() error { return kb.MotionAbsolute((x0+x1)/2, (y0+y1)/2, uint32(w), uint32(h)) },
		func() error { return kb.MotionAbsolute(x1, y1, uint32(w), uint32(h)) },
		func() error { return kb.Button(buttonLeft, false) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Enter confirms a dragged region on a real compositor, read through the
// keymap the keyboard sent: the Return of a keymap like wtype's, on a
// code a physical keyboard gives Escape.
func TestHeadlessEnterConfirmsTheDraggedRegion(t *testing.T) {
	kb, answers := startOverlay(t)
	dragOn(t, kb, 100, 100, 300, 200)
	kb.Tap(keytest.Return)
	r := apptest.Answer(t, "the region after Return", answers)
	if !r.ok || r.sel.Width != 200 || r.sel.Height != 100 || r.sel.X != 100 || r.sel.Y != 100 {
		t.Fatalf("answer = %+v, want the 200x100 region at 100,100 confirmed", r)
	}
}

// Escape cancels, read the same way: the code a physical keyboard gives
// Return carries Escape in this keymap, and cancels.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	kb, answers := startOverlay(t)
	dragOn(t, kb, 100, 100, 300, 200)
	kb.Tap(keytest.Escape)
	if r := apptest.Answer(t, "the region after Escape", answers); r.ok {
		t.Fatalf("Escape confirmed %+v", r.sel)
	}
}
