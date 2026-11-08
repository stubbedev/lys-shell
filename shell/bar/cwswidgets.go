package bar

import (
	"slices"
	"strconv"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// newCwsRow builds the workspaces row, lys-ui's SegmentIndicator,
// which paints the box workspaces.css styles —
//
//	segments.workspaces.<kind>[.border-*]    (the --ws-* variables inline)
//	├ indicator.indicator-<kind>[.vertical]  (the travelling active mark)
//	└ button.segment.workspace.<state>[...]  (--ws-override-color inline)
//	  └ [box (workspace-padding), for content past the circle]
//	    └ box.workspace-content
//	      ├ box: label.workspace-label, the mapped icon, label.workspace-divider
//	      └ box.workspace-icons > image.workspace-icon...
//
// lys-ui stacks the segments down a side bar. A primary click on a
// segment is OnSelect; the middle and right clicks and the wheel bind
// on each segment.
func newCwsRow(m *cwsView) {
	m.segs = widget.NewSegmentIndicator(0)
	m.segs.OnSelect = func(i int) { m.dispatchClick(m.cfg.Click.LeftClick, m.shown[i].ws) }
	m.segs.AddClass("workspaces")
	if m.kind != "" {
		m.segs.AddClass(m.kind)
	}
	bar := m.ctx.Config.Bar
	if c, ok := bar.ButtonBorderLocation.CSSClass(); ok && m.cfg.BorderShow {
		m.segs.AddClass(c)
	}
	m.circle = m.measureCircle()
	m.segs.SetInlineStyle(inlineDecls(styling.WorkspacesCSS(m.cfg, m.otherMonitor, bar)) + " --ws-circle: " + strconv.Itoa(m.circle) + "px;")
	m.preview = newWindowPreview(m.ctx, revealHoldOf(m.ctx))
	m.ctx.OnRetire(m.preview.stop)
	drop := m.segs.EnableDrop()
	drop.AddClass(m.cfg.ActiveIndicator.CSSClass())
	drop.SetClass("vertical", m.vertical())
}

func (m *cwsView) measureCircle() int {
	digit := m.measureLabel("8")
	return max(m.measureLabel("88").W, digit.W+2*m.paddingPx(), digit.H, m.iconPx())
}

func (m *cwsView) measureLabel(text string) widget.Size {
	label := widget.NewLabel(m.boldFace, m.labelPx(), text, 0)
	width := label.Measure(widget.Row, widget.Unknown).Nat
	return widget.Size{W: width, H: label.Measure(widget.Column, width).Nat}
}

type cwsSegmentShown struct {
	classes  []string
	override string
	hasContent,
	showLabel, showIcon, showDivider bool
	label, icon string
	appNames    []string
	appIcons    []*widget.Icon
}

func (c *cwsSegmentShown) sameContent(model cwsButtonModel, cfg config.CompositorWorkspacesConfig) bool {
	return c.hasContent && c.label == model.label && c.icon == model.icon &&
		c.showLabel == model.showLabel(cfg.DisplayMode) && c.showIcon == model.showIcon(cfg.DisplayMode) && c.showDivider == model.showDivider(cfg) &&
		slices.EqualFunc(c.appNames, model.appIcons, func(name string, app cwsAppIcon) bool { return name == app.name })
}

// setSegments shows one segment per model: the active one through
// SetActive, or ShiftActive for the same workspace at a new index, its
// occupancy through SetSegmentState and its urgency
// through SetSegmentUrgent, then the model's classes, the
// workspace-map color, and its content. A new segment takes its
// handlers; what a kept one already holds stays.
func (m *cwsView) setSegments(models []cwsButtonModel) {
	s := m.segs
	previous, hadActive := m.shownActive()
	kept := min(s.Count(), len(models))
	s.SetCount(len(models))
	m.segShown = append(m.segShown[:kept], make([]cwsSegmentShown, len(models)-kept)...)
	active, activeOverride := -1, ""
	for i, model := range models {
		seg := s.Segment(i)
		shown := &m.segShown[i]
		s.SetSegmentState(i, cwsSegmentState(model))
		s.SetSegmentUrgent(i, hasClass(model.classes, "urgent"))
		classes := slices.DeleteFunc(slices.Clone(model.classes), segmentOwned)
		if !slices.Equal(shown.classes, classes) {
			seg.RemoveClass(shown.classes...)
			seg.AddClass(classes...)
			shown.classes = classes
		}
		override := ""
		if color, ok := cwsOverrideColor(model.classes, m.cfg.WorkspaceMap); ok {
			override = "--ws-override-color: " + color.ToCSS() + ";"
		}
		if hasClass(model.classes, "active") {
			active, activeOverride = i, override
		}
		if shown.override != override {
			seg.SetInlineStyle(override)
			shown.override = override
		}
		if i >= kept {
			seg.SetOnPointer(func(e widget.PointerEvent) bool { return m.pointer(i, e) })
			seg.SetOnScroll(m.scroll)
			seg.SetOnHoverWithin(func(on bool) { m.hoverSegment(i, seg, on) })
		}
		if shown.sameContent(model, m.cfg) {
			for j, app := range model.appIcons {
				shown.appIcons[j].SetClass("urgent", app.urgent)
			}
			continue
		}
		s.SetSegmentContent(i, m.segmentContent(model, shown))
	}
	if active >= 0 && hadActive && models[active].ws.id == previous {
		s.ShiftActive(active)
	} else {
		s.SetActive(active)
	}
	m.dressDrop(models, active, activeOverride)
	m.shown = models
}

func (m *cwsView) shownActive() (uint64, bool) {
	i := m.segs.Active()
	if i < 0 {
		return 0, false
	}
	return m.shown[i].ws.id, true
}

func (m *cwsView) dressDrop(models []cwsButtonModel, active int, override string) {
	var classes []string
	if active >= 0 {
		classes = models[active].classes
	}
	drop := m.segs.EnableDrop()
	drop.SetInlineStyle(override)
	drop.SetClass("urgent", hasClass(classes, "urgent") && !hasClass(classes, "urgent-application"))
	drop.SetClass("special", hasClass(classes, "special"))
}

// segmentOwned reports a state class lys-ui's SegmentIndicator sets
// itself from SetActive, SetSegmentState and SetSegmentUrgent.
func segmentOwned(class string) bool {
	return class == "active" || class == "occupied" || class == "urgent"
}

// cwsSegmentState is the occupancy a model's classes mark.
func cwsSegmentState(model cwsButtonModel) widget.SegmentState {
	if hasClass(model.classes, "occupied") {
		return widget.SegmentOccupied
	}
	return widget.SegmentEmpty
}

// segmentContent is the button view!: the identity row (label, mapped
// icon, divider) and the app-icon box; lys-ui centers it in the
// segment. Content that fits the circle stands alone. Content past it,
// app icons, a divider or a long name, takes workspace-padding along
// the bar and so makes the segment a pill of the same end radius.
// Their colors are the stylesheet's. It records what it built in shown.
func (m *cwsView) segmentContent(model cwsButtonModel, shown *cwsSegmentShown) widget.Widget {
	cfg := m.cfg
	shown.hasContent, shown.label, shown.icon = true, model.label, model.icon
	shown.showLabel, shown.showIcon, shown.showDivider = model.showLabel(cfg.DisplayMode), model.showIcon(cfg.DisplayMode), model.showDivider(cfg)
	shown.appNames, shown.appIcons = shown.appNames[:0], shown.appIcons[:0]
	vertical := m.vertical()
	axis := widget.Row
	if vertical {
		axis = widget.Column
	}
	content := widget.NewBox(axis, 0, 0)
	content.AddClass("workspace-content")

	labelPx := m.labelPx()
	fits := !cfg.AppIconsShow
	if model.showLabel(cfg.DisplayMode) || model.showIcon(cfg.DisplayMode) || model.showDivider(cfg) {
		identity := widget.NewBox(widget.Row, 0, 0)
		if model.showLabel(cfg.DisplayMode) {
			label := widget.NewLabel(m.boldFace, labelPx, model.label, 0)
			label.AddClass("workspace-label")
			identity.Append(label, false)
			fits = fits && m.measureLabel(model.label).W <= m.circle
		}
		if model.showIcon(cfg.DisplayMode) {
			icon := widget.NewThemeIcon(model.icon, m.iconPx())
			if model.flavor == cwsHyprland {
				icon.AddClass("workspace-custom-icon")
			} else {
				icon.AddClass("workspace-icon")
			}
			identity.Append(icon, false)
		}
		if model.showDivider(cfg) {
			divider := widget.NewLabel(m.boldFace, labelPx, cfg.Divider, 0)
			divider.AddClass("workspace-divider")
			identity.Append(divider, false)
		}
		content.Append(identity, false)
	}
	if cfg.AppIconsShow {
		icons := widget.NewBox(axis, m.iconGapPx(), 0)
		icons.AddClass("workspace-icons")
		if len(model.appIcons) == 0 {
			icon := widget.NewThemeIcon(cfg.AppIconsEmpty, m.iconPx())
			icon.AddClass("workspace-icon", "workspace-icon-empty")
			icons.Append(icon, false)
		}
		for _, app := range model.appIcons {
			icon := widget.NewThemeIcon(app.name, m.iconPx())
			icon.AddClass("workspace-icon")
			icon.SetClass("urgent", app.urgent)
			icons.Append(icon, false)
			shown.appNames, shown.appIcons = append(shown.appNames, app.name), append(shown.appIcons, icon)
		}
		content.Append(icons, false)
	}
	if fits {
		return content
	}
	pad := m.paddingPx()
	padded := widget.NewBox(widget.Row, 0, 0)
	padded.Append(content, true)
	if vertical {
		padded.SetPadding(render.Insets{Top: pad, Bottom: pad})
	} else {
		padded.SetPadding(render.Insets{Left: pad, Right: pad})
	}
	return padded
}

// pointer is segment i's middle/right GestureClick pair; the primary
// goes on to OnSelect.
func (m *cwsView) pointer(i int, e widget.PointerEvent) bool {
	switch e.Button {
	case widget.BTNMiddle:
		m.dispatchClick(m.cfg.Click.MiddleClick, m.shown[i].ws)
	case widget.BTNRight:
		m.dispatchClick(m.cfg.Click.RightClick, m.shown[i].ws)
	default:
		return false
	}
	return true
}

// scroll is attach_scroll: discrete vertical steps, always consumed
// (Propagation::Stop).
func (m *cwsView) scroll(e widget.ScrollEvent) bool {
	switch {
	case e.DY > 0:
		m.dispatchScroll(m.cfg.Click.ScrollDown)
	case e.DY < 0:
		m.dispatchScroll(m.cfg.Click.ScrollUp)
	}
	return true
}

// Attach keeps the bar surface the window previews open from.
func (m *cwsView) Attach(host app.Host) { m.preview.Attach(host) }

// hoverSegment previews segment i's workspace once the pointer rests on
// it.
func (m *cwsView) hoverSegment(i int, seg widget.Boundser, on bool) {
	if !on || m.previewOf == nil || i >= len(m.shown) {
		m.preview.hover(false, seg, nil)
		return
	}
	ws := m.shown[i].ws
	m.preview.hover(true, seg, func() ([]previewWindow, previewActions) {
		return m.previewOf(ws), &compositorActions{backend: modes.DetectWindows()}
	})
}

// revealHoldOf keeps ctx's bar revealed while a preview of it shows.
func revealHoldOf(ctx ModuleContext) func() func() {
	if ctx.Dropdowns == nil {
		return nil
	}
	return func() func() { return ctx.Dropdowns.holdReveal(ctx.Connector) }
}
