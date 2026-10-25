package bar

import (
	"slices"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/styling"
)

// newCwsRow builds the workspaces row, lys-ui's SegmentIndicator in
// the container box workspaces.css paints, which a SegmentIndicator
// does not paint itself —
//
//	box.workspaces.<kind>[.border-*]          (the --ws-* variables inline)
//	└ segments
//	  └ button.segment.workspace.<state>[...]  (--ws-override-color inline)
//	    └ box (workspace-padding)
//	      └ box.workspace-content
//	        ├ box: label.workspace-label, the mapped icon, label.workspace-divider
//	        └ box.workspace-icons > image.workspace-icon...
//
// lys-ui stacks the segments down a side bar. A primary click on a
// segment is OnSelect; the middle and right clicks and the wheel bind
// on each segment.
func newCwsRow(m *cwsView) {
	m.segs = widget.NewSegmentIndicator(0)
	m.segs.OnSelect = func(i int) { m.dispatchClick(m.cfg.Click.LeftClick, m.shown[i].ws) }
	axis := widget.Row
	if m.vertical() {
		axis = widget.Column
	}
	m.root = widget.NewBox(axis, 0, 0)
	m.root.AddClass("workspaces")
	if m.kind != "" {
		m.root.AddClass(m.kind)
	}
	bar := m.ctx.Config.Bar
	if c, ok := bar.ButtonBorderLocation.CSSClass(); ok && m.cfg.BorderShow {
		m.root.AddClass(c)
	}
	m.root.SetInlineStyle(inlineDecls(styling.WorkspacesCSS(m.cfg, m.otherMonitor, bar)))
	m.root.Append(m.segs, false)
}

// setSegments shows one segment per model: the active one through
// SetActive, its occupancy and urgency through SetSegmentState, then
// the model's classes (an urgent segment keeps its occupied state
// class beside lys-ui's urgent one), the workspace-map color, and its
// content.
func (m *cwsView) setSegments(models []cwsButtonModel) {
	s := m.segs
	s.SetCount(len(models))
	for len(m.segClasses) < len(models) {
		m.segClasses = append(m.segClasses, nil)
	}
	active := -1
	for i, model := range models {
		seg := s.Segment(i)
		seg.RemoveClass(m.segClasses[i]...)
		s.SetSegmentState(i, cwsSegmentState(model))
		m.segClasses[i] = slices.DeleteFunc(slices.Clone(model.classes), func(c string) bool { return c == "active" })
		seg.AddClass(m.segClasses[i]...)
		if hasClass(model.classes, "active") {
			active = i
		}
		override := ""
		if color, ok := cwsOverrideColor(model.classes, m.cfg.WorkspaceMap); ok {
			override = "--ws-override-color: " + color.ToCSS() + ";"
		}
		seg.SetInlineStyle(override)
		seg.SetOnPointer(func(e widget.PointerEvent) bool { return m.pointer(i, e) })
		seg.SetOnScroll(m.scroll)
		s.SetSegmentContent(i, m.segmentContent(model))
	}
	s.SetActive(active)
	m.shown = models
}

// cwsSegmentState is the segment state a model's classes mark: urgent
// while its urgency shows, else occupied in the occupied state.
func cwsSegmentState(model cwsButtonModel) widget.SegmentState {
	switch {
	case hasClass(model.classes, "urgent"):
		return widget.SegmentUrgent
	case hasClass(model.classes, "occupied"):
		return widget.SegmentOccupied
	}
	return widget.SegmentEmpty
}

// segmentContent is the button view!: the identity row (label, mapped
// icon, divider) and the app-icon box, padded by workspace-padding
// along the bar and centered in the segment. Their colors are the
// stylesheet's.
func (m *cwsView) segmentContent(model cwsButtonModel) widget.Widget {
	cfg := m.cfg
	vertical := m.vertical()
	axis := widget.Row
	if vertical {
		axis = widget.Column
	}
	content := widget.NewBox(axis, 0, 0)
	content.AddClass("workspace-content")

	labelPx := m.labelPx()
	if model.showLabel(cfg.DisplayMode) || model.showIcon(cfg.DisplayMode) || model.showDivider(cfg) {
		identity := widget.NewBox(widget.Row, 0, 0)
		if model.showLabel(cfg.DisplayMode) {
			label := widget.NewLabel(m.boldFace, labelPx, model.label, 0)
			label.AddClass("workspace-label")
			identity.Append(label, false)
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
			if app.urgent {
				icon.AddClass("urgent")
			}
			icons.Append(icon, false)
		}
		content.Append(icons, false)
	}
	pad := m.paddingPx()
	padded := widget.NewBox(widget.Row, 0, 0)
	padded.SetHExpand(true)
	padded.SetHAlign(widget.AlignCenter)
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
