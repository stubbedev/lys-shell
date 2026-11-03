package colorpicker

import (
	"context"
	"errors"
	"image"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// Escape cancels the picker on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the picker open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	r := newRig(t)
	answers := r.pick(t)
	r.kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	r.kb.Tap(keytest.Escape)
	if got := apptest.Answer(t, "the picker after Escape", answers); got.ok || got.err != nil {
		t.Fatalf("Escape answered %+v, want a cancel", got)
	}
}

// rig is a picker and a region overlay on one capture gate, on a private
// headless sway, its keyboard dialled.
type rig struct {
	a      *app.Application
	p      *Picker
	region *regionoverlay.Overlay
	kb     *keytest.Keyboard
}

func newRig(t *testing.T) *rig {
	t.Helper()
	a := apptest.Run(t)
	font, err := app.Font("monospace", 13)
	if err != nil {
		t.Fatal(err)
	}
	gate := &regionoverlay.Gate{}
	return &rig{
		a:      a,
		p:      New(a, gate, a.Session().Outputs, font, ""),
		region: regionoverlay.New(a, gate, a.Session().Outputs, nil, font, nil),
		kb:     keytest.Dial(t, a),
	}
}

// frames is a frozen frame per output.
func (r *rig) frames() (map[string]render.StillImage, error) {
	return apptest.On(r.a, func() map[string]render.StillImage {
		frames := map[string]render.StillImage{}
		for _, out := range r.a.Session().Outputs() {
			frames[out.Name] = render.NewStillImage(image.NewRGBA(image.Rect(0, 0, 64, 64)))
		}
		return frames
	}), nil
}

// pick opens the picker and waits until it holds the keyboard; the answer
// arrives on the returned channel. It ends with the test.
func (r *rig) pick(t *testing.T) chan result {
	t.Helper()
	apptest.Until(t, r.a, "an output appeared", func() bool { return len(r.a.Session().Outputs()) > 0 })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	answers := make(chan result, 1)
	go func() {
		c, ok, err := r.p.Request(ctx, r.frames)
		answers <- result{color: c, ok: ok, err: err}
	}()
	apptest.Until(t, r.a, "the picker took the keyboard", func() bool {
		return len(r.p.surfaces) > 0 && r.a.Session().KeyboardFocus() == r.p.surfaces[0].HostSurface()
	})
	return answers
}

// uncaptured is a Frames failing the test if a refused request runs it.
func uncaptured(t *testing.T) regionoverlay.Frames {
	return func() (map[string]render.StillImage, error) {
		t.Error("a refused request took a capture")
		return nil, nil
	}
}

func TestHeadlessClosingLeavesNothingPainting(t *testing.T) {
	r := newRig(t)
	answers := r.pick(t)
	surfaces := apptest.On(r.a, func() []*app.LayerWindow { return slices.Clone(r.p.surfaces) })
	r.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the pick after Escape", answers)
	for i, s := range surfaces {
		if err := apptest.On(r.a, s.EnsureUsable); err == nil {
			t.Errorf("surface %d still draws after the requester got its answer", i)
		}
	}
}

// A region selection during a color pick is refused: it captures nothing,
// and the picker stays open, holding the keyboard, until the user answers.
// The region overlay opens once the pick ended.
func TestHeadlessRegionIsRefusedDuringAPick(t *testing.T) {
	r := newRig(t)
	answers := r.pick(t)
	surfaces := apptest.On(r.a, func() []*app.LayerWindow { return slices.Clone(r.p.surfaces) })
	refused := within(t, "the region request's refusal", func() error {
		_, _, err := r.region.Request(context.Background(), uncaptured(t))
		return err
	})
	if !errors.Is(refused, regionoverlay.ErrBusy) {
		t.Fatalf("the region request answered %v, want ErrBusy", refused)
	}
	apptest.Until(t, r.a, "the picker kept its surfaces and the keyboard", func() bool {
		return slices.Equal(r.p.surfaces, surfaces) && r.a.Session().KeyboardFocus() == surfaces[0].HostSurface()
	})
	apptest.Never(t, "the pick after a refused region request", answers)
	r.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the picker after Escape", answers)

	ctx, dismiss := context.WithCancel(context.Background())
	opened := make(chan error, 1)
	go func() {
		_, _, err := r.region.Request(ctx, func() (map[string]render.StillImage, error) {
			dismiss()
			return nil, nil
		})
		opened <- err
	}()
	if err := apptest.Answer(t, "the region request after the pick", opened); err != nil {
		t.Fatalf("the region request after the pick was refused: %v", err)
	}
}

// A color pick during a region selection is refused: it maps no surface and
// captures nothing, and the selection goes on. The picker opens once the
// selection ended.
func TestHeadlessPickIsRefusedDuringARegionSelection(t *testing.T) {
	r := newRig(t)
	ctx, dismiss := context.WithCancel(context.Background())
	t.Cleanup(dismiss)
	selection := make(chan bool, 1)
	go func() {
		_, ok, err := r.region.Request(ctx, nil)
		if err != nil {
			t.Errorf("the region request was refused: %v", err)
		}
		selection <- ok
	}()
	keytest.AwaitFocus(t, r.a)
	refused := within(t, "the pick's refusal", func() error {
		_, _, err := r.p.Request(context.Background(), uncaptured(t))
		return err
	})
	if !errors.Is(refused, regionoverlay.ErrBusy) {
		t.Fatalf("the pick answered %v, want ErrBusy", refused)
	}
	if n := apptest.On(r.a, func() int { return len(r.p.surfaces) }); n != 0 {
		t.Fatalf("the refused pick mapped %d surfaces", n)
	}
	apptest.Never(t, "the selection after a refused pick", selection)
	r.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the selection after Escape", selection)
	answers := r.pick(t)
	r.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the pick after the selection", answers)
}

// within runs fn, failing the test with what if it does not return in
// apptest.Answer's bound: a request wrongly admitted would block on its overlay.
func within[T any](t *testing.T, what string, fn func() T) T {
	t.Helper()
	answered := make(chan T, 1)
	go func() { answered <- fn() }()
	return apptest.Answer(t, what, answered)
}
