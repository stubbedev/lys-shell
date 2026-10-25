package widgets

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The dialog parts are lys-ui's: the card at the width asked, the
// title a heading a step over the text size and the text plain at it,
// a button in the variant asked and answering its click, the actions
// pushed right behind a spacer.
func TestDialogParts(t *testing.T) {
	font := treetest.Font(t)
	card := DialogCard("x-surface", 300)
	if !widget.HasClass(card, css.Card) || !widget.HasClass(card, "x-surface") || card.SizeRequest().W != 300 {
		t.Errorf("card classes %v width %d", card.Classes(), card.SizeRequest().W)
	}
	text := widget.Current().TextSize
	title := DialogTitle(font, "T", "x-title")
	if !widget.HasClass(title, css.Heading) || title.SizePx() != text*DialogTitleScale || !title.Wrap() {
		t.Errorf("title %vpx %v", title.SizePx(), title.Classes())
	}
	body := DialogText(font, "B", "x-body", css.DimLabel)
	if widget.HasClass(body, css.Heading) || !widget.HasClass(body, css.DimLabel) || body.SizePx() != text || !body.Wrap() {
		t.Errorf("text %vpx %v", body.SizePx(), body.Classes())
	}
	clicked := 0
	b := DialogButton(font, "OK", "x-ok", css.SuggestedAction, func() { clicked++ })
	if !widget.HasClass(b, css.SuggestedAction) || !widget.HasClass(b, "x-ok") || widget.HasClass(b, css.Outline) {
		t.Errorf("button classes %v", b.Classes())
	}
	b.OnClick()
	if clicked != 1 {
		t.Errorf("clicks %d, want 1", clicked)
	}
	row := DialogActions(b)
	kids := row.Children()
	if len(kids) != 2 || kids[1] != widget.Widget(b) {
		t.Fatalf("actions %v, want a spacer then the button", kids)
	}
	if _, ok := kids[0].(*widget.Spacer); !ok {
		t.Errorf("actions lead with %T, want the spacer", kids[0])
	}
}
