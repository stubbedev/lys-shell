package widgets

import (
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
)

// DialogTitleScale steps a dialog's title over lys-ui's text size.
const DialogTitleScale = 1.125

// DialogCard is a dialog's surface: lys-ui's card, spaced and padded by
// the theme, width px wide, carrying class.
func DialogCard(class string, width int) *widget.Box {
	th := widget.Current()
	box := widget.NewBox(widget.Column, 2*th.Spacing, 3*th.Padding)
	box.AddClass(css.Card, class)
	box.SetSizeRequest(width, 0)
	return box
}

// DialogTitle is a dialog's wrapping heading, a step over the text size.
func DialogTitle(font render.Font, text, class string) *widget.Label {
	l := widget.NewLabel(font, widget.Current().TextSize*DialogTitleScale, text, 0)
	l.AddClass(class, css.Heading)
	l.SetWrap(true)
	return l
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

// DialogActions is the right-aligned row of a dialog's buttons.
func DialogActions(buttons ...*widget.Button) *widget.Box {
	row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	row.Append(widget.NewSpacer(0, 0), true)
	for _, b := range buttons {
		row.Append(b, false)
	}
	return row
}
