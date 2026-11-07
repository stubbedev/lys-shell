package powermenu

import (
	"errors"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

type fakeWindow struct {
	closed  bool
	focused widget.Widget
}

func (w *fakeWindow) Close() { w.closed = true }

func (w *fakeWindow) SetFocus(t widget.Widget) error {
	w.focused = t
	return nil
}

type harness struct {
	cfg     *config.Config
	m       *Menu
	opened  []app.LayerConfig
	windows []*fakeWindow
	ran     []string
	openErr error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	face := treetest.Font(t)
	h := &harness{cfg: config.Defaults()}
	h.m = New(Deps{
		Config: func() *config.Config { return h.cfg },
		Open: func(cfg app.LayerConfig) (Window, error) {
			if h.openErr != nil {
				return nil, h.openErr
			}
			h.opened = append(h.opened, cfg)
			w := &fakeWindow{}
			h.windows = append(h.windows, w)
			return w, nil
		},
		Run:  func(cmd string) { h.ran = append(h.ran, cmd) },
		Font: face,
	})
	return h
}

// buttons are the menu's buttons in order.
func (h *harness) buttons(t *testing.T) []*widget.Button {
	t.Helper()
	var out []*widget.Button
	var walk func(w widget.Widget)
	walk = func(w widget.Widget) {
		if b, ok := w.(*widget.Button); ok {
			out = append(out, b)
			return
		}
		if c, ok := w.(interface{ Children() []widget.Widget }); ok {
			for _, k := range c.Children() {
				walk(k)
			}
		}
	}
	walk(h.opened[len(h.opened)-1].Root)
	return out
}

func TestShowMapsTheOverlay(t *testing.T) {
	h := newHarness(t)
	h.m.Show()
	if len(h.opened) != 1 {
		t.Fatalf("opened %d surfaces", len(h.opened))
	}
	cfg := h.opened[0]
	all := app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight
	if cfg.Layer != app.LayerOverlay || cfg.Anchor != all || cfg.Keyboard != app.KeyboardExclusive ||
		cfg.ExclusiveZone != -1 || cfg.Namespace != "lys-shell-power-menu" {
		t.Errorf("layer config = %+v", cfg)
	}
	if got := len(h.buttons(t)); got != 5 {
		t.Errorf("buttons = %d, want all five by default", got)
	}
	if !h.m.rev.Revealed() {
		t.Error("the menu is not entering")
	}
	h.m.Show() // already up
	if len(h.opened) != 1 {
		t.Error("a second Show while up mapped another surface")
	}
}

func TestTheMenuFrostsBehindItsSurfaceAlone(t *testing.T) {
	h := newHarness(t)
	h.m.d.Sheet = apptheme.New(h.cfg).Sheet
	h.m.Show()
	cfg := h.opened[0]
	if !cfg.BackgroundBlur {
		t.Error("the menu does not ask for the blur")
	}
	out := render.Rect{W: 1600, H: 1000}
	cfg.Root.Arrange(out)
	cfg.Root.Arrange(out)
	h.m.rev.Finish()
	cfg.Root.Arrange(out)
	region := widget.BackdropRegion(cfg.Root)
	if len(region) == 0 {
		t.Fatal("no blur region")
	}
	bound := region[0]
	for _, r := range region[1:] {
		bound = bound.Union(r)
	}
	if bound.W >= out.W || bound.H >= out.H {
		t.Errorf("the blur covers %+v of %+v, the dim around the surface as well", bound, out)
	}
}

func TestArrowsWalkTheButtonsAndWrap(t *testing.T) {
	h := newHarness(t)
	h.cfg.Power.ShowReboot = false
	h.m.Show()
	buttons := h.buttons(t)
	press := func(name string) bool {
		t.Helper()
		a, err := app.ParseAccel(name)
		if err != nil {
			t.Fatal(err)
		}
		return h.opened[0].KeyCapture(a)
	}
	for i, step := range []struct {
		key  string
		want int
	}{{"Right", 0}, {"Right", 1}, {"Left", 0}, {"Left", len(buttons) - 1}, {"Right", 0}} {
		if !press(step.key) {
			t.Fatalf("step %d: %s was not taken", i, step.key)
		}
		if h.windows[0].focused != widget.Widget(buttons[step.want]) {
			t.Errorf("step %d: %s did not focus button %d", i, step.key, step.want)
		}
	}
	if press("a") {
		t.Error("the menu took a key it has no use for")
	}
}

func TestTheFirstLeftLandsOnTheLastButton(t *testing.T) {
	h := newHarness(t)
	h.m.Show()
	buttons := h.buttons(t)
	a, err := app.ParseAccel("Left")
	if err != nil {
		t.Fatal(err)
	}
	h.opened[0].KeyCapture(a)
	if h.windows[0].focused != widget.Widget(buttons[len(buttons)-1]) {
		t.Error("the first Left did not land on the last button")
	}
}

func TestOnlyActionsThatEndTheSessionAreDangerous(t *testing.T) {
	h := newHarness(t)
	h.m.Show()
	for i, want := range []bool{false, true, false, true, true} {
		if got := h.buttons(t)[i].HasClass("danger"); got != want {
			t.Errorf("button %d danger = %v, want %v", i, got, want)
		}
	}
}

func TestHiddenActionsAreLeftOut(t *testing.T) {
	h := newHarness(t)
	h.cfg.Power.ShowReboot = false
	h.cfg.Power.ShowLogout = false
	h.m.Show()
	if got := len(h.buttons(t)); got != 3 {
		t.Errorf("buttons = %d, want the three still shown", got)
	}
}

func TestButtonRunsItsCommandAfterTheExit(t *testing.T) {
	h := newHarness(t)
	h.cfg.Power.SuspendCommand = "systemctl suspend"
	h.m.Show()
	h.m.rev.Finish()
	h.buttons(t)[2].OnClick() // lock, log out, suspend
	if len(h.ran) != 0 || h.windows[0].closed {
		t.Fatal("the command ran (or the menu closed) before the exit played")
	}
	if h.m.Open() {
		t.Error("the menu still reports open mid-exit")
	}
	h.m.rev.Finish()
	if !h.windows[0].closed || len(h.ran) != 1 || h.ran[0] != "systemctl suspend" {
		t.Errorf("after the exit: closed %v, ran %v", h.windows[0].closed, h.ran)
	}
	h.m.Show()
	if len(h.opened) != 2 {
		t.Error("the menu did not reopen after closing")
	}
}

func TestEscapeCancels(t *testing.T) {
	h := newHarness(t)
	h.cfg.Animations.Enabled = false
	h.m.Show()
	if !h.opened[0].KeyCapture(app.Accel{Sym: escape.Sym}) {
		t.Fatal("Escape was not consumed")
	}
	if !h.windows[0].closed || len(h.ran) != 0 {
		t.Errorf("Escape: closed %v, ran %v; want closed, nothing run", h.windows[0].closed, h.ran)
	}
	other, _ := app.ParseAccel("a")
	h.m.Show()
	if h.opened[1].KeyCapture(other) {
		t.Error("a plain key was consumed")
	}
	h.m.Cancel()
	h.m.Cancel() // closing already: no double close or run
	if len(h.ran) != 0 {
		t.Errorf("cancel ran %v", h.ran)
	}
}

func TestShowWhenTheOverlayCannotMap(t *testing.T) {
	h := newHarness(t)
	h.openErr = errors.New("no layer shell")
	h.m.Show()
	if h.m.Open() {
		t.Error("an unmapped menu reports open")
	}
	h.m.Cancel() // nothing to close
}

// The row is shell/power_menu's: homogeneous, so every button takes
// the widest's width without stretching the row, and the labels take
// their ink from the stylesheet.
func TestRowIsHomogeneousAndUnpinned(t *testing.T) {
	h := newHarness(t)
	h.m.Show()
	root := h.opened[0].Root
	rows := treetest.WithClass(root, "power-menu-surface")
	if len(rows) != 1 || !rows[0].(*widget.Box).Homogeneous() {
		t.Fatalf("surface %v, want one homogeneous .power-menu-surface", rows)
	}
	for _, l := range treetest.All[*widget.Label](root) {
		if l.Color() != 0 {
			t.Errorf("label %q pins ink %#08x", l.Text(), uint32(l.Color()))
		}
	}
}

// The surface is lys-ui's glass and each action a flat button,
// a square tile of the menu's own size.
func TestActionsAreFlatTilesOnGlass(t *testing.T) {
	h := newHarness(t)
	h.m.Show()
	root := h.opened[0].Root
	if surface := treetest.WithClass(root, "power-menu-surface"); len(surface) != 1 || !widget.HasClass(surface[0], css.Glass) {
		t.Error("the surface is not lys-ui's glass")
	}
	for _, b := range h.buttons(t) {
		if !b.HasClass(css.Flat) || b.SizeRequest() != (widget.Size{W: buttonPx, H: buttonPx}) {
			t.Errorf("an action button = classes %v size %v, want a %dpx flat tile", b.Classes(), b.SizeRequest(), buttonPx)
		}
	}
}

func TestTheMenuAsksForNoBlurWithReducedTransparency(t *testing.T) {
	h := newHarness(t)
	h.cfg.Styling.ReduceTransparency = true
	h.m.Show()
	if h.opened[0].BackgroundBlur {
		t.Error("reduce-transparency still asks for the blur")
	}
}
