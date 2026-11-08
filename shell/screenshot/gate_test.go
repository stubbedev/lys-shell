package screenshot

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// syncLoop stands in for the application's loop: it runs what it is
// given at once and settles at once.
type syncLoop struct{}

func (syncLoop) Invoke(fn func()) { fn() }

func (syncLoop) Settle(done func()) { done() }

// gated is an overlay under the real gate that stays open until the test
// answers it or its context ends.
type gated[T any] struct {
	gate   *regionoverlay.Gate
	opened chan chan T
}

func newGated[T any](g *regionoverlay.Gate) *gated[T] {
	return &gated[T]{gate: g, opened: make(chan chan T, 1)}
}

func (o *gated[T]) open(ctx context.Context, frames regionoverlay.Frames) (T, error) {
	return regionoverlay.OpenSession(ctx, o.gate, syncLoop{}, frames,
		func(_ map[string]render.StillImage, reply chan T) { o.opened <- reply },
		func(reply chan T) {
			var none T
			reply <- none
		})
}

type regionAnswer struct {
	sel regionoverlay.Selection
	ok  bool
}

type gatedRegion struct{ *gated[regionAnswer] }

func (r gatedRegion) Request(ctx context.Context, frames regionoverlay.Frames) (regionoverlay.Selection, bool, error) {
	a, err := r.open(ctx, frames)
	return a.sel, a.ok, err
}

type pickAnswer struct {
	c  colorpicker.RGB
	ok bool
}

type gatedPicker struct{ *gated[pickAnswer] }

func (p gatedPicker) Request(ctx context.Context, frames regionoverlay.Frames, _ colorpicker.Options) (colorpicker.Pick, bool, error) {
	a, err := p.open(ctx, frames)
	return colorpicker.Pick{Color: a.c}, a.ok, err
}

// gateRig is a host whose overlays share the real gate, counting the
// captures it takes.
type gateRig struct {
	h        *Host
	region   gatedRegion
	picker   gatedPicker
	captures *atomic.Int32
}

func newGateRig(t *testing.T) *gateRig {
	t.Helper()
	h, _, _ := testHost(t, nil)
	r := &gateRig{
		h:        h,
		region:   gatedRegion{newGated[regionAnswer](h.Gate)},
		picker:   gatedPicker{newGated[pickAnswer](h.Gate)},
		captures: &atomic.Int32{},
	}
	h.Region, h.Picker = r.region, r.picker
	frozen := h.frozen
	h.frozen = func(pointer bool) ([]frozenOutput, error) {
		r.captures.Add(1)
		return frozen(pointer)
	}
	guardCompositor(t)
	return r
}

// guardCompositor points the real capture path at nothing: a request wrongly
// let through fails, and no test reaches a developer's screen.
func guardCompositor(t *testing.T) {
	t.Helper()
	t.Setenv("WAYLAND_DISPLAY", "lys-shell-test-no-such-display")
}

// capture runs a region capture for handle; its path and error arrive on the
// returned channel.
func (r *gateRig) capture(handle string) chan captureAnswer {
	answers := make(chan captureAnswer, 1)
	go func() {
		path, err := r.h.Capture(portaldialogs.Caller{Handle: handle}, ModeRegion, "")
		answers <- captureAnswer{path, err}
	}()
	return answers
}

type captureAnswer struct {
	path string
	err  error
}

// assertRefused has every kind of request refused with ErrBusy, for handle.
func assertRefused(t *testing.T, h *Host, handle string) {
	t.Helper()
	caller := portaldialogs.Caller{Handle: handle}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, mode := range []string{ModeRegion, ModeOutput, ModeScreen, ModeWindow} {
			if path, err := h.Capture(caller, mode, ""); !errors.Is(err, ErrBusy) || path != "" {
				t.Errorf("Capture %s = %q %v, want ErrBusy", mode, path, err)
			}
			if path, err := h.CapturePrivate(caller, mode, "", false); !errors.Is(err, ErrBusy) || path != "" {
				t.Errorf("CapturePrivate %s = %q %v, want ErrBusy", mode, path, err)
			}
		}
		if _, picked, err := h.PickColor(caller); !errors.Is(err, ErrBusy) || picked {
			t.Errorf("PickColor = %v %v, want ErrBusy", picked, err)
		}
	}()
	apptest.Answer(t, "the refusals of every kind of request", done)
}

// During a region selection or a color pick every other request is refused
// before it captures, whatever its mode, and the open session goes on to
// answer as if nothing had happened.
func TestEveryRequestIsRefusedDuringASession(t *testing.T) {
	t.Run("region", func(t *testing.T) {
		r := newGateRig(t)
		answers := r.capture("/r/open")
		reply := apptest.Answer(t, "the region opened", r.region.opened)
		assertRefused(t, r.h, "/r/refused")
		if n := r.captures.Load(); n != 1 {
			t.Errorf("%d captures, want the open session's one", n)
		}
		if live := r.h.mem.Stats().Live; live != 2 {
			t.Errorf("%d frames allocated, want the open session's 2", live)
		}
		if len(reply) != 0 || len(answers) != 0 {
			t.Fatal("a refused request answered the open session")
		}
		reply <- regionAnswer{regionoverlay.Selection{Output: "DP-1", Width: 20, Height: 10}, true}
		a := apptest.Answer(t, "the open session", answers)
		if a.err != nil || a.path == "" {
			t.Fatalf("the open session answered %+v, want its saved selection", a)
		}
	})
	t.Run("color pick", func(t *testing.T) {
		r := newGateRig(t)
		answers := make(chan bool, 1)
		go func() {
			_, picked, err := r.h.PickColor(portaldialogs.Caller{Handle: "/r/open"})
			if err != nil {
				t.Errorf("the open pick failed: %v", err)
			}
			answers <- picked
		}()
		reply := apptest.Answer(t, "the picker opened", r.picker.opened)
		assertRefused(t, r.h, "/r/refused")
		if n := r.captures.Load(); n != 1 {
			t.Errorf("%d captures, want the open session's one", n)
		}
		reply <- pickAnswer{colorpicker.RGB{R: 1}, true}
		if !apptest.Answer(t, "the open pick", answers) {
			t.Fatal("the open pick did not answer its pick")
		}
	})
}

// A new request opens once the previous session ended: confirmed, cancelled
// or dismissed, each releasing what it captured.
func TestANewRequestOpensAfterTheSessionEnded(t *testing.T) {
	for name, end := range map[string]func(r *gateRig, reply chan regionAnswer){
		"confirmed": func(_ *gateRig, reply chan regionAnswer) {
			reply <- regionAnswer{regionoverlay.Selection{Output: "DP-1", Width: 20, Height: 10}, true}
		},
		"cancelled": func(_ *gateRig, reply chan regionAnswer) { reply <- regionAnswer{} },
		"dismissed": func(r *gateRig, _ chan regionAnswer) { r.h.Dismiss("/r/first") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newGateRig(t)
			first := r.capture("/r/first")
			end(r, apptest.Answer(t, "the first session opened", r.region.opened))
			if a := apptest.Answer(t, "the first session", first); a.err != nil {
				t.Fatalf("the first session answered %+v", a)
			}
			if live := r.h.mem.Stats().Live; live != 0 {
				t.Errorf("%d frames allocated after the session", live)
			}
			second := r.capture("/r/second")
			apptest.Answer(t, "the next session opened", r.region.opened) <- regionAnswer{}
			if a := apptest.Answer(t, "the next session", second); a.err != nil || a.path != "" {
				t.Fatalf("the next session answered %+v", a)
			}
			if n := r.captures.Load(); n != 2 {
				t.Errorf("%d captures, want one per session", n)
			}
		})
	}
}

// Dismissing the request whose region selection is open cancels the overlay;
// a refused request's handle, or another's, leaves it, also after a refused
// request carried the open one's handle.
func TestDismissClosesTheRightOverlay(t *testing.T) {
	r := newGateRig(t)
	answers := r.capture("/r/mine")
	reply := apptest.Answer(t, "the region opened", r.region.opened)
	assertRefused(t, r.h, "/r/refused")
	assertRefused(t, r.h, "/r/mine")
	r.h.Dismiss("/r/refused")
	r.h.Dismiss("/r/other")
	r.h.Dismiss("")
	if len(reply) != 0 || len(answers) != 0 {
		t.Fatal("another request's dismissal touched the open overlay")
	}
	r.h.Dismiss("/r/mine")
	if a := apptest.Answer(t, "the open session", answers); a.err != nil || a.path != "" {
		t.Fatalf("the dismissed selection answered %+v", a)
	}
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	if len(r.h.interactive) != 0 {
		t.Errorf("%d requests still registered after they all ended", len(r.h.interactive))
	}
}

// A Dismiss arriving while the frames are captured opens nothing afterwards,
// and the frames are released.
func TestDismissDuringTheCaptureOpensNothing(t *testing.T) {
	r := newGateRig(t)
	frozen := r.h.frozen
	r.h.frozen = func(pointer bool) ([]frozenOutput, error) {
		r.h.Dismiss("/r/mine")
		return frozen(pointer)
	}
	a := apptest.Answer(t, "the dismissed request", r.capture("/r/mine"))
	if a.err != nil || a.path != "" {
		t.Fatalf("answered %+v", a)
	}
	if len(r.region.opened) != 0 {
		t.Fatal("the dismissed request opened its overlay")
	}
	if live := r.h.mem.Stats().Live; live != 0 {
		t.Errorf("%d frames allocated after the dismissed request", live)
	}
}

// An instant capture running keeps an overlay off the screen it is capturing:
// the overlay request is refused, while another instant capture runs.
func TestAnInstantCaptureRefusesAnOverlay(t *testing.T) {
	r := newGateRig(t)
	entered, proceed := make(chan struct{}), make(chan struct{})
	frozen := r.h.frozen
	var calls atomic.Int32
	r.h.frozen = func(pointer bool) ([]frozenOutput, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-proceed
		}
		return frozen(pointer)
	}
	first := make(chan error, 1)
	go func() {
		_, err := r.h.Capture(portaldialogs.Caller{}, ModeScreen, "")
		first <- err
	}()
	apptest.Answer(t, "the instant capture started", entered)
	if a := apptest.Answer(t, "the region's refusal", r.capture("/r/region")); !errors.Is(a.err, ErrBusy) {
		t.Errorf("a region during an instant capture answered %+v, want ErrBusy", a)
	}
	if _, _, err := r.h.PickColor(portaldialogs.Caller{}); !errors.Is(err, ErrBusy) {
		t.Errorf("a pick during an instant capture answered %v, want ErrBusy", err)
	}
	if _, err := r.h.Capture(portaldialogs.Caller{}, ModeScreen, ""); err != nil {
		t.Errorf("a second instant capture answered %v", err)
	}
	close(proceed)
	if err := apptest.Answer(t, "the instant capture", first); err != nil {
		t.Fatal(err)
	}
	second := r.capture("/r/region")
	apptest.Answer(t, "the next session opened", r.region.opened) <- regionAnswer{}
	if a := apptest.Answer(t, "the next session", second); a.err != nil {
		t.Fatalf("a region after the instant capture answered %+v", a)
	}
}
