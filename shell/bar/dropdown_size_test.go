package bar

import (
	"testing"

	"github.com/stubbedev/gelm/render"
	"github.com/stubbedev/gelm/widget"
)

// pagedPanel is a w=200 panel over a column holding one expanding page
// stack (sized to its visible page) with a 100px page and a tall one.
func pagedPanel(h int, tall widget.Widget) (*panelBox, *widget.Stack) {
	s := widget.NewStack().Add("short", widget.NewSpacer(10, 100)).Add("tall", tall)
	s.SetHomogeneous(false)
	col := widget.NewBox(widget.Column, 0, 0)
	col.Append(s, true)
	return newPanelBox(200, h, col), s
}

func panelLayout(p *panelBox) (surface int, card render.Rect) {
	sz := p.Measure(widget.Constraints{Max: widget.Size{W: 1000, H: 2000}})
	p.Arrange(render.Rect{W: sz.W, H: sz.H})
	return sz.H, p.child.(interface{ Bounds() render.Rect }).Bounds()
}

// The card is its visible page's natural height, anchored at the top;
// the surface reserves the tallest page.
func TestPanelCardTakesItsPageAndTheSurfaceReservesTheTallest(t *testing.T) {
	p, s := pagedPanel(300, widget.NewSpacer(10, 500))
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

// The configured height is the popover's height request: it floors the
// surface, never the card, which stays its content's height (the Rust
// audio dropdown's 512 leaves a ~300px card and a transparent rest).
func TestPanelConfiguredHeightFloorsOnlyTheSurface(t *testing.T) {
	p, _ := pagedPanel(600, widget.NewSpacer(10, 500))
	surface, card := panelLayout(p)
	if surface != 600 || card.H != 100 {
		t.Errorf("surface %d card %+v, want the 600 floor and the 100 card", surface, card)
	}
}

// Clamped to a monitor, every scroll propagates its content's natural
// height up to the scrolled cap (find_and_clamp_scrolled_windows): a
// list sizes the card instead of scrolling inside a fixed one, and
// scrolls only past the cap.
func TestClampedPanelScrollsSizeTheCard(t *testing.T) {
	list := widget.NewScroll(widget.NewSpacer(10, 400))
	p, s := pagedPanel(300, list)
	p.clamp(1000)
	s.Show("tall")
	if surface, card := panelLayout(p); card.H != 400 || surface != 400 {
		t.Errorf("a 400px list: surface %d card %+v, want both 400", surface, card)
	}
	long := widget.NewScroll(widget.NewSpacer(10, 2000))
	p, s = pagedPanel(300, long)
	p.clamp(1000)
	s.Show("tall")
	if _, card := panelLayout(p); card.H != 1000-panelMonitorMargin-panelScrollMargin {
		t.Errorf("a 2000px list: card %+v, want the scrolled cap", card)
	}
}

func TestContentSizedPanelFollowsTheVisiblePage(t *testing.T) {
	p, s := pagedPanel(-1, widget.NewSpacer(10, 500))
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
	p, _ := pagedPanel(300, widget.NewSpacer(10, 500))
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
	p, _ := pagedPanel(300, widget.NewSpacer(10, 900))
	p.maxH = 400
	_, card := panelLayout(p)
	if card.H > 400 {
		t.Errorf("card %+v, want it capped at 400", card)
	}
}

// The configured width is a floor: content wider than it widens the
// surface, the Rust registry's natural-width sizing.
func TestPanelWidthFloorsAtTheConfig(t *testing.T) {
	wide := widget.NewBox(widget.Row, 0, 0)
	wide.Append(widget.NewSpacer(500, 20), false)
	p := newPanelBox(300, 0, wide)
	sz := p.Measure(widget.Constraints{Max: widget.Size{W: 600, H: 1000}})
	if sz.W != 500 {
		t.Errorf("panel width %d, want the content's 500 over the config's 300", sz.W)
	}
}
