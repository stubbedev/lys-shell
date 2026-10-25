package settings

import (
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
)

// confirmDialogWidth is the confirm's width, lys-ui's MessageBox's.
const confirmDialogWidth = 440

// Responses of the reset-all confirm.
const (
	responseConfirm = "confirm"
	responseCancel  = "cancel"
)

// resetAllDialog is the reset-all question (ConfirmResetAll): lys-ui's
// alert dialog with the description under the title, cancel and the
// destructive confirm. It is modal, Esc cancels, Enter does nothing,
// and only the confirm response drops the overrides.
func resetAllDialog(k *kit) app.DialogConfig {
	t := i18n.Settings()
	body := k.label(t.Get("settings-reset-all-description"), css.DimLabel)
	body.SetWrap(true)
	return app.DialogConfig{
		Title:   t.Get("settings-reset-all-title"),
		Width:   confirmDialogWidth,
		Content: body,
		Buttons: []app.DialogButton{
			{Label: t.Get("settings-reset-all-cancel"), Response: responseCancel, Role: app.ButtonRoleCancel, Appearance: app.AppearancePlain},
			{Label: t.Get("settings-reset-all-confirm"), Response: responseConfirm, Appearance: app.AppearanceDestructive},
		},
		Modal:          true,
		CancelResponse: responseCancel,
		OnResponse: func(r string) {
			if r == responseConfirm {
				k.store.resetAll()
			}
		},
	}
}
