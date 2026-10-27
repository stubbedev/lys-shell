package bar

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// dropdownGeometry is a dropdown's built-in width, each Rust
// dropdown's BASE_WIDTH at scale 1; its height is its content's.
// size picks its [dropdowns.<name>] override; nil for a dropdown with
// none (the recorder).
type dropdownGeometry struct {
	width float64
	size  func(*config.DropdownsConfig) config.DropdownSize
}

// dropdownGeometries covers every sized dropdown; one absent here (the
// power menu) keeps its natural size.
var dropdownGeometries = map[string]dropdownGeometry{
	"audio":        {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"volume":       {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"microphone":   {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"battery":      {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Battery }},
	"bitwarden":    {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Bitwarden }},
	"bluetooth":    {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Bluetooth }},
	"brightness":   {320, func(d *config.DropdownsConfig) config.DropdownSize { return d.Brightness }},
	"calendar":     {340, func(d *config.DropdownsConfig) config.DropdownSize { return d.Calendar }},
	"dashboard":    {380, func(d *config.DropdownsConfig) config.DropdownSize { return d.Dashboard }},
	"mail":         {300, func(d *config.DropdownsConfig) config.DropdownSize { return d.Mail }},
	"media":        {380, func(d *config.DropdownsConfig) config.DropdownSize { return d.Media }},
	"network":      {382, func(d *config.DropdownsConfig) config.DropdownSize { return d.Network }},
	"notification": {425, func(d *config.DropdownsConfig) config.DropdownSize { return d.Notification }},
	"recorder":     {360, nil},
	"treeman":      {440, func(d *config.DropdownsConfig) config.DropdownSize { return d.Treeman }},
	"weather":      {395, func(d *config.DropdownsConfig) config.DropdownSize { return d.Weather }},
}

// dropdownDims resolves a dropdown's panel size in logical pixels: the
// width is the base at the global scale, which an override scales
// further or replaces with pixels; the height is a pixel override's,
// the height its lists scroll at, else -1, its content's. ok is false
// for a dropdown with no geometry.
func dropdownDims(name string, cfg *config.Config) (w, h int, ok bool) {
	g, ok := dropdownGeometries[name]
	if !ok {
		return 0, 0, false
	}
	var size config.DropdownSize
	if g.size != nil {
		size = g.size(&cfg.Dropdowns)
	}
	return config.ResolveDimension(size.Width, g.width, float64(cfg.Styling.Scale)), config.ResolveContentHeight(size.Height), true
}

// panelBox gives a dropdown its panel: the configured width and the
// height of its content, so the content's own padding is the card's
// inset on every side, and a page switch resizes the popover, which
// lys-ui springs to the new size. The height is measured at the width
// the panel is laid out at, so text wrapping at a narrower one never
// leaves the card taller than what it shows. A configured height caps
// the panel: content taller than it gives the difference up from its
// scrolls (down to its minimum height), and only content that cannot
// scroll grows the panel past it. The monitor caps it outright.
type panelBox struct {
	widget.Base
	child widget.Widget
	// width floors the panel's width, as GTK's popover width request
	// does; <= 0 leaves it to the content.
	width int
	// height is the configured height the scrolls give way to; <= 0
	// lets the content grow to the monitor.
	height int
	// maxH caps the card (the monitor less 100, the registry's
	// max_allowed_height); <= 0 leaves it to the host.
	maxH int
}

func newPanelBox(child widget.Widget, width, height int) *panelBox {
	return &panelBox{child: child, width: width, height: height}
}

// The registry's clamp_height margins below the monitor height: the
// card's, and the scrolled content's within it.
const (
	panelMonitorMargin = 100
	panelScrollMargin  = 80
)

// clamp is clamp_height for a monitor monH tall: the card caps at the
// monitor less its margin, and every scroll the content holds now asks
// for its content's natural height up to the card cap less the
// scrolled margin (find_and_clamp_scrolled_windows), so a list sizes
// the card instead of scrolling inside a fixed one.
func (p *panelBox) clamp(monH int) {
	p.maxH = monH - panelMonitorMargin
	scrollMax := p.maxH - panelScrollMargin
	walkPages(p.child, func(w widget.Widget) {
		if s, ok := w.(*widget.Scroll); ok {
			s.PropagateNaturalHeight = true
			s.SetMaxContentHeight(scrollMax)
		}
	})
}

// capWrapping gives every wrapping label in content, every page
// included, the max-width-chars that holds its natural width to the
// panel's width less the content's insets, so wrapped text asks for no
// more than the panel's width (GTK apps cap a wrapping label's natural
// width the same way). Labels added later are capped on the next
// measure. A panel with no width leaves its labels uncapped.
func capWrapping(content widget.Widget, width int) {
	if width <= 0 {
		return
	}
	room := width - 2*dropdownInset()
	walkPages(content, func(w widget.Widget) {
		switch l := w.(type) {
		case *widget.Label:
			if l.Wrap() {
				l.SetMaxWidthChars(l.CharsWithin(room))
			}
		case *widget.RichLabel:
			if l.Wrap() {
				l.SetMaxWidthChars(l.CharsWithin(room))
			}
		}
	})
}

// walkPages visits w and everything under it, every page of a stack
// included (the Rust walks follow GTK's child list, where a stack's
// hidden pages are children too; a lys-ui Stack lists only its visible
// page).
func walkPages(w widget.Widget, visit func(widget.Widget)) {
	visit(w)
	if s, ok := w.(*widget.Stack); ok {
		for _, name := range s.Order() {
			walkPages(s.Page(name), visit)
		}
		return
	}
	if c, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, k := range c.Children() {
			walkPages(k, visit)
		}
	}
}

// Measure sizes the panel as GTK sizes a popover: across, its width
// request or its content's natural width, whichever is wider, so
// content that cannot narrow (a long device name) widens it, while
// wrapping text, its natural width capped to fit the width (capWrapping),
// wraps inside it; down, the content's natural height at the width given, down to
// the configured height as far as its scrolls give way, and never past
// maxH.
func (p *panelBox) Measure(axis widget.Axis, forSize int) widget.Sizes {
	widget.SetParents(p, p.child)
	if axis == widget.Row {
		capWrapping(p.child, p.width)
		req := p.child.Measure(widget.Row, widget.Unknown)
		return widget.Sizes{Min: req.Min, Nat: max(p.width, req.Nat)}
	}
	req := p.child.Measure(widget.Column, forSize)
	h := req.Nat
	if p.height > 0 {
		h = max(min(req.Nat, p.height), req.Min)
	}
	if p.maxH > 0 {
		h = min(h, p.maxH)
	}
	return widget.FixedSizes(h)
}

func (p *panelBox) Arrange(r render.Rect) {
	p.ArrangeSelf(r)
	widget.SetParents(p, p.child)
	p.child.Arrange(r)
}

func (p *panelBox) Paint(cv *render.Canvas) { widget.PaintChild(cv, p.child) }

// Children exposes the content to the tree walks.
func (p *panelBox) Children() []widget.Widget { return []widget.Widget{p.child} }

// HitTest is the content's.
func (p *panelBox) HitTest(pt widget.Point) widget.Widget { return p.child.HitTest(pt) }

// dropdownCloser forwards to the content, so a sized dropdown still
// stops following its service when its instance is released.
func (p *panelBox) dropdownClosed() {
	if c, ok := p.child.(dropdownCloser); ok {
		c.dropdownClosed()
	}
}

// dropdownFocus forwards the content's initial focus.
func (p *panelBox) dropdownFocus() widget.Widget {
	if f, ok := p.child.(dropdownFocuser); ok {
		return f.dropdownFocus()
	}
	return nil
}

// dropdownHidden forwards each close of the popover to the content.
func (p *panelBox) dropdownHidden() {
	if h, ok := p.child.(dropdownHider); ok {
		h.dropdownHidden()
	}
}

// dropdownOpened forwards each open to the content.
func (p *panelBox) dropdownOpened() {
	if o, ok := p.child.(dropdownOpener); ok {
		o.dropdownOpened()
	}
}

// attachPopover forwards the popover handle to content that acts on
// its popover.
func (p *panelBox) attachPopover(h popoverHandle) {
	if d, ok := p.child.(dropdownAttacher); ok {
		d.attachPopover(h)
	}
}
