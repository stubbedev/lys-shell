package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
)

// pagedPanel is a panel over a 200px-wide column holding one expanding
// page stack (sized to its visible page) with a 100px page and a tall
// one.
func pagedPanel(tall widget.Widget) (*panelBox, *widget.Stack) {
	s := widget.NewStack().Add("short", widget.NewSpacer(200, 100)).Add("tall", tall)
	s.SetHomogeneous(false)
	col := widget.NewBox(widget.Column, 0, 0)
	col.Append(s, true)
	return newPanelBox(col), s
}

func panelLayout(p *panelBox) (surface int, card render.Rect) {
	sz := p.Measure(widget.Constraints{Max: widget.Size{W: 1000, H: 2000}})
	p.Arrange(render.Rect{W: sz.W, H: sz.H})
	return sz.H, p.child.(interface{ Bounds() render.Rect }).Bounds()
}

// The card is its visible page's natural height, anchored at the top;
// the surface reserves the tallest page.
func TestPanelCardTakesItsPageAndTheSurfaceReservesTheTallest(t *testing.T) {
	p, s := pagedPanel(widget.NewSpacer(10, 500))
	surface, card := panelLayout(p)
	if surface != 500 || card.H != 100 || card.Y != 0 {
		t.Errorf("short page: surface %d card %+v, want 500 reserved and the 100 page's card at the top", surface, card)
	}
	s.Show("tall")
	surface, card = panelLayout(p)
	if surface != 500 || card.H != 500 {
		t.Errorf("tall page: surface %d card %+v, want the card grown to 500 in the same surface", surface, card)
	}
}

// Clamped to a monitor, every scroll propagates its content's natural
// height up to the scrolled cap (find_and_clamp_scrolled_windows): a
// list sizes the card instead of scrolling inside a fixed one, and
// scrolls only past the cap.
func TestClampedPanelScrollsSizeTheCard(t *testing.T) {
	list := widget.NewScroll(widget.NewSpacer(10, 400))
	p, s := pagedPanel(list)
	p.clamp(1000)
	s.Show("tall")
	if surface, card := panelLayout(p); card.H != 400 || surface != 400 {
		t.Errorf("a 400px list: surface %d card %+v, want both 400", surface, card)
	}
	long := widget.NewScroll(widget.NewSpacer(10, 2000))
	p, s = pagedPanel(long)
	p.clamp(1000)
	s.Show("tall")
	if _, card := panelLayout(p); card.H != 1000-panelMonitorMargin-panelScrollMargin {
		t.Errorf("a 2000px list: card %+v, want the scrolled cap", card)
	}
}

func TestContentSizedPanelFollowsTheVisiblePage(t *testing.T) {
	p, s := pagedPanel(widget.NewSpacer(10, 500))
	surface, card := panelLayout(p)
	if surface != 500 || card.H != 100 {
		t.Errorf("content-sized: surface %d card %+v, want 500 reserved and the 100 page", surface, card)
	}
	s.Show("tall")
	if _, card = panelLayout(p); card.H != 500 {
		t.Errorf("content-sized tall: card %+v, want 500", card)
	}
}

func TestPanelAnchorsAtTheTopAndTheRestCloses(t *testing.T) {
	p, _ := pagedPanel(widget.NewSpacer(10, 500))
	_, card := panelLayout(p)
	if card.Y != 0 {
		t.Errorf("card %+v, want it anchored at the surface's top like the Rust revealer's valign Start", card)
	}
	pop := &fakePopover{}
	p.attachPopover(pop)
	// The reserved surface is taller than the visible card: the rest
	// below it is the transparent close target.
	if hit := p.HitTest(widget.Point{X: 10, Y: card.Y + card.H + 50}); hit != widget.Widget(p) {
		t.Fatalf("the transparent rest hit %T, want the panel", hit)
	}
	p.ClickAt(widget.Point{X: 10, Y: card.Y + card.H + 50})
	if pop.dismissed != 1 {
		t.Errorf("a click on the rest dismissed %d times, want once", pop.dismissed)
	}
	if hit := p.HitTest(widget.Point{X: 10, Y: card.Y + card.H/2}); hit == widget.Widget(p) {
		t.Error("a click on the card fell through to the panel")
	}
}

// The card's height caps at the panel's monitor-derived ceiling even
// when the measure constraints are looser.
func TestPanelCapsAtTheMonitorCeiling(t *testing.T) {
	p, _ := pagedPanel(widget.NewSpacer(10, 900))
	p.maxH = 400
	_, card := panelLayout(p)
	if card.H > 400 {
		t.Errorf("card %+v, want it capped at 400", card)
	}
}

// The configured size is the popover's own size request, margins
// included (the Rust popover's width and height requests around the
// registry's margins): content smaller than it leaves the card at the
// request less the margins and a transparent rest; content wider than
// it widens the popover.
func TestDropdownSizeFloorsThePopoverAroundItsMargins(t *testing.T) {
	cfg := config.Defaults()
	m := newDropdownMargins(float64(cfg.Styling.Scale), cfg.Bar.Location)
	con := widget.Constraints{Max: widget.Size{W: 2000, H: 2000}}

	p, _ := pagedPanel(widget.NewSpacer(10, 50))
	root, _ := dropdownTree(p, cfg, 382, 512)
	// The bundle's `* { all: unset }` must not take the margins with it.
	isolateConfigDir(t)
	apptheme.New(cfg).Attach(root)
	sz := widget.MeasureAsChild(root, con)
	if sz != (widget.Size{W: 382, H: 512}) {
		t.Fatalf("popover %+v, want the 382x512 request", sz)
	}
	widget.ArrangeAsChild(root, render.Rect{W: sz.W, H: sz.H})
	if got := p.child.(interface{ Bounds() render.Rect }).Bounds(); got.W != 382-m.start-m.end || got.H != 100 {
		t.Errorf("card %+v, want %d wide (the request less the margins) and its 100px content tall", got, 382-m.start-m.end)
	}

	wide := widget.NewBox(widget.Row, 0, 0)
	wide.Append(widget.NewSpacer(500, 20), false)
	root, _ = dropdownTree(newPanelBox(wide), cfg, 382, 0)
	apptheme.New(cfg).Attach(root)
	if sz := widget.MeasureAsChild(root, con); sz.W != 500+m.start+m.end {
		t.Errorf("popover width %d, want the 500px content plus the margins", sz.W)
	}
}
