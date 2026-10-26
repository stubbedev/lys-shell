package widgets

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// DialogCard is a dialog's surface: lys-ui's card under title, width
// px wide, carrying class.
func DialogCard(font render.Font, title, class string, width int) *widget.Card {
	c := widget.NewCard(font, title)
	c.AddClass(class)
	Pin(c, width, 0)
	return c
}

// DialogBody stacks a dialog's rows for its card's content, the
// theme's double spacing apart; nil rows are skipped.
func DialogBody(rows ...widget.Widget) *widget.Box {
	box := widget.NewBox(widget.Column, 2*widget.Current().Spacing, 0)
	for _, r := range rows {
		if r != nil {
			box.Append(r, false)
		}
	}
	return box
}

// DialogText is a wrapping line at lys-ui's text size.
func DialogText(font render.Font, text string, classes ...string) *widget.Label {
	l := widget.NewLabel(font, widget.Current().TextSize, text, 0)
	l.AddClass(classes...)
	l.SetWrap(true)
	return l
}

// DialogButton is a text button in lys-ui's variant (css.Outline,
// css.SuggestedAction), carrying class and its label class+"-label".
func DialogButton(font render.Font, text, class, variant string, onClick func()) *widget.Button {
	l := widget.NewLabel(font, widget.Current().TextSize, text, 0)
	l.AddClass(class + "-label")
	l.SetAlignment(render.AlignCenter)
	b := widget.NewButton(l, 0, 0)
	b.AddClass(class, variant)
	b.OnClick = onClick
	return b
}
