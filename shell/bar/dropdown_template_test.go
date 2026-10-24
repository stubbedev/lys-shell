package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The empty state centers itself at its natural size in whatever page
// holds it (the template's set_expand and set_align Center), and its
// icon and text center inside it; it never stretches to the page.
func TestEmptyStateCentersInItsPage(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	font, px := dropdownFont(ctx)
	col, glyph, desc := templateEmptyState(font, px, "ld-play-symbolic", nil, "No Media Playing", "Start playing media in any app to control it here")
	page := widget.NewBox(widget.Column, 0, 0)
	page.Append(col, true)
	ctx.Theme.Attach(page)
	page.Measure(widgetConstraintsMax(600, 400))
	page.Arrange(render.Rect{W: 600, H: 400})

	nat := col.Measure(widgetConstraintsMax(600, 400))
	cb := boundsOf(col)
	if cb.W != nat.W || cb.H != nat.H {
		t.Errorf("the empty state spans %+v, want its natural %+v (it stretched to the page)", cb, nat)
	}
	if d := midX(cb) - 300; d < -1 || d > 1 {
		t.Errorf("the empty state sits %dpx off the page's horizontal center", d)
	}
	if d := midY(cb) - 200; d < -1 || d > 1 {
		t.Errorf("the empty state sits %dpx off the page's vertical center", d)
	}
	if d := midX(boundsOf(glyph)) - midX(cb); d < -1 || d > 1 {
		t.Errorf("the icon sits %dpx off the state's center", d)
	}
	for _, l := range treetest.All[*widget.Label](col) {
		if l.Alignment() != render.AlignCenter {
			t.Errorf("label %q aligns %v, want its text centered", l.Text(), l.Alignment())
		}
	}
	if desc.Wrap() || desc.MaxWidthChars() != 0 {
		t.Error("the template wraps its description; only the brightness dropdown asks for that")
	}
}
