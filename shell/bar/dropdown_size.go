package bar

import (
	"math"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// dropdownGeometry is a dropdown's built-in size, each Rust dropdown's
// BASE_WIDTH and BASE_HEIGHT at scale 1: a zero height grows to fit the
// content. size picks its [dropdowns.<name>] override; nil for a
// dropdown with none (the recorder).
type dropdownGeometry struct {
	width, height float64
	size          func(*config.DropdownsConfig) config.DropdownSize
}

// dropdownGeometries covers every sized dropdown; one absent here (the
// power menu) keeps its natural size.
var dropdownGeometries = map[string]dropdownGeometry{
	"audio":        {382, 512, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"volume":       {382, 512, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"microphone":   {382, 512, func(d *config.DropdownsConfig) config.DropdownSize { return d.Audio }},
	"battery":      {382, 312, func(d *config.DropdownsConfig) config.DropdownSize { return d.Battery }},
	"bluetooth":    {382, 512, func(d *config.DropdownsConfig) config.DropdownSize { return d.Bluetooth }},
	"brightness":   {320, 0, func(d *config.DropdownsConfig) config.DropdownSize { return d.Brightness }},
	"calendar":     {340, 0, func(d *config.DropdownsConfig) config.DropdownSize { return d.Calendar }},
	"dashboard":    {380, 0, func(d *config.DropdownsConfig) config.DropdownSize { return d.Dashboard }},
	"mail":         {300, 0, func(d *config.DropdownsConfig) config.DropdownSize { return d.Mail }},
	"media":        {380, 410, func(d *config.DropdownsConfig) config.DropdownSize { return d.Media }},
	"network":      {382, 512, func(d *config.DropdownsConfig) config.DropdownSize { return d.Network }},
	"notification": {425, 725, func(d *config.DropdownsConfig) config.DropdownSize { return d.Notification }},
	"recorder":     {360, 0, nil},
	"treeman":      {440, 460, func(d *config.DropdownsConfig) config.DropdownSize { return d.Treeman }},
	"weather":      {395, 695, func(d *config.DropdownsConfig) config.DropdownSize { return d.Weather }},
}

// dropdownDims resolves a dropdown's panel size in logical pixels
// (resolve_dimension and resolve_content_height): an override scales
// the base or sets pixels, no override is the base at the global
// scale. A content-height dropdown takes only a pixel height
// override; otherwise its height is -1, natural. ok is false for a
// dropdown with no geometry.
func dropdownDims(name string, cfg *config.Config) (w, h int, ok bool) {
	g, ok := dropdownGeometries[name]
	if !ok {
		return 0, 0, false
	}
	scale := float64(cfg.Styling.Scale)
	if scale <= 0 {
		scale = 1
	}
	var size config.DropdownSize
	if g.size != nil {
		size = g.size(&cfg.Dropdowns)
	}
	w = resolveDimension(size.Width, g.width, scale)
	switch {
	case g.height > 0:
		h = resolveDimension(size.Height, g.height, scale)
	case size.Height != nil && size.Height.Unit == config.SizePixels:
		h = int(math.Round(float64(size.Height.Value)))
	default:
		h = -1
	}
	return w, h, true
}

func resolveDimension(override *config.Size, base, scale float64) int {
	if override != nil {
		return int(math.Round(override.ResolvePx(base, scale)))
	}
	return int(math.Round(base * scale))
}

// panelBox gives a dropdown its panel, the Rust registry's overlay: the
// card fills the surface's width and takes its natural height, anchored
// at the bar's edge (the revealer's valign Start over the spacer), so a
// page switch resizes it, tweened by the stack's interpolate-size
// (dropdown_resize::animate_height). The configured size is the
// popover's own size request (dropdownTree), around the margins.
//
// A mapped popup cannot resize mid-grab without flicker, so the surface
// reserves, up front, the room every stack's tallest page needs
// (dropdown_resize::measure, the worst stack, not the sum): the card
// moves inside that fixed surface, and the transparent rest closes the
// dropdown on a click, as the Rust spacer's gesture does.
type panelBox struct {
	widget.Base
	child widget.Widget
	// maxH caps the surface and the card (the monitor less 100, the
	// registry's max_allowed_height); <= 0 takes the measure constraints.
	maxH int
	pop  popoverHandle
	// dismiss closes with the exit animation; the registry sets it per
	// open. Without it the popover just goes.
	dismiss func()
}

func newPanelBox(child widget.Widget) *panelBox {
	return &panelBox{child: child}
}

// The registry's clamp_height margins below the monitor height: the
// surface's, and the scrolled content's within it.
const (
	panelMonitorMargin = 100
	panelScrollMargin  = 80
)

// clamp is clamp_height for a monitor monH tall: the surface caps at
// the monitor less its margin, and every scroll the content holds now
// asks for its content's natural height up to the surface cap less the
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

// layout is the card's height and the surface's at width w: the card
// is the content's natural height; the surface adds the worst stack's
// shortfall - its neediest page against the height the stack is given
// now (surface_height).
func (p *panelBox) layout(w, maxH int) (card, surface int) {
	if p.maxH > 0 {
		maxH = min(maxH, p.maxH)
	}
	con := widget.Constraints{Min: widget.Size{W: w}, Max: widget.Size{W: w, H: maxH}}
	card = min(p.child.Measure(con).H, maxH)
	p.fit(w, card)
	reserve := 0
	walkPages(p.child, func(w widget.Widget) {
		if s, ok := w.(*widget.Stack); ok {
			reserve = max(reserve, s.PageFloor(con)-s.Bounds().H)
		}
	})
	return card, min(card+reserve, maxH)
}

// fit lays the content out in a w x h card at the origin.
func (p *panelBox) fit(w, h int) {
	p.child.Measure(widget.Constraints{Min: widget.Size{W: w, H: h}, Max: widget.Size{W: w, H: h}})
	p.child.Arrange(render.Rect{W: w, H: h})
}

func (p *panelBox) Measure(con widget.Constraints) widget.Size {
	// The Rust registry sizes the surface to the card's natural width;
	// the configured width floors the popover around it.
	nat := p.child.Measure(widget.Constraints{Max: widget.Size{W: con.Max.W}})
	w := min(max(con.Min.W, nat.W), con.Max.W)
	_, surface := p.layout(w, con.Max.H)
	return widget.Size{W: w, H: surface}
}

func (p *panelBox) Arrange(r render.Rect) {
	p.ArrangeSelf(r)
	widget.SetParents(p, p.child)
	h, _ := p.layout(r.W, r.H)
	// The card sits at the surface's top unconditionally (the Rust
	// revealer's valign Start): the fixed surface's transparent rest
	// hangs below, and closes the dropdown on a click.
	p.child.Measure(widget.Constraints{Min: widget.Size{W: r.W, H: h}, Max: widget.Size{W: r.W, H: h}})
	p.child.Arrange(render.Rect{X: r.X, Y: r.Y, W: r.W, H: h})
}

func (p *panelBox) Paint(cv *render.Canvas) { widget.PaintChild(cv, p.child) }

// Children exposes the content to the tree walks.
func (p *panelBox) Children() []widget.Widget { return []widget.Widget{p.child} }

// HitTest is the card's content, else the transparent rest.
func (p *panelBox) HitTest(pt widget.Point) widget.Widget {
	if hit := p.child.HitTest(pt); hit != nil {
		return hit
	}
	return p.HitLeaf(p, pt)
}

// ClickAt on the transparent rest closes the dropdown, playing its
// exit (dismiss_on_spacer_click).
func (p *panelBox) ClickAt(widget.Point) {
	switch {
	case p.dismiss != nil:
		p.dismiss()
	case p.pop != nil:
		p.pop.Dismiss()
	}
}

// dropdownCloser forwards to the content, so a sized dropdown still
// stops following its service when its instance is released.
func (p *panelBox) dropdownClosed() {
	if c, ok := p.child.(dropdownCloser); ok {
		c.dropdownClosed()
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
	p.pop = h
	if d, ok := p.child.(dropdownAttacher); ok {
		d.attachPopover(h)
	}
}
