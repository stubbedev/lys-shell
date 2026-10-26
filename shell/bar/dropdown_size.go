package bar

import (
	"github.com/stubbedev/lys-ui/render"
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
// scrolls (lys-ui's Shrinker), and only content that cannot scroll
// grows the panel past it. The monitor caps it outright.
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
	// max_allowed_height); <= 0 takes the measure constraints.
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

// heightAt is the panel's height at width w under maxH: the content's
// natural height, down to the configured height as far as its scrolls
// give way, and never past maxH.
func (p *panelBox) heightAt(w, maxH int) int {
	if p.maxH > 0 {
		maxH = min(maxH, p.maxH)
	}
	natural := p.child.Measure(widget.Constraints{Min: widget.Size{W: w}, Max: widget.Size{W: w, H: maxH}}).H
	h := natural
	if p.height > 0 {
		least := natural
		if s, ok := p.child.(widget.Shrinker); ok {
			least -= s.Shrinkable()
		}
		h = max(min(natural, p.height), least)
	}
	return min(h, maxH)
}

func (p *panelBox) Measure(con widget.Constraints) widget.Size {
	// GTK sizes a popover to its request or its content's preferred
	// width, whichever is wider: the configured width floors it,
	// unbroken content (a long device name) widens it, and wrapping
	// text wraps inside it instead.
	w := min(max(con.Min.W, p.width, widget.PreferredWidth(p.child)), con.Max.W)
	return widget.Size{W: w, H: p.heightAt(w, con.Max.H)}
}

func (p *panelBox) Arrange(r render.Rect) {
	p.ArrangeSelf(r)
	widget.SetParents(p, p.child)
	p.child.Measure(widget.Constraints{Min: widget.Size{W: r.W, H: r.H}, Max: widget.Size{W: r.W, H: r.H}})
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
