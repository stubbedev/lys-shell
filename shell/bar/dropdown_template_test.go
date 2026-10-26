package bar

import (
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// The empty state is lys-ui's StatusPage: its icon sits on the page's
// center line, its labels center their text, and a long description
// wraps inside the page instead of widening it.
func TestEmptyStateCentersInItsPage(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	font, px := dropdownFont(ctx)
	state, glyph := widgets.EmptyStateIcon(font, px, "ld-play-symbolic", "No Media Playing", "Start playing media in any app to control it here, or pick a player from the list")
	page := widget.NewBox(widget.Column, 0, 0)
	page.Append(state, true)
	ctx.Theme.Attach(page)
	const w = 240
	page.Measure(widgetConstraintsMax(w, 400))
	page.Arrange(render.Rect{W: w, H: 400})
	if d := midX(boundsOf(glyph)) - w/2; d < -1 || d > 1 {
		t.Errorf("the icon sits %dpx off the page's center", d)
	}
	labels := treetest.All[*widget.Label](state)
	if len(labels) != 2 {
		t.Fatalf("labels = %d, want the title and the description", len(labels))
	}
	for _, l := range labels {
		if l.Alignment() != render.AlignCenter {
			t.Errorf("label %q aligns %v, want its text centered", l.Text(), l.Alignment())
		}
		if b := boundsOf(l); b.X < 0 || b.X+b.W > w {
			t.Errorf("label %q spans %+v, past the %d page", l.Text(), b, w)
		}
	}
	desc := labels[1]
	if one := widget.MeasureAsChild(desc, widgetConstraintsMax(2000, 400)).H; boundsOf(desc).H <= one {
		t.Errorf("the description is one %dpx line in a %d page, want it wrapped", one, w)
	}
}

// A dropdown's content takes lys-ui's metrics under the stylesheet:
// its sections sit twice the theme's spacing apart, inset by twice its
// padding, and no shell rule replaces either.
func TestDropdownContentTakesTheThemeMetrics(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	content := dropdownContent("x-content")
	first, second := widget.NewSpacer(10, 20), widget.NewSpacer(10, 20)
	content.Append(first, false)
	content.Append(second, false)
	ctx.Theme.Attach(content)
	content.Measure(widgetConstraintsMax(300, 400))
	content.Arrange(render.Rect{W: 300, H: 400})
	th := widget.Current()
	inset := 2 * th.Padding
	if b := boundsOf(first); b.X != inset || b.Y != inset {
		t.Errorf("the first section sits at %d,%d, want the %dpx inset", b.X, b.Y, inset)
	}
	if gap := boundsOf(second).Y - (boundsOf(first).Y + boundsOf(first).H); gap != 2*th.Spacing {
		t.Errorf("the sections sit %dpx apart, want twice the theme's %d", gap, th.Spacing)
	}
	if !widget.HasClass(content, "dropdown-content") || !widget.HasClass(content, "x-content") {
		t.Error("the content lacks .dropdown-content or its own class")
	}
}

// The header is lys-ui's small Item over a separator: the actions are
// the item's own, the icon takes the accent, and the strip paints no
// fill of its own.
func TestDropdownHeaderIsAnItem(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	action := widget.NewSwitch(false)
	header, glyph, item := dropdownHeaderParts(ctx.Font, "ld-bluetooth-symbolic", "Bluetooth", action)
	ctx.Theme.Attach(header)
	header.Measure(widgetConstraintsMax(360, 200))
	header.Arrange(render.Rect{W: 360, H: 200})
	kids := header.Children()
	if len(kids) != 2 || kids[0] != widget.Widget(item) {
		t.Fatalf("header children = %d, want the item over a separator", len(kids))
	}
	if _, ok := kids[1].(*widget.Separator); !ok {
		t.Errorf("the header's second child is %T, want a separator", kids[1])
	}
	if !widget.HasClass(item, "small") {
		t.Error("the header item is not lys-ui's small item")
	}
	if action.Parent() == nil || !isUnder(action, item) {
		t.Error("the header action is not the item's own")
	}
	if got, want := widget.CascadeColor(glyph), ctx.Theme.RenderPalette().Primary; got != want {
		t.Errorf("the header icon inks %#x, want the accent %#x", uint32(got), uint32(want))
	}
	if bg := widget.StyleBackground(header); bg != 0 {
		t.Errorf("the header strip paints %#x, want no fill of its own", uint32(bg))
	}
}

// A section label is lys-ui's muted caption heading.
func TestSectionLabelIsTheCaptionHeading(t *testing.T) {
	l := sectionLabel(treetest.Font(t), "Devices")
	for _, class := range []string{"section-label", css.CaptionHeading, css.DimLabel} {
		if !l.HasClass(class) {
			t.Errorf("the section label lacks .%s", class)
		}
	}
}

// isUnder reports whether w sits in root's subtree.
func isUnder(w, root widget.Widget) bool {
	found := false
	treetest.Walk(root, func(x widget.Widget) { found = found || x == w })
	return found
}

// The dropdowns' text is lys-ui's text size times the styling scale,
// whatever size the bar's own labels take.
func TestDropdownFontIsTheToolkitTextSize(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	if _, px := dropdownFont(ctx); px != widget.Current().TextSize {
		t.Errorf("default text = %vpx, want lys-ui's %v", px, widget.Current().TextSize)
	}
	ctx.Style.labelPx *= 2
	if _, px := dropdownFont(ctx); px != widget.Current().TextSize {
		t.Errorf("a larger bar label moved the dropdown text to %vpx", px)
	}
	cfg.Styling.Scale = 1.5
	if _, px := dropdownFont(ctx); px != widget.Current().TextSize*1.5 {
		t.Errorf("scale 1.5 text = %vpx, want %v", px, widget.Current().TextSize*1.5)
	}
}

// A dropdown button's label keeps the text size it was built at: no
// shell rule resizes button text over lys-ui's.
func TestDropdownButtonTextKeepsItsSize(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	font, px := dropdownFont(ctx)
	label := widget.NewLabel(font, px, "Clear All", 0)
	content := dropdownContent()
	content.Append(dropdownButton(label, nil, css.Ghost, css.Small), false)
	ctx.Theme.Attach(content)
	content.Measure(widgetConstraintsMax(400, 200))
	content.Arrange(render.Rect{W: 400, H: 200})
	bare := widget.NewLabel(font, px, "Clear All", 0)
	big := widget.Constraints{Max: widget.Size{W: 400, H: 200}}
	if got, want := widget.MeasureAsChild(label, big).W, bare.Measure(big).W; got != want {
		t.Errorf("the button's label measures %dpx wide, want its own %dpx", got, want)
	}
}
