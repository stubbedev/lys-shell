package settings

import (
	"log"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/styling"
)

// hiddenPriority puts the reset button's hidden opacity above every
// stylesheet: Rust sets the widget's opacity, which no CSS overrides.
const hiddenPriority = apptheme.Priority + 1

// Row text limits (row/mod.rs).
const (
	descriptionMaxChars        = 120
	descriptionTooltipMinChars = 80
)

// kit builds the window's widgets: the text face every label starts
// from (the stylesheet then sizes and weighs it) and the store the
// controls write to.
type kit struct {
	face render.Font
	// mono and monoVariants are the code views' monospace face and its
	// bold and italic faces.
	mono         render.Font
	monoVariants widget.VariantFunc
	store        store
	// invoke runs a function on the loop (the slider's trailing commit).
	invoke func(func())
	// pickers opens popovers and dialogs; nil without a window.
	pickers pickers
	// background runs blocking work off the loop (a D-Bus query);
	// nil runs it in place, the tests' synchronous loop.
	background func(func())
}

// async runs work off the loop and hands its result to done on it.
func async[T any](k *kit, work func() T, done func(T)) {
	if k.background == nil {
		done(work())
		return
	}
	k.background(func() {
		v := work()
		k.invoke(func() { done(v) })
	})
}

// palette is the render palette the stored styling resolves to, the
// colors the token swatches show; a provider that fails is logged and
// leaves the configured palette.
func (k *kit) palette() styling.Palette {
	p, err := styling.ConfigPalette(k.store.svc.Config().Styling)
	if err != nil {
		log.Printf("settings: palette: %v", err)
	}
	return *p
}

// textPx is lys-ui's text size, which every label and control of the
// window takes.
func (k *kit) textPx() float64 { return widget.Current().TextSize }

// label is a styled label; color, size and weight come from the
// stylesheet.
func (k *kit) label(text string, classes ...string) *widget.Label {
	l := widget.NewLabel(k.face, k.textPx(), text, 0)
	l.AddClass(classes...)
	return l
}

// entry is a text field sized the way a GtkEntry is: a fixed width
// the text pans inside, whatever it holds.
func (k *kit) entry() *widget.Entry {
	e := widget.NewEntry(k.face, k.textPx(), 0)
	e.SetTextWidth(widget.GTKTextWidth)
	return e
}

// searchField is a picker's search entry: an entry showing the
// placeholder key's text that hands every edit to onChange.
func (k *kit) searchField(placeholder string, onChange func(query string)) *widget.Entry {
	e := k.entry()
	e.SetPlaceholder(i18n.Settings().Get(placeholder))
	e.OnChanged = onChange
	return e
}

// icon is a theme icon the stylesheet sizes and tints (an image node).
func (k *kit) icon(name string, classes ...string) *widget.Icon {
	ic := widget.NewThemeIcon(name, 16)
	ic.AddClass(classes...)
	return ic
}

// gapBox lines widgets up along axis a theme's spacing apart, the one
// gap between the controls of a group, the fields of a form and the
// rows of a list.
func gapBox(axis widget.Axis) *widget.Box {
	return widget.NewBox(axis, widget.Current().Spacing, 0)
}

// browseButton is the outline folder button asking the file chooser,
// pick getting the chosen path.
func (k *kit) browseButton(pick func(path string)) *widget.Button {
	return k.button(k.icon("ld-folder-open-symbolic"), func() {
		if k.pickers != nil {
			k.pickers.openFile(pick)
		}
	}, "browse", css.Outline, css.Icon)
}

// button wraps child in a plain button carrying classes.
func (k *kit) button(child widget.Widget, onClick func(), classes ...string) *widget.Button {
	b := widget.NewButton(child, 0, 0)
	b.AddClass(classes...)
	b.OnClick = onClick
	return b
}

// badge is lys-ui's quiet badge reading text; a status class
// (success, warning, error, info) tints it.
func (k *kit) badge(text string, classes ...string) *widget.Badge {
	b := widget.NewBadge(k.face, text)
	b.SetVariant(widget.BadgeSecondary)
	b.AddClass(classes...)
	return b
}

// badgeStatuses are the status classes a badge's tint comes from.
var badgeStatuses = []string{"success", "warning", "error", "info"}

// control is a row's editor: its widget, and refresh re-reading the
// value after the config changed.
type control interface {
	widget.Widget
	refresh()
}

// settingRow is SettingRow: the label with its unit and source badge,
// the description, the reset button and the control.
type settingRow struct {
	*widget.Box
	kit   *kit
	path  string
	ctl   control
	badge *widget.Badge
	reset *widget.Button
	// action is the row's RowBehavior::Action.
	action bool
}

// newSettingRow lays a row out (row/mod.rs view): a full-width control
// stacks under the text, with the reset button beside the label.
func newSettingRow(k *kit, spec rowSpec, ctl control) *settingRow {
	t := i18n.Settings()
	axis := widget.Row
	if spec.fullWidth {
		axis = widget.Column
	}
	th := widget.Current()
	r := &settingRow{kit: k, path: spec.path, ctl: ctl, action: spec.action, Box: gapBox(axis)}
	r.SetElement("item")
	r.AddClass(css.Small, "setting-row")

	info := widget.NewBox(widget.Column, th.Spacing/4, 0)
	labelRow := widget.NewBox(widget.Row, th.Spacing/2, 0)
	labelRow.AppendAligned(k.label(t.Get(spec.labelKey()), "setting-label"), false, widget.AlignCenter)
	if spec.unit != "" {
		labelRow.AppendAligned(k.label("("+spec.unit+")", "setting-unit", css.Caption, css.DimLabel, css.Monospace), false, widget.AlignCenter)
	}
	r.badge = k.badge("")
	labelRow.Append(r.badge, false)
	if d, ok := ctl.(interface{ dirtyBadge() *widget.Badge }); ok {
		labelRow.Append(d.dirtyBadge(), false)
	}
	info.Append(labelRow, false)
	if desc := t.Attr(spec.labelKey(), "description"); desc != "" {
		dl := k.label(desc, "setting-description", css.Caption, css.DimLabel)
		dl.SetEllipsize(widget.EllipsizeEnd)
		dl.SetMaxWidthChars(descriptionMaxChars)
		if len(desc) > descriptionTooltipMinChars {
			dl.SetTooltip(desc)
		}
		info.Append(dl, false)
	}

	r.reset = k.button(k.icon("ld-rotate-ccw-symbolic"), func() { k.store.reset(spec.path) }, "setting-reset", css.Ghost, css.Icon)
	slot := widget.NewBox(widget.Row, 0, 0)
	slot.AddClass("setting-control")
	slot.AppendAligned(ctl, spec.fullWidth, widget.AlignCenter)

	if spec.fullWidth {
		r.AddClass("vertical")
		labelRow.Append(widget.NewSpacer(0, 0), true)
		labelRow.Append(r.reset, false)
		r.Append(info, false)
		r.Append(slot, false)
	} else {
		r.AppendAligned(info, true, widget.AlignCenter)
		r.AppendAligned(r.reset, false, widget.AlignCenter)
		r.AppendAligned(slot, false, widget.AlignCenter)
	}
	r.refreshSource()
	return r
}

// refresh re-reads the source and the control's value.
func (r *settingRow) refresh() {
	r.refreshSource()
	r.ctl.refresh()
}

// refreshSource shows the badge and arms the reset button
// (update_source_info): the button keeps its place, invisible and
// inert, while there is no runtime override.
func (r *settingRow) refreshSource() {
	src := sourceInfo{}
	if !r.action {
		src = sourceOf(r.kit.store, r.path)
	}
	r.badge.SetVisible(src.badge)
	r.badge.SetText(src.label)
	r.badge.SetTooltip(src.tooltip)
	r.badge.RemoveClass(badgeStatuses...)
	if src.class != "" {
		r.badge.AddClass(src.class)
	}
	r.reset.SetEnabled(src.reset)
	if src.reset {
		r.reset.SetInlineStyle("")
	} else {
		r.reset.SetInlineStylePriority("opacity: 0;", hiddenPriority)
	}
}
