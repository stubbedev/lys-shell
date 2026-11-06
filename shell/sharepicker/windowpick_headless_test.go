package sharepicker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// pickRig is a picker with the real click-to-pick overlay on a private
// sway, a window pick open over a back window, a smaller front one
// overlapping it, and one on an output sway does not have.
type pickRig struct {
	t       *testing.T
	a       *app.Application
	p       *Picker
	o       *regionoverlay.WindowPick
	gate    *regionoverlay.Gate
	kb      *keytest.Keyboard
	output  string
	answers chan string
}

func windowPicker(t *testing.T, screen string) *pickRig {
	t.Helper()
	a := apptest.Run(t)
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	gate := &regionoverlay.Gate{}
	o := regionoverlay.NewWindowPick(a, gate, a.Session().Outputs, treetest.Font(t), nil)
	p := New(a, Deps{Config: func() *config.Config { return cfg }, Font: treetest.Font(t)}, gate, nil, o)
	output := apptest.On(a, func() string { return a.Session().Outputs()[0].Name })
	r := &pickRig{t: t, a: a, p: p, o: o, gate: gate, kb: keytest.Dial(t, a), output: output, answers: make(chan string, 1)}
	req := WindowRequest{Windows: []regionoverlay.Window{
		{ID: "front", Output: output, X: 200, Y: 150, Width: 200, Height: 100},
		{ID: "elsewhere", Output: "NOPE-1", X: 0, Y: 0, Width: 1000, Height: 1000},
		{ID: "back", Output: output, X: 50, Y: 50, Width: 600, Height: 400},
	}, Screen: screen}
	t.Cleanup(func() { p.Dismiss("/r/click") })
	go func() {
		got, err := p.PickWindow(portaldialogs.Caller{Handle: "/r/click"}, req)
		if err != nil {
			t.Errorf("the open window pick failed: %v", err)
		}
		r.answers <- got
	}()
	apptest.Until(t, a, "the overlay took the keyboard", func() bool { return gate.Open() && a.Session().KeyboardFocus() != nil })
	return r
}

// point moves the pointer to x,y on the output, again until the overlay
// has it there: a surface only follows the pointer once it is mapped.
func (r *pickRig) point(x, y int, under string) {
	r.t.Helper()
	w, h := r.kb.OutputSize()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		for _, at := range [][2]int{{x + 1, y + 1}, {x, y}} {
			if err := r.kb.MotionAbsolute(uint32(at[0]), uint32(at[1]), uint32(w), uint32(h)); err != nil {
				r.t.Fatal(err)
			}
			if err := r.kb.Roundtrip(); err != nil {
				r.t.Fatal(err)
			}
			apptest.Dispatched(r.t, r.a)
		}
		hovered := apptest.On(r.a, func() regionoverlay.Window { w, _ := r.o.Hovered(); return w })
		if hovered.ID == under {
			return
		}
	}
	r.t.Fatalf("the overlay never had the pointer at %d,%d over %q", x, y, under)
}

// click presses and releases the left button where the pointer is.
func (r *pickRig) click() {
	r.t.Helper()
	for _, pressed := range []bool{true, false} {
		if err := r.kb.Button(buttonLeft, pressed); err != nil {
			r.t.Fatal(err)
		}
		if err := r.kb.Roundtrip(); err != nil {
			r.t.Fatal(err)
		}
		apptest.Dispatched(r.t, r.a)
	}
}

func TestHeadlessAClickPicksTheFrontWindowUnderThePointer(t *testing.T) {
	r := windowPicker(t, "")
	r.point(250, 180, "front")
	r.click()
	if got := apptest.Answer(t, "the pick after a click on the front window", r.answers); got != "/window:front" {
		t.Fatalf("the click answered %q, want the front window", got)
	}
	if apptest.On(r.a, r.gate.Open) {
		t.Error("the overlay stayed up after the pick")
	}
}

func TestHeadlessAClickBesideTheFrontWindowPicksTheOneBehind(t *testing.T) {
	r := windowPicker(t, "")
	r.point(100, 100, "back")
	r.click()
	if got := apptest.Answer(t, "the pick after a click on the back window", r.answers); got != "/window:back" {
		t.Fatalf("the click answered %q, want the back window", got)
	}
}

func TestHeadlessAClickOnNoWindowPicksNothingAndEscapeGivesUp(t *testing.T) {
	r := windowPicker(t, "")
	r.point(250, 180, "front")
	r.point(800, 600, "")
	r.click()
	apptest.Never(t, "an answer to a click on no window", r.answers)
	r.kb.Tap(keytest.Return)
	apptest.Never(t, "an answer to Enter with no screen offered", r.answers)
	r.kb.Tap(keytest.Escape)
	if got := apptest.Answer(t, "the pick after Escape", r.answers); got != "" {
		t.Fatalf("Escape answered %q, want nothing", got)
	}
	if apptest.On(r.a, r.gate.Open) {
		t.Error("the overlay stayed up after Escape")
	}
}

func TestHeadlessEnterSharesTheOfferedScreen(t *testing.T) {
	r := windowPicker(t, "DP-9")
	r.kb.Tap(keytest.Return)
	if got := apptest.Answer(t, "the pick after Enter", r.answers); got != "/screen:DP-9" {
		t.Fatalf("Enter answered %q, want the offered screen", got)
	}
}

func TestHeadlessDismissTakesTheWindowPickDown(t *testing.T) {
	r := windowPicker(t, "")
	r.p.Dismiss("/r/other")
	apptest.Never(t, "an answer to another request's dismissal", r.answers)
	r.p.Dismiss("/r/click")
	if got := apptest.Answer(t, "the pick after its dismissal", r.answers); got != "" {
		t.Fatalf("the dismissed pick answered %q, want nothing", got)
	}
	apptest.Until(t, r.a, "the overlay went", func() bool { return !r.gate.Open() })
}

func TestPickWindowFailsWhenTheOverlayCannotOpenAndAnswersEscapeWithNothing(t *testing.T) {
	req := WindowRequest{Windows: []regionoverlay.Window{{ID: "w", Output: "DP-1", Width: 10, Height: 10}}, Screen: "DP-1"}
	if got, err := New(&app.Application{}, Deps{}, &regionoverlay.Gate{}, nil, nil).PickWindow(portaldialogs.Caller{Handle: "/r/none"}, req); !errors.Is(err, errNoWindowPick) || got != "" {
		t.Fatalf("a picker without the overlay answered %q %v, want errNoWindowPick", got, err)
	}
	gate := &regionoverlay.Gate{}
	end, err := gate.Instant()
	if err != nil {
		t.Fatal(err)
	}
	a := &app.Application{}
	p := New(a, Deps{}, gate, nil, regionoverlay.NewWindowPick(a, gate, nil, nil, nil))
	if got, err := p.PickWindow(portaldialogs.Caller{Handle: "/r/busy"}, req); !errors.Is(err, regionoverlay.ErrBusy) || got != "" {
		t.Fatalf("a pick while another capture holds the gate answered %q %v, want ErrBusy", got, err)
	}
	end()
	ended := New(a, Deps{}, gate, nil, nil)
	ended.windowPick = endedPick{}
	if got, err := ended.PickWindow(portaldialogs.Caller{Handle: "/r/esc"}, req); err != nil || got != "" {
		t.Fatalf("Escape answered %q %v, want nothing and no error", got, err)
	}
}

type endedPick struct{}

func (endedPick) Request(context.Context, []regionoverlay.Window, string) (regionoverlay.Picked, bool, error) {
	return regionoverlay.Picked{}, false, nil
}
