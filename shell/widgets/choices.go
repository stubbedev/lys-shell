package widgets

import (
	"strconv"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/styling"
)

// ChoiceRows builds a row per portal choice, a check button for a
// yes/no and a labelled dropdown otherwise, each starting at its
// choice's start value, and the function reading what the user picked.
// The rows carry class and their labels class+"-label".
func ChoiceRows(font render.Font, choices []portaldialogs.Choice, class string) ([]widget.Widget, func() []portaldialogs.Selection) {
	label := func(text string) *widget.Label {
		l := widget.NewLabel(font, TextPx(styling.Body, 1), text, 0)
		l.AddClass(class + "-label")
		return l
	}
	rows := make([]widget.Widget, 0, len(choices))
	reads := make([]func() portaldialogs.Selection, 0, len(choices))
	for _, c := range choices {
		if c.Boolean() {
			check := widget.NewCheckButton(c.Start() == "true")
			check.AddClass(class)
			l := label(c.Label)
			l.SetWrap(true)
			check.SetChild(l)
			rows = append(rows, check)
			reads = append(reads, func() portaldialogs.Selection {
				return portaldialogs.Selection{ID: c.ID, Value: strconv.FormatBool(check.Checked())}
			})
			continue
		}
		labels := make([]string, len(c.Options))
		start := 0
		for i, o := range c.Options {
			labels[i] = o.Label
			if o.ID == c.Start() {
				start = i
			}
		}
		drop := widget.NewDropdown(font, TextPx(styling.Body, 1), labels, start)
		drop.AddClass(class)
		row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
		row.Append(label(c.Label), true)
		row.Append(drop, false)
		rows = append(rows, row)
		reads = append(reads, func() portaldialogs.Selection {
			return portaldialogs.Selection{ID: c.ID, Value: c.Options[drop.Selected()].ID}
		})
	}
	return rows, func() []portaldialogs.Selection {
		out := make([]portaldialogs.Selection, 0, len(reads))
		for _, read := range reads {
			out = append(out, read())
		}
		return out
	}
}
