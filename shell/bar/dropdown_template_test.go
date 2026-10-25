package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The empty state is lys-ui's StatusPage: its icon sits on the page's
// center line, its labels center their text, and a long description
// wraps inside the page instead of widening it.
func TestEmptyStateCentersInItsPage(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	font, px := dropdownFont(ctx)
	state, glyph := emptyStateIcon(font, px, "ld-play-symbolic", "No Media Playing", "Start playing media in any app to control it here, or pick a player from the list")
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
