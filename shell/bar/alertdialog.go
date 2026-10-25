package bar

import (
	"log"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
)

// alertSpec is the gtk::AlertDialog the dropdowns build for a
// destructive action: the message, its detail, and the buttons
// [cancel, accept] with cancel as both the default and the cancel
// button (cancel_button(0), default_button(0)).
type alertSpec struct {
	message, detail string
	cancel, accept  string
}

// Alert responses.
const (
	alertCancel = "cancel"
	alertAccept = "accept"
)

// alertDialog declares spec as lys-ui's modal alert (modal(true)):
// the message as the title over the muted, wrapped detail, cancel
// plain and accept destructive. Enter and Esc both answer cancel, so
// only a click on accept goes ahead. answer hears true for accept,
// false for anything else.
func alertDialog(ctx ModuleContext, spec alertSpec, answer func(accepted bool)) app.DialogConfig {
	px := 14.0
	if ctx.Style != nil && ctx.Style.labelPx > 0 {
		px = ctx.Style.labelPx
	}
	detail := widget.NewLabel(ctx.Font, px, spec.detail, 0)
	detail.SetWrap(true)
	detail.AddClass(css.DimLabel)
	return app.DialogConfig{
		Title:   spec.message,
		Width:   400,
		Content: detail,
		Buttons: []app.DialogButton{
			{Label: spec.cancel, Response: alertCancel, Role: app.ButtonRoleCancel, Appearance: app.AppearancePlain},
			{Label: spec.accept, Response: alertAccept, Appearance: app.AppearanceDestructive},
		},
		DefaultResponse: alertCancel,
		CancelResponse:  alertCancel,
		Modal:           true,
		OnResponse:      func(r string) { answer(r == alertAccept) },
	}
}

// showAlert asks spec in a modal dialog and answers once. The open
// dropdowns close first: the dialog is its own window, and a popover
// holding the keyboard would keep Enter and Esc from it (GTK's popover
// loses its grab to the dialog the same way). Without an application
// (headless) nothing can ask, so nothing is confirmed.
func (c ModuleContext) showAlert(spec alertSpec, answer func(accepted bool)) {
	if c.Alert != nil {
		c.Alert(spec, answer)
		return
	}
	if c.App == nil {
		answer(false)
		return
	}
	if c.Dropdowns != nil {
		c.Dropdowns.closeAll()
	}
	if _, err := c.App.NewDialog(nil, alertDialog(c, spec, answer)); err != nil {
		log.Printf("bar: alert: %v", err)
		answer(false)
	}
}
