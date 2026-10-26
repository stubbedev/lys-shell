package bar

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// pagedPanel is a panel capped at height over a 200px-wide column
// holding one expanding page stack (sized to its visible page) with a
// 100px page and a tall one.
func pagedPanel(tall widget.Widget, height int) (*panelBox, *widget.Stack) {
	s := widget.NewStack().Add("short", widget.NewSpacer(200, 100)).Add("tall", tall)
	s.SetHomogeneous(false)
	col := widget.NewBox(widget.Column, 0, 0)
	col.Append(s, true)
	return newPanelBox(col, 0, height), s
}

// panelLayout measures and arranges p as the popover does, returning
// its height and its content's rect.
func panelLayout(p *panelBox) (height int, content render.Rect) {
	sz := p.Measure(widget.Constraints{Max: widget.Size{W: 1000, H: 2000}})
	p.Arrange(render.Rect{W: sz.W, H: sz.H})
	return sz.H, p.child.(widget.Boundser).Bounds()
}

// The card is its visible page's height, the panel no taller: nothing
// is reserved for a taller page, which grows the popover when it
// shows.
func TestPanelIsItsVisiblePage(t *testing.T) {
	p, s := pagedPanel(widget.NewSpacer(10, 500), 0)
	h, content := panelLayout(p)
	if h != 100 || content != (render.Rect{W: 200, H: 100}) {
		t.Errorf("short page: panel %d content %+v, want both the 100px page, no room reserved for the tall one", h, content)
	}
	s.Show("tall")
	if h, content = panelLayout(p); h != 500 || content.H != 500 {
		t.Errorf("tall page: panel %d content %+v, want both grown to 500", h, content)
	}
}

// Clamped to a monitor, every scroll propagates its content's natural
// height up to the scrolled cap (find_and_clamp_scrolled_windows): a
// list sizes the card instead of scrolling inside a fixed one, and
// scrolls only past the cap.
func TestClampedPanelScrollsSizeTheCard(t *testing.T) {
	list := dropdownScroll(widget.NewSpacer(10, 400), "")
	p, s := pagedPanel(list, 0)
	p.clamp(1000)
	s.Show("tall")
	if h, content := panelLayout(p); h != 400 || content.H != 400 {
		t.Errorf("a 400px list: panel %d content %+v, want both 400", h, content)
	}
	long := dropdownScroll(widget.NewSpacer(10, 2000), "")
	p, s = pagedPanel(long, 0)
	p.clamp(1000)
	s.Show("tall")
	if h, _ := panelLayout(p); h != 1000-panelMonitorMargin-panelScrollMargin {
		t.Errorf("a 2000px list: panel %d, want the scrolled cap", h)
	}
}

// The configured height caps the card where its lists can scroll: a
// list taller than it scrolls inside it, shorter content keeps the card
// at its own height instead of padding it out to the configured one,
// and content that cannot scroll grows the card past it.
func TestPanelHeightCapsWhereTheListsScroll(t *testing.T) {
	list := func(h int) *widget.Box {
		col := widget.NewBox(widget.Column, 0, 0)
		col.Append(widget.NewSpacer(10, 100), false)
		col.Append(dropdownScroll(widget.NewSpacer(10, h), ""), true)
		return col
	}
	long := newPanelBox(list(2000), 0, 300)
	long.clamp(1000)
	if h, content := panelLayout(long); h != 300 || content.H != 300 {
		t.Errorf("100px over a 2000px list capped at 300: panel %d content %+v, want both 300", h, content)
	}
	short := newPanelBox(list(50), 0, 300)
	short.clamp(1000)
	if h, _ := panelLayout(short); h != 150 {
		t.Errorf("100px over a 50px list capped at 300: panel %d, want its own 150", h)
	}
	fixed := newPanelBox(widget.NewSpacer(10, 690), 0, 512)
	fixed.clamp(1000)
	if h, _ := panelLayout(fixed); h != 690 {
		t.Errorf("690px that cannot scroll capped at 512: panel %d, want it grown to 690", h)
	}
}

// The card's height caps at the panel's monitor-derived ceiling even
// when the measure constraints are looser.
func TestPanelCapsAtTheMonitorCeiling(t *testing.T) {
	p, _ := pagedPanel(widget.NewSpacer(10, 900), 0)
	p.maxH = 400
	p.child.(*widget.Box).Children()[0].(*widget.Stack).Show("tall")
	if h, _ := panelLayout(p); h != 400 {
		t.Errorf("panel %d, want it capped at 400", h)
	}
}

// The configured width sizes the card under the popover's width cap,
// the card is as tall as its content inside the popover's border, and
// content wider than the width widens the card where nothing caps it.
// No margins ride around the card: the popover's gap and shadow gutter
// keep it off the bar.
func TestDropdownCardIsItsContent(t *testing.T) {
	cfg := config.Defaults()
	isolateConfigDir(t)

	s := widget.NewStack().Add("short", widget.NewSpacer(10, 100))
	p := newPanelBox(s, 382, 512)
	root, _ := dropdownTree(p, cfg)
	apptheme.New(cfg).Attach(root)
	sz := widget.MeasureAsChild(root, widget.Constraints{Max: widget.Size{W: 382, H: 2000}})
	if sz != (widget.Size{W: 382, H: 100 + 2*popoverBorder}) {
		t.Fatalf("card %+v, want the 382px width and the 100px content inside the border", sz)
	}
	widget.ArrangeAsChild(root, render.Rect{W: sz.W, H: sz.H})
	if got, want := p.child.(widget.Boundser).Bounds(), 382-2*popoverBorder; got.W != want || got.H != 100 {
		t.Errorf("card content %+v, want %d wide (the width less the popover's border) and its 100px content tall", got, want)
	}
	if got := root.Margin(); got != (render.Insets{}) {
		t.Errorf("card margins %+v, want none", got)
	}

	wide := widget.NewBox(widget.Row, 0, 0)
	wide.Append(widget.NewSpacer(500, 20), false)
	root, _ = dropdownTree(newPanelBox(wide, 382, 0), cfg)
	apptheme.New(cfg).Attach(root)
	if sz := widget.MeasureAsChild(root, widget.Constraints{Max: widget.Size{W: 2000, H: 2000}}); sz.W != 500+2*popoverBorder {
		t.Errorf("card width %d, want the 500px content plus the popover's border", sz.W)
	}
}

// popoverBorder is lys-ui's popover surface border width.
const popoverBorder = 1

// A panel is as wide as its width or its content's preferred width,
// GTK's popover sizing: long wrapping text wraps inside the width,
// while text that cannot narrow (an ellipsizing device name keeps its
// natural width) widens the popover past it.
func TestPanelWidthIsRequestOrPreferredWidth(t *testing.T) {
	face := treetest.Font(t)
	long := strings.Repeat("worktree daemon ", 8)
	panelWith := func(label *widget.Label) *panelBox {
		col := widget.NewBox(widget.Column, 0, 0)
		col.Append(label, false)
		return newPanelBox(col, 300, 0)
	}
	con := widget.Constraints{Max: widget.Size{W: 2000, H: 2000}}

	wrapping := widget.NewLabel(face, 13, long, 0)
	wrapping.SetWrap(true)
	if w := panelWith(wrapping).Measure(con).W; w != 300 {
		t.Errorf("a wrapping sentence widened the panel to %d, want the 300px width", w)
	}

	name := widget.NewLabel(face, 13, long, 0)
	name.SetEllipsize(widget.EllipsizeEnd)
	nat := name.Measure(widget.Constraints{Max: widget.Size{W: 2000, H: 100}}).W
	if w := panelWith(name).Measure(con).W; w != nat || w <= 300 {
		t.Errorf("an ellipsizing name sized the panel %d, want its %dpx natural width past the width", w, nat)
	}
}

// The panel's height is its content's at the width it is laid out at:
// text that wraps narrower than the panel's width never leaves the
// card taller than the lines it shows. Measured without the width, the
// same column is taller.
func TestPanelHeightIsTheContentsAtItsWidth(t *testing.T) {
	face := treetest.Font(t)
	column := func() *widget.Box {
		col := widget.NewBox(widget.Column, 0, 0)
		text := widget.NewLabel(face, 13, strings.Repeat("worktree daemon ", 4), 0)
		text.SetWrap(true)
		col.Append(text, false)
		return col
	}
	con := widget.Constraints{Max: widget.Size{W: 1000, H: 1000}}
	atWidth := column().Measure(widget.Constraints{Min: widget.Size{W: 380}, Max: widget.Size{W: 380, H: 1000}}).H
	if sz := newPanelBox(column(), 380, 0).Measure(con); sz.W != 380 || sz.H != atWidth {
		t.Errorf("panel %+v, want 380 wide and the %dpx the text takes at 380", sz, atWidth)
	}
	if bare := newPanelBox(column(), 0, 0).Measure(con); bare.H <= atWidth {
		t.Fatalf("the column measured at its preferred width is %dpx, no taller than the %dpx at 380: nothing to pin", bare.H, atWidth)
	}
}

// A dropdown without a height grows to fit its content even when that
// content sits in a scroll (the dashboard, mail): the scroll asks for
// its content's height. A plain dropdown scroll would ask for none.
func TestGrowingDropdownScrollAsksForItsContent(t *testing.T) {
	cfg := config.Defaults()
	isolateConfigDir(t)
	con := widget.Constraints{Max: widget.Size{W: 2000, H: 2000}}
	list := func() widget.Widget { return widget.NewSpacer(10, 700) }
	grown, _ := dropdownTree(newPanelBox(growingScroll(dropdownScroll(list(), "")), 380, 0), cfg)
	apptheme.New(cfg).Attach(grown)
	if sz := widget.MeasureAsChild(grown, con); sz.H < 700 {
		t.Errorf("a growing dropdown over a 700px scroll is %dpx tall, want its content's height", sz.H)
	}
	plain, _ := dropdownTree(newPanelBox(dropdownScroll(list(), ""), 380, 0), cfg)
	apptheme.New(cfg).Attach(plain)
	if sz := widget.MeasureAsChild(plain, con); sz.H >= 700 {
		t.Errorf("a plain dropdown scroll asked for %dpx, want none of its content's height", sz.H)
	}
}
