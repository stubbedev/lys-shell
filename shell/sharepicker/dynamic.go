package sharepicker

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

func dynamicPageTitle() string { return i18n.T("share-picker-dynamic-page") }

func (p *Picker) dynamicPage() widget.Widget {
	card := p.card()
	card.RemoveClass("share-picker-card-loading")
	title := p.label(i18n.T("share-picker-dynamic-title"))
	title.AddClass("share-picker-image-label", css.Heading)
	title.SetAlignment(render.AlignCenter)
	body := p.label(i18n.T("share-picker-dynamic-body"))
	body.AddClass(css.Caption, css.DimLabel)
	body.SetAlignment(render.AlignCenter)
	body.SetWrap(true)
	card.Append(title, false)
	card.Append(body, false)
	button := widget.NewButton(card, 0, 0)
	button.AddClass(css.Flat, "share-picker-dynamic-button", choiceClass)
	button.SetCursorName("pointer")
	payload := Target{Kind: TargetDynamic}.Payload()
	button.OnClick = func() { p.selectPayload(payload) }
	box := widget.NewBox(widget.Column, 0, 0)
	box.AddClass("share-picker-page")
	box.Append(button, false)
	return page(widgets.Center(box))
}
