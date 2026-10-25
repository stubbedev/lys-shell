package bar

import (
	"os"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestParseClickActionForms(t *testing.T) {
	// click_action.rs's from_str cases.
	if action := config.ParseClickAction(""); action.Kind != config.ClickNone {
		t.Errorf("empty = %+v, want none", action)
	}
	if action := config.ParseClickAction("dropdown:battery"); action.Kind != config.ClickDropdown || action.Dropdown != "battery" {
		t.Errorf("dropdown = %+v", action)
	}
	if action := config.ParseClickAction("brightness:5"); action.Kind != config.ClickBrightness || action.Brightness != 5 {
		t.Errorf("brightness delta = %+v", action)
	}
	if action := config.ParseClickAction("brightness:-5"); action.Kind != config.ClickBrightness || action.Brightness != -5 {
		t.Errorf("negative delta = %+v", action)
	}
	if action := config.ParseClickAction("brightness:toggle"); action.Kind != config.ClickBrightnessToggle {
		t.Errorf("toggle = %+v", action)
	}
	if action := config.ParseClickAction("lys-shell audio output-mute"); action.Kind != config.ClickShell || action.Command != "lys-shell audio output-mute" {
		t.Errorf("shell = %+v", action)
	}
	// Round trip.
	for _, raw := range []string{"", "dropdown:battery", "brightness:-5", "brightness:toggle", "true"} {
		if got := config.ParseClickAction(raw).String(); got != raw {
			t.Errorf("round trip %q -> %q", raw, got)
		}
	}
	if got := config.ParseClickAction("brightness:louder"); got.Kind != config.ClickNone {
		t.Errorf("bad delta = %+v, want no action (click_action.rs from_str)", got)
	}
}

func TestModuleClickDefaultsMatchSchema(t *testing.T) {
	cfg := config.Defaults()
	if got := cfg.Battery.Clicks().LeftClick.String(); got != "dropdown:battery" {
		t.Errorf("battery left-click = %q", got)
	}
	if got := cfg.Brightness.Clicks().ScrollUp.String(); got != "brightness:5" {
		t.Errorf("brightness scroll-up = %q", got)
	}
	if got := cfg.Brightness.Clicks().ScrollDown.String(); got != "brightness:-5" {
		t.Errorf("brightness scroll-down = %q", got)
	}
	if got := cfg.Volume.Clicks().MiddleClick.String(); got != "lys-shell audio output-mute" {
		t.Errorf("volume middle-click = %q", got)
	}
	if got := cfg.Microphone.Clicks().MiddleClick.String(); got != "lys-shell audio input-mute" {
		t.Errorf("microphone middle-click = %q", got)
	}
	if got := cfg.Clock.Clicks().RightClick.String(); got != "dropdown:weather" {
		t.Errorf("clock right-click = %q", got)
	}
	if cfg.KeyboardInput.Clicks().LeftClick.Kind != config.ClickNone {
		t.Errorf("keyboard-input left-click = %+v, want none", cfg.KeyboardInput.Clicks().LeftClick)
	}
}

// recordedActions captures what the button dispatches.
type recordedActions struct {
	actions []config.ClickAction
}

func newWrappedModule(t *testing.T, binding config.ClickConfig) (*barButton, *recordedActions) {
	t.Helper()
	rec := &recordedActions{}
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	button := newBarButton(ctx, nil, "x")
	button.configure(batteryButton(cfg), binding, func(action config.ClickAction, _ bool) {
		rec.actions = append(rec.actions, action)
	})
	return button, rec
}

func TestBarButtonRoutesAllFiveBindings(t *testing.T) {
	binding := config.ClickConfig{
		LeftClick:   config.ParseClickAction("left"),
		MiddleClick: config.ParseClickAction("middle"),
		RightClick:  config.ParseClickAction("right"),
		ScrollUp:    config.ParseClickAction("up"),
		ScrollDown:  config.ParseClickAction("down"),
	}
	button, rec := newWrappedModule(t, binding)

	// The primary fires on the press, the Rust GestureClick's
	// connect_pressed; the release fires nothing more.
	router, p := treetest.Pointer(button, button, 200, 40)
	router.Press(widget.BTNLeft, p, 0)
	router.Release(widget.BTNLeft, p)
	router.Press(widget.BTNMiddle, p, 0)
	router.Press(widget.BTNRight, p, 0)
	router.Axis(0, -1, 0)
	router.Axis(0, 1, 0)
	if len(rec.actions) != 5 {
		t.Fatalf("dispatched %+v, want five", rec.actions)
	}
	for i, want := range []string{"left", "middle", "right", "up", "down"} {
		if got := rec.actions[i].Command; got != want {
			t.Errorf("action %d = %q, want %q", i, got, want)
		}
	}
	// A horizontal step (dy == 0) dispatches nothing.
	router.Axis(1, 0, 0)
	if len(rec.actions) != 5 {
		t.Errorf("dy=0 dispatched %+v", rec.actions)
	}
}

func TestUnboundBarButtonConsumesScroll(t *testing.T) {
	button, rec := newWrappedModule(t, config.ClickConfig{})
	behind := widget.NewBox(widget.Row, 0, 0)
	behind.Append(button, false)
	reached := 0
	behind.SetOnScroll(func(widget.ScrollEvent) bool { reached++; return true })
	router, p := treetest.Pointer(behind, button, 200, 40)
	// Scroll steps are always consumed (the Rust helper's
	// Propagation::Stop); the unbound action no-ops in run.
	router.Axis(0, 1, 0)
	if reached != 0 {
		t.Error("scroll passed through the bar button; content behind it scrolls")
	}
	router.Press(widget.BTNRight, p, 0)
	router.Press(widget.BTNLeft, p, 0)
	router.Release(widget.BTNLeft, p)
	if len(rec.actions) != 0 || button.bound() {
		t.Errorf("unbound button dispatched %+v", rec.actions)
	}
}

// The module wrapper: `module` plus the module's own type class sit on
// a box ABOVE the bar button, so ancestor selectors like
// `.media-disc button.bar-button ... image` match, the Rust factory's
// shape.
func TestModuleWrapsTheButtonInModuleClasses(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	row := itemsZone([]config.BarItem{{Module: "clock"}}, ctx)
	item := row.Children()[0].(*widget.Box).Children()[0].(*widget.Box)
	if !item.HasClass("module") || !item.HasClass("clock") {
		t.Fatalf("wrapper classes %v, want module + clock above the button", item.Classes())
	}
	type hasClass interface{ HasClass(string) bool }
	if b, ok := item.Children()[0].(hasClass); !ok || !b.HasClass("bar-button") {
		t.Errorf("the wrapper holds %T, want the bar button", item.Children()[0])
	}
}

// shownIcon is the module icon a bar button shows in its tree, nil
// for none. The button lays out again where it is (at its natural size
// when it never was), which links its current parts to it.
func shownIcon(b *barButton) *widget.Icon {
	r := b.Bounds()
	if r.Empty() {
		size := b.Measure(widget.Constraints{Max: widget.Size{W: 1000, H: 1000}})
		r = render.Rect{W: size.W, H: size.H}
	}
	b.Arrange(r)
	if b.icon == nil || (!widget.DescendsFrom(b.icon, b) && !widget.DescendsFrom(b.icon, &b.Button)) {
		return nil
	}
	return b.icon
}

// The module button is lys-ui's BarButton, its icon and label the
// button's own: the image beside the label in the button's row, each
// carrying the class the bar-button CSS styles.
func TestBarButtonTreeAndClasses(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	icon := widget.NewThemeIcon("x-symbolic", 16)
	b := newBarButton(ctx, icon, "42%")
	b.configure(batteryButton(cfg), config.ClickConfig{}, nil)
	host := widget.NewBox(widget.Row, 0, 0).Append(b, false)
	host.Measure(widget.Constraints{Max: widget.Size{W: 400, H: 40}})
	host.Arrange(render.Rect{W: 400, H: 40})
	if b.Element() != "button" || !b.HasClass("bar-button") {
		t.Fatalf("the module button is %q %v, not lys-ui's button.bar-button", b.Element(), b.Classes())
	}
	if shownIcon(b) != icon || !icon.HasClass("bar-button-icon") || icon.Element() != "image" {
		t.Error("the module's icon is not the button's image.bar-button-icon")
	}
	if label := b.Label(); label.Text() != "42%" || !label.HasClass("bar-button-label") || label.Color() != 0 {
		t.Errorf("label %q %v ink %#08x, want the module's text as label.bar-button-label in the stylesheet's ink",
			label.Text(), label.Classes(), uint32(label.Color()))
	}
	if ib, lb := icon.Bounds(), b.Label().Bounds(); ib.X >= lb.X {
		t.Errorf("icon at %d, label at %d: want the icon first", ib.X, lb.X)
	}
	for _, c := range []string{"bar-button", cfg.Bar.ButtonVariant.CSSClass()} {
		if !b.HasClass(c) {
			t.Errorf("missing %q in %v", c, b.Classes())
		}
	}
	for _, c := range []string{"icon-only", "label-only", "vertical", "icon-end", "border-all"} {
		if b.HasClass(c) {
			t.Errorf("default button carries %q", c)
		}
	}
	if !strings.Contains(b.InlineStyle(), "--bar-btn-label-color: var(--yellow)") {
		t.Errorf("inline vars = %q, want the battery's label color", b.InlineStyle())
	}

	// The modifiers follow the config: variant, hidden label/icon, the
	// border class, the icon position.
	cfg.Bar.ButtonVariant = config.ButtonBlockPrefix
	cfg.Bar.ButtonIconPosition = config.IconEnd
	bc := batteryButton(cfg)
	bc.LabelShow, bc.IconShow, bc.BorderShow = false, false, true
	v := newBarButton(newTestContext(t, cfg), widget.NewThemeIcon("x", 16), "x")
	v.configure(bc, config.ClickConfig{}, nil)
	for _, c := range []string{"block-prefix", "icon-only", "label-only", "icon-end", "border-all"} {
		if !v.HasClass(c) {
			t.Errorf("modified button misses %q in %v", c, v.Classes())
		}
	}
	if !strings.Contains(v.InlineStyle(), "--bar-btn-icon-color: var(--fg-on-accent)") {
		t.Errorf("block-prefix auto icon color = %q, want fg-on-accent", v.InlineStyle())
	}
}

// button-icon-position end puts the label before the icon; start, the
// default, the icon before the label.
func TestBarButtonIconPosition(t *testing.T) {
	for _, at := range []config.IconPosition{config.IconStart, config.IconEnd} {
		cfg := config.Defaults()
		cfg.Bar.ButtonIconPosition = at
		icon := widget.NewThemeIcon("x", 16)
		b := newBarButton(newTestContext(t, cfg), icon, "12:30")
		host := widget.NewBox(widget.Row, 0, 0).Append(b, false)
		host.Measure(widget.Constraints{Max: widget.Size{W: 400, H: 40}})
		host.Arrange(render.Rect{W: 400, H: 40})
		iconFirst := icon.Bounds().X < b.Label().Bounds().X
		if iconFirst != (at == config.IconStart) {
			t.Errorf("icon position %s: icon first %v", at, iconFirst)
		}
	}
}

// icon-show and label-show drop the icon and the label from the button;
// a blank label shows none either, and a label with text shows again.
func TestBarButtonShowsOnlyWhatTheConfigShows(t *testing.T) {
	cfg := config.Defaults()
	icon := widget.NewThemeIcon("x", 16)
	b := newBarButton(newTestContext(t, cfg), icon, " ")
	b.configure(batteryButton(cfg), config.ClickConfig{}, nil)
	if b.Label().Visible() {
		t.Error("a blank label shows")
	}
	b.SetLabel("50%")
	if !b.Label().Visible() || b.Label().Text() != "50%" {
		t.Errorf("a label with text shows %q visible %v", b.Label().Text(), b.Label().Visible())
	}
	if shownIcon(b) != icon {
		t.Error("icon-show on dropped the icon")
	}
	hidden := batteryButton(cfg)
	hidden.IconShow, hidden.LabelShow = false, false
	b.configure(hidden, config.ClickConfig{}, nil)
	if shownIcon(b) != nil || b.Label().Visible() {
		t.Errorf("icon-show and label-show off: icon shown %v, label visible %v", shownIcon(b) != nil, b.Label().Visible())
	}
	b.configure(batteryButton(cfg), config.ClickConfig{}, nil)
	if shownIcon(b) != icon || b.Label().Text() != "50%" {
		t.Error("turning icon-show and label-show back on did not restore the icon and the label")
	}
	bare := newBarButton(newTestContext(t, cfg), nil, "x")
	if shownIcon(bare) != nil {
		t.Error("a button without an icon shows one")
	}
}

// label-max-length caps the label at that many characters with an
// ellipsis; zero leaves it whole.
func TestBarButtonLabelMaxLength(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), nil, strings.Repeat("w", 40))
	capped := batteryButton(cfg)
	capped.LabelMaxLength = 4
	b.configure(capped, config.ClickConfig{}, nil)
	short := b.Label().Measure(widget.Constraints{Max: widget.Size{W: 2000, H: 100}}).W
	if b.Label().MaxWidthChars() != 4 || b.Label().Ellipsize() != widget.EllipsizeEnd {
		t.Errorf("capped: %d chars, ellipsize %v; want 4, end", b.Label().MaxWidthChars(), b.Label().Ellipsize())
	}
	b.configure(batteryButton(cfg), config.ClickConfig{}, nil)
	whole := b.Label().Measure(widget.Constraints{Max: widget.Size{W: 2000, H: 100}}).W
	if b.Label().Ellipsize() != widget.EllipsizeNone || whole <= short {
		t.Errorf("uncapped: ellipsize %v, %dpx against the capped %dpx", b.Label().Ellipsize(), whole, short)
	}
}

func TestBarButtonThresholdsOverrideTheColors(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), nil, "x")
	b.configure(batteryButton(cfg), config.ClickConfig{}, nil)
	before := b.InlineStyle()
	red := mustToken(config.TokenRed)
	b.SetThresholds(config.ThresholdColors{LabelColor: &red})
	if !strings.Contains(b.InlineStyle(), "--bar-btn-label-color: var(--red)") {
		t.Errorf("threshold label color = %q", b.InlineStyle())
	}
	b.SetThresholds(config.ThresholdColors{})
	if b.InlineStyle() != before {
		t.Error("clearing the thresholds did not restore the config colors")
	}
}

func TestLoadFileAppliesAndRejectsClickBindings(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	content := "modules:\n  battery:\n    left-click: \"brightness:toggle\"\n    scroll-down: \"dropdown:battery\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Battery.Clicks().LeftClick.Kind != config.ClickBrightnessToggle {
		t.Errorf("left-click = %+v", c.Battery.Clicks().LeftClick)
	}
	if c.Battery.Clicks().ScrollDown.Dropdown != "battery" {
		t.Errorf("scroll-down = %+v", c.Battery.Clicks().ScrollDown)
	}

	if err := os.WriteFile(path, []byte("modules:\n  battery:\n    scroll-up: \"brightness:sideways\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Every string parses: a bad delta is no action (click_action.rs).
	if c, err := config.LoadFile(path); err != nil || c.Battery.Clicks().ScrollUp.Kind != config.ClickNone {
		t.Errorf("bad delta: want no action and no error, got %v", err)
	}
}

// The real input path: lys-ui's Router hit-tests the tree and drives the
// hit leaf, so a routed left press must reach the binding and a hover
// must shade the button.
func TestBarButtonRoutesThroughTheRouter(t *testing.T) {
	binding := config.ClickConfig{LeftClick: config.ParseClickAction("left")}
	button, rec := newWrappedModule(t, binding)
	button.Measure(widget.Constraints{Max: widget.Size{W: 200, H: 50}})
	button.Arrange(render.Rect{X: 0, Y: 0, W: 100, H: 30})
	router := &widget.Router{Root: button}
	p := widget.Point{X: 10, Y: 10}
	router.Move(p)
	if !button.Hovered {
		t.Fatal("routed hover did not reach the button")
	}
	router.Press(widget.BTNLeft, p, 0)
	if !button.Pressed {
		t.Fatal("routed press did not reach the button")
	}
	router.Release(widget.BTNLeft, p)
	if len(rec.actions) != 1 || rec.actions[0].Command != "left" {
		t.Fatalf("routed left click dispatched %+v, want the left binding", rec.actions)
	}
}

// batteryButton is the battery module's bar-button view.
func batteryButton(cfg *config.Config) config.ButtonConfig {
	b, _ := cfg.ModuleButton("battery")
	return b
}

// configure and later refreshes replace only the classes the button
// itself derives; a class the module put on its button stays.
func TestBarButtonKeepsTheModulesClasses(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), widget.NewThemeIcon("x", 16), "x")
	b.AddClass("media-disc")
	b.configure(config.ButtonConfig{IconShow: true, LabelShow: false}, config.ClickConfig{}, nil)
	if !b.HasClass("media-disc") || !b.HasClass("bar-button") || !b.HasClass("icon-only") {
		t.Fatalf("after configure: %v", b.Classes())
	}
	b.configure(config.ButtonConfig{IconShow: true, LabelShow: true}, config.ClickConfig{}, nil)
	if b.HasClass("icon-only") {
		t.Error("a stale derived class survived the refresh")
	}
	if !b.HasClass("media-disc") {
		t.Error("the refresh dropped the module's class")
	}
}

// The bar button is named by its label for assistive technology, as
// GTK names the Rust shell's menu button; a button with no label has
// no name to give.
func TestBarButtonAccessibleNameIsItsLabel(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), widget.NewThemeIcon("x", 16), "12:30")
	st := widget.Describe(b)
	if st.Role != widget.RoleButton || st.Name != "12:30" {
		t.Errorf("button = %s %q, want a button named by its label", st.Role, st.Name)
	}
	bare := newBarButtonAround(newTestContext(t, cfg), widget.NewBox(widget.Row, 0, 0))
	if got := widget.Describe(bare).Name; got != "" {
		t.Errorf("label-less button name = %q, want none", got)
	}
}

// While its dropdown is open the bar button is :checked and shaded, as
// a BarButton is under its own popover, and with dropdown-freeze-label
// its label holds still (FreezeSize), the module's updates landing on
// the close (pending_label); the close ends both. Without the freeze
// the label keeps following the module.
func TestBarButtonHoldsOpenWhileItsDropdownIs(t *testing.T) {
	for _, freeze := range []bool{true, false} {
		layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
		bar := buildRoot(styledContext(t, config.Defaults()), layout, "DP-1")
		b := clockButton(bar)
		b.SetLabel("12:30")
		settleBar(bar, widget.Size{W: 1000, H: 100})
		lb := b.Label().Bounds()
		at := widget.Point{X: lb.X + 1, Y: lb.Y + 1}
		rest := paintedAt(bar, 1000, 100, at)
		release := b.holdOpen(freeze)
		if open := paintedAt(bar, 1000, 100, at); !b.HasState(widget.StateChecked) || open == rest {
			t.Errorf("freeze %v: the open dropdown's button paints %#x, as at rest", freeze, uint32(open))
		}
		b.SetLabel("12:31")
		want := "12:31"
		if freeze {
			want = "12:30"
		}
		if got := b.Label().Text(); got != want {
			t.Errorf("freeze %v: the open button shows %q, want %q", freeze, got, want)
		}
		release()
		if closed := paintedAt(bar, 1000, 100, at); b.HasState(widget.StateChecked) || closed != rest {
			t.Errorf("freeze %v: the closed dropdown left the button checked, painting %#x", freeze, uint32(closed))
		}
		if got := b.Label().Text(); got != "12:31" {
			t.Errorf("freeze %v: the closed button shows %q, want the module's latest 12:31", freeze, got)
		}
	}
}
