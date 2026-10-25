package bar

import (
	"strings"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// barButton is a module's button: lys-ui's BarButton with the module's
// icon and label as its own, so the bar-button CSS styles it —
//
//	button.bar-button.<variant>[.icon-only][.label-only][.vertical][.border-*][.icon-end]
//	└ box
//	  ├ image.bar-button-icon
//	  └ label.bar-button-label
//
// (the label first with button-icon-position end), with the module's
// colors injected as the per-button custom properties
// (styling.ButtonCSS) on the button. The module writes its label
// through SetLabel, which the button shows unless label-show is off,
// the text is blank or the label is frozen. The button is the router's
// hit leaf, so the five click and scroll bindings route through it.
type barButton struct {
	*widget.BarButton

	// icon is the module's icon, nil for none and for a content
	// button; iconShown is whether it sits in the button.
	icon      *widget.Icon
	iconShown bool
	// text is the module's latest label; frozen holds the shown one
	// still while a dropdown is open.
	text   string
	frozen bool

	cfg        *config.Config
	button     config.ButtonConfig
	thresholds config.ThresholdColors

	binding config.ClickConfig
	onRun   func(action config.ClickAction, scroll bool)
	// ownClasses are the classes refresh last applied; classes a module
	// sets on its button (media-disc, the threshold states) are left
	// alone.
	ownClasses []string
}

// newBarButton builds a module's bar button of its icon (nil for none)
// and its label text (empty for none yet).
func newBarButton(ctx ModuleContext, icon *widget.Icon, label string) *barButton {
	if icon != nil {
		icon.AddClass("bar-button-icon")
	}
	b := newBarButtonOf(ctx, nil)
	b.icon, b.text = icon, label
	b.refresh()
	return b
}

// newBarButtonAround wraps arbitrary module content (a canvas, a row of
// its own) in the same button, in the icon's place.
func newBarButtonAround(ctx ModuleContext, content widget.Widget) *barButton {
	b := newBarButtonOf(ctx, content)
	b.refresh()
	return b
}

// newBarButtonOf builds the lys-ui bar button around lead, its icon
// slot, on the bar's icon side.
func newBarButtonOf(ctx ModuleContext, lead widget.Widget) *barButton {
	b := &barButton{cfg: ctx.Config, BarButton: widget.NewBarButton(ctx.Font, lead, "")}
	b.Label().AddClass("bar-button-label")
	if ctx.Config != nil {
		b.button = config.ButtonConfig{IconShow: true, LabelShow: true, AutoIconColor: config.TokenFgDefault}
		if ctx.Config.Bar.ButtonIconPosition == config.IconEnd {
			b.SetIconPosition(widget.IconEnd)
		}
	}
	b.SetOnPointer(b.pointer)
	b.SetOnScroll(b.scroll)
	return b
}

// SetLabel is the module's label text: shown at once, or on thaw while
// the label is frozen (pending_label).
func (b *barButton) SetLabel(text string) {
	b.text = text
	b.showLabel()
}

// showLabel puts the module's text on the button unless the label is
// frozen; label-show off or a blank text shows none, which drops the
// label and its gap.
func (b *barButton) showLabel() {
	if b.frozen {
		return
	}
	shown := b.text
	if !b.button.LabelShow || strings.TrimSpace(shown) == "" {
		shown = ""
	}
	b.BarButton.SetLabel(shown)
}

// showIcon puts the module's icon in the button while icon-show is on.
func (b *barButton) showIcon() {
	show := b.icon != nil && b.button.IconShow
	if show == b.iconShown {
		return
	}
	b.iconShown = show
	if show {
		b.SetIcon(b.icon)
		return
	}
	b.SetIcon(nil)
}

// holdOpen marks the button while its dropdown is open: :checked, as a
// BarButton stays while its own popover is, and with freezeLabel
// (dropdown-freeze-label) its label held still so the popover stays
// anchored (FreezeSize). The returned func ends both, showing what the
// module set meanwhile.
func (b *barButton) holdOpen(freezeLabel bool) func() {
	b.SetState(widget.StateChecked, true)
	b.frozen = freezeLabel
	return func() {
		b.SetState(widget.StateChecked, false)
		b.frozen = false
		b.showLabel()
	}
}

// configure applies a module's button config and bindings; appendModule
// calls it once it knows which module the button belongs to.
func (b *barButton) configure(button config.ButtonConfig, binding config.ClickConfig, onRun func(action config.ClickAction, scroll bool)) {
	b.button, b.binding, b.onRun = button, binding, onRun
	// label-max-length is BarButtonBehavior's label_max_chars: the
	// label's width caps at that many characters and ellipsizes there.
	b.Label().SetMaxWidthChars(button.LabelMaxLength)
	if button.LabelMaxLength > 0 {
		b.Label().SetEllipsize(widget.EllipsizeEnd)
	} else {
		b.Label().SetEllipsize(widget.EllipsizeNone)
	}
	b.refresh()
}

// SetThresholds applies threshold color overrides over the config
// colors (BarButtonInput::SetThresholdColors): the per-button variables
// rebuild when they changed.
func (b *barButton) SetThresholds(t config.ThresholdColors) {
	if thresholdsEqual(t, b.thresholds) {
		return
	}
	b.thresholds = t
	b.refresh()
}

// applyThresholds evaluates value against the module's thresholds
// (evaluate_thresholds) and applies the matching colors.
func (b *barButton) applyThresholds(value float64, entries []config.ThresholdEntry) {
	b.SetThresholds(config.EvaluateThresholds(value, entries))
}

// refresh recomputes the classes, the per-button variables, and which
// of the icon and label show.
func (b *barButton) refresh() {
	b.showIcon()
	b.showLabel()
	if b.cfg == nil {
		return
	}
	b.RemoveClass(b.ownClasses...)
	b.ownClasses = b.classes()
	b.AddClass(b.ownClasses...)
	b.SetInlineStyle(inlineDecls(styling.ButtonCSS(b.button, b.cfg.Bar, b.cfg.Styling.ColorExtractor.ThemeProvider, b.thresholds)))
}

// classes is BarButton::css_classes beyond lys-ui's bar-button and
// vertical: the variant and the modifiers.
func (b *barButton) classes() []string {
	bar := b.cfg.Bar
	out := []string{bar.ButtonVariant.CSSClass()}
	if !b.button.LabelShow {
		out = append(out, "icon-only")
	}
	if !b.button.IconShow {
		out = append(out, "label-only")
	}
	if b.button.BorderShow {
		if c, ok := bar.ButtonBorderLocation.CSSClass(); ok {
			out = append(out, c)
		}
	}
	if c, ok := bar.ButtonIconPosition.CSSClass(); ok {
		out = append(out, c)
	}
	return out
}

// run fires one binding; scroll marks the scroll-up/down ones (the custom
// module debounces its on-action for those).
func (b *barButton) run(action config.ClickAction, scroll bool) {
	if action.Kind == config.ClickNone || b.onRun == nil {
		return
	}
	b.onRun(action, scroll)
}

// bound reports whether any of the five bindings is set.
func (b *barButton) bound() bool {
	c := b.binding
	return c.LeftClick.Kind != config.ClickNone || c.MiddleClick.Kind != config.ClickNone ||
		c.RightClick.Kind != config.ClickNone || c.ScrollUp.Kind != config.ClickNone ||
		c.ScrollDown.Kind != config.ClickNone
}

// HitTest keeps the bar button the hit leaf, so the router drives its
// hover, press, and click, and the non-primary hooks below.
func (b *barButton) HitTest(p widget.Point) widget.Widget {
	if b.BarButton.HitTest(p) != nil {
		return b
	}
	return nil
}

// SetPressed fires the primary binding on the press, the Rust
// GestureClick's connect_pressed: the dropdown appears under the
// button-down, and a press that slides off before the release still
// opened it. The release's click opens nothing: the button has no
// popover of its own, the binding opens the dropdown.
func (b *barButton) SetPressed(on bool) {
	was := b.Pressed
	b.BarButton.SetPressed(on)
	if on && !was {
		b.run(b.binding.LeftClick, false)
	}
}

// pointer runs the middle and right bindings and consumes those
// presses; the primary goes on to the button's press.
func (b *barButton) pointer(e widget.PointerEvent) bool {
	switch e.Button {
	case widget.BTNMiddle:
		b.run(b.binding.MiddleClick, false)
	case widget.BTNRight:
		b.run(b.binding.RightClick, false)
	default:
		return false
	}
	return true
}

// scroll maps vertical steps to the up/down bindings, positive DY
// scrolling down. The step is always consumed, the Rust helper's
// Propagation::Stop: an unbound action no-ops in run, and content
// behind the bar never scrolls through a module.
func (b *barButton) scroll(e widget.ScrollEvent) bool {
	switch {
	case e.DY > 0:
		b.run(b.binding.ScrollDown, true)
	case e.DY < 0:
		b.run(b.binding.ScrollUp, true)
	default:
		return false
	}
	return true
}

// inlineDecls strips a `selector { ... }` rule down to its declaration
// list: the Rust per-widget providers are `* { ... }` rules, which in
// lys-ui are the widget's inline declarations.
func inlineDecls(rule string) string {
	open, end := strings.IndexByte(rule, '{'), strings.LastIndexByte(rule, '}')
	if open < 0 || end < open {
		return rule
	}
	return strings.TrimSpace(rule[open+1 : end])
}

// thresholdsEqual compares two override sets by value.
func thresholdsEqual(a, b config.ThresholdColors) bool {
	eq := func(x, y *config.ColorValue) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	}
	return eq(a.IconColor, b.IconColor) && eq(a.LabelColor, b.LabelColor) &&
		eq(a.IconBgColor, b.IconBgColor) && eq(a.ButtonBgColor, b.ButtonBgColor) &&
		eq(a.BorderColor, b.BorderColor)
}
