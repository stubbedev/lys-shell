package regionoverlay

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/swaytest"
)

// remappedKeymap is a keymap of the kind wtype uploads: the keys it
// types on codes of its own. Return sits on evdev 1 (Escape on a real
// keyboard) and Escape on evdev 28 (Return on a real keyboard).
const remappedKeymap = `xkb_keymap {
	xkb_keycodes "remapped" { minimum = 8; maximum = 255; <K1> = 9; <K28> = 36; };
	xkb_types { include "complete" };
	xkb_compat { include "complete" };
	xkb_symbols "remapped" { key <K1> { [ Return ] }; key <K28> { [ Escape ] }; };
};
`

const (
	evdevOne         = 1
	evdevTwentyEight = 28
	buttonLeft       = 0x110
)

// startOverlay runs the overlay on a private headless sway and opens a
// request; the answer arrives on the returned channel.
func startOverlay(t *testing.T) (*vinput.Device, chan result) {
	t.Helper()
	swaytest.Start(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	a := app.NewApplication(sess)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	o := New(a, sess.Outputs, nil, font, widget.NewStylesheet("", 0))
	a.Hold()
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() {
		o.Cancel()
		a.Invoke(a.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
	})
	answers := make(chan result, 1)
	go func() {
		sel, ok := o.Request(nil)
		answers <- result{sel: sel, ok: ok}
	}()
	dev, err := vinput.Dial("", vinput.Options{Keyboard: true, FallbackKeymap: []byte(remappedKeymap)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	mapped := make(chan bool, 1)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		a.Invoke(func() { mapped <- len(o.surfaces) > 0 })
		if <-mapped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the overlay never mapped a surface")
		}
	}
	return dev, answers
}

// drag presses at (x0, y0) and releases at (x1, y1) on the output.
func dragOn(t *testing.T, dev *vinput.Device, x0, y0, x1, y1 uint32) {
	t.Helper()
	w, h := dev.OutputSize()
	steps := []func() error{
		func() error { return dev.MotionAbsolute(x0, y0, uint32(w), uint32(h)) },
		func() error { return dev.Button(buttonLeft, true) },
		func() error { return dev.MotionAbsolute((x0+x1)/2, (y0+y1)/2, uint32(w), uint32(h)) },
		func() error { return dev.MotionAbsolute(x1, y1, uint32(w), uint32(h)) },
		func() error { return dev.Button(buttonLeft, false) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func tap(t *testing.T, dev *vinput.Device, code uint32) {
	t.Helper()
	for _, pressed := range []bool{true, false} {
		if err := dev.Key(code, pressed); err != nil {
			t.Fatal(err)
		}
	}
}

func answer(t *testing.T, answers chan result) result {
	t.Helper()
	select {
	case r := <-answers:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the overlay never answered")
		return result{}
	}
}

// Enter confirms a dragged region on a real compositor, read through the
// keymap the keyboard sent: the Return of a keymap like wtype's, on a
// code a physical keyboard gives Escape.
func TestHeadlessEnterConfirmsTheDraggedRegion(t *testing.T) {
	dev, answers := startOverlay(t)
	dragOn(t, dev, 100, 100, 300, 200)
	tap(t, dev, evdevOne)
	r := answer(t, answers)
	if !r.ok || r.sel.Width != 200 || r.sel.Height != 100 || r.sel.X != 100 || r.sel.Y != 100 {
		t.Fatalf("answer = %+v, want the 200x100 region at 100,100 confirmed", r)
	}
}

// Escape cancels, read the same way: the code a physical keyboard gives
// Return carries Escape in this keymap, and cancels.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	dev, answers := startOverlay(t)
	dragOn(t, dev, 100, 100, 300, 200)
	tap(t, dev, evdevTwentyEight)
	if r := answer(t, answers); r.ok {
		t.Fatalf("Escape confirmed %+v", r.sel)
	}
}
