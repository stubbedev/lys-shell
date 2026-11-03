package regionoverlay

import (
	"context"
	"errors"
	"image"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
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
	r := newRig(t)
	return r.kb, r.request(t, context.Background())
}

// rig is an overlay on a private headless sway, its keyboard dialled.
type rig struct {
	a  *app.Application
	o  *Overlay
	kb *keytest.Keyboard
}

func newRig(t *testing.T) *rig {
	t.Helper()
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	o := New(a, &Gate{}, a.Session().Outputs, nil, font, widget.NewStylesheet("", 0))
	return &rig{a: a, o: o, kb: keytest.Dial(t, a)}
}

// request opens a request and waits until its surface holds the keyboard;
// the answer arrives on the returned channel. It ends with the test.
func (r *rig) request(t *testing.T, ctx context.Context) chan result {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	answers := make(chan result, 1)
	go func() {
		sel, ok, err := r.o.Request(ctx, nil)
		if err != nil {
			t.Errorf("the request was refused: %v", err)
		}
		answers <- result{sel: sel, ok: ok}
	}()
	apptest.Until(t, r.a, "the overlay's surface took the keyboard", func() bool {
		return len(r.o.surfaces) > 0 && r.a.Session().KeyboardFocus() == r.o.surfaces[0].HostSurface()
	})
	return answers
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

// A second request while a selection is being made maps no surface, takes
// no capture and leaves the selection: the first request then answers the
// region the user had drawn.
func TestHeadlessSecondRequestOpensNothing(t *testing.T) {
	r := newRig(t)
	first := r.request(t, context.Background())
	dragOn(t, r.kb, 100, 100, 300, 200)
	type state struct {
		surfaces []*app.LayerWindow
		sel      image.Rectangle
		reply    chan result
	}
	read := func() state {
		return apptest.On(r.a, func() state { return state{slices.Clone(r.o.surfaces), r.o.ed.sel, r.o.reply} })
	}
	drawn := image.Rect(100, 100, 300, 200)
	before := read()
	for deadline := time.Now().Add(10 * time.Second); before.sel != drawn; before = read() {
		if time.Now().After(deadline) {
			t.Fatalf("the drag never reached %v: %+v", drawn, before)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(before.surfaces) == 0 {
		t.Fatalf("nothing open to refuse over: %+v", before)
	}
	captures := 0
	err := within(t, "the second request's refusal", func() error {
		_, _, err := r.o.Request(context.Background(), func() (map[string]render.StillImage, error) {
			captures++
			return nil, nil
		})
		return err
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("the second request answered %v, want ErrBusy", err)
	}
	if captures != 0 {
		t.Errorf("the refused request took %d captures", captures)
	}
	after := read()
	if !slices.Equal(after.surfaces, before.surfaces) || after.sel != before.sel || after.reply != before.reply {
		t.Fatalf("the refused request changed the session: %+v, was %+v", after, before)
	}
	apptest.Never(t, "the first request after the refusal", first)
	r.kb.Tap(keytest.Return)
	got := apptest.Answer(t, "the first request after Return", first)
	if !got.ok || got.sel.Width != 200 || got.sel.Height != 100 || got.sel.X != 100 || got.sel.Y != 100 {
		t.Fatalf("answer = %+v, want the region drawn before the refusal", got)
	}
}

// A new request opens once the previous session ended, however it ended.
func TestHeadlessNewRequestAfterTheSessionEnded(t *testing.T) {
	r := newRig(t)
	confirmed := r.request(t, context.Background())
	dragOn(t, r.kb, 100, 100, 300, 200)
	r.kb.Tap(keytest.Return)
	if got := apptest.Answer(t, "the confirmed session", confirmed); !got.ok {
		t.Fatalf("confirm answered %+v", got)
	}
	cancelled := r.request(t, context.Background())
	r.kb.Tap(keytest.Escape)
	if got := apptest.Answer(t, "the cancelled session", cancelled); got.ok {
		t.Fatalf("Escape answered %+v", got)
	}
	ctx, dismiss := context.WithCancel(context.Background())
	dismissed := r.request(t, ctx)
	dismiss()
	if got := apptest.Answer(t, "the dismissed session", dismissed); got.ok {
		t.Fatalf("the dismissal answered %+v", got)
	}
	if n := apptest.On(r.a, func() int { return len(r.o.surfaces) }); n != 0 {
		t.Fatalf("%d surfaces left after the dismissal", n)
	}
	last := r.request(t, context.Background())
	r.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the session after a dismissal", last)
}

// within runs fn, failing the test with what if it does not return in
// apptest.Answer's bound: a request wrongly admitted would block on its overlay.
func within[T any](t *testing.T, what string, fn func() T) T {
	t.Helper()
	answered := make(chan T, 1)
	go func() { answered <- fn() }()
	return apptest.Answer(t, what, answered)
}
