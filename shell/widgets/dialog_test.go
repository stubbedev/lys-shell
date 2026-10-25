package widgets

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The dialog parts are lys-ui's: the card titled and pinned at the
// width asked, its body the rows given without the nil ones, the text
// plain at the text size, a button in the variant asked and answering
// its click.
func TestDialogParts(t *testing.T) {
	font := treetest.Font(t)
	card := DialogCard(font, "T", "x-surface", 300)
	if card.Title() != "T" || !widget.HasClass(card, "x-surface") || card.SizeRequest().W != 300 || card.MaximumSize().W != 300 {
		t.Errorf("card %q classes %v width %d..%d", card.Title(), card.Classes(), card.SizeRequest().W, card.MaximumSize().W)
	}
	a, b := widget.NewSpacer(1, 1), widget.NewSpacer(2, 2)
	if kids := DialogBody(a, nil, b).Children(); len(kids) != 2 || kids[0] != widget.Widget(a) || kids[1] != widget.Widget(b) {
		t.Errorf("body rows %v, want the two given without the nil", kids)
	}
	text := widget.Current().TextSize
	body := DialogText(font, "B", "x-body", css.DimLabel)
	if widget.HasClass(body, css.Heading) || !widget.HasClass(body, css.DimLabel) || body.SizePx() != text || !body.Wrap() {
		t.Errorf("text %vpx %v", body.SizePx(), body.Classes())
	}
	clicked := 0
	ok := DialogButton(font, "OK", "x-ok", css.SuggestedAction, func() { clicked++ })
	if !widget.HasClass(ok, css.SuggestedAction) || !widget.HasClass(ok, "x-ok") || widget.HasClass(ok, css.Outline) {
		t.Errorf("button classes %v", ok.Classes())
	}
	ok.OnClick()
	if clicked != 1 {
		t.Errorf("clicks %d, want 1", clicked)
	}
}
