package bar

import (
	"context"
	"log"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/network"
)

// netSaveOffer is the offer to keep a sign-in the user typed for a
// connection in the vault's network folder, once that connection came
// up with it: the agent answers from there the next time.
type netSaveOffer struct {
	*formCard
	v       *networkView
	body    *widget.Label
	errText *widget.Label
	saveBtn *widget.Button
	showing network.SaveOffer
	saving  bool
}

func newNetSaveOffer(v *networkView) *netSaveOffer {
	f := &netSaveOffer{v: v}
	f.formCard = newFormCard(v.font, v.px, "ld-shield-check-symbolic", f.dismiss, "network-save-card")
	f.head.SetDescription(i18n.T("dropdown-network-save-title"))
	f.body = widget.NewLabel(v.font, v.px, "", 0)
	f.body.AddClass(css.DimLabel)
	f.body.SetWrap(true)
	f.body.SetMaxWidthChars(32)
	f.formCard.body.Append(f.body, false)
	f.errText = widget.NewLabel(v.font, v.px, "", 0)
	f.errText.AddClass(css.Error)
	f.errText.SetWrap(true)
	f.errText.SetMaxWidthChars(32)
	f.errText.SetVisible(false)
	f.formCard.body.Append(f.errText, false)
	f.saveBtn = v.primaryText(i18n.T("dropdown-network-save"), "network-save", f.save)
	f.formCard.body.Append(formActions(v.outlineText(i18n.T("dropdown-network-save-not-now"), "network-save-dismiss", f.dismiss), f.saveBtn), false)
	f.SetVisible(false)
	return f
}

// apply shows the agent's standing offer while the vault can take it.
func (f *netSaveOffer) apply(o network.SaveOffer, offered bool) {
	if !offered || f.v.saveLogin == nil {
		f.showing = network.SaveOffer{}
		f.SetVisible(false)
		return
	}
	if o.UUID != f.showing.UUID {
		f.errText.SetVisible(false)
	}
	f.showing = o
	f.head.SetTitle(o.Name)
	f.body.SetText(i18n.T("dropdown-network-save-body", i18n.Str("name", o.Name)))
	f.saveBtn.SetEnabled(!f.saving)
	f.SetVisible(true)
}

// save keeps the offer's sign-in in the vault off the loop; once kept
// the offer goes, a failure stays on the card.
func (f *netSaveOffer) save() {
	o, saveLogin := f.showing, f.v.saveLogin
	if f.saving || o.UUID == "" || saveLogin == nil {
		return
	}
	f.saving = true
	f.saveBtn.SetEnabled(false)
	go func() {
		ctx, cancel := context.WithTimeout(f.v.life, vaultCallTimeout)
		defer cancel()
		err := saveLogin(ctx, o.Name, o.Entry)
		f.v.ctx.Invoke(func() {
			f.saving = false
			f.saveBtn.SetEnabled(true)
			if err != nil {
				log.Printf("network: save %s to the vault: %v", o.Name, err)
				f.errText.SetText(bwErrorText(err))
				f.errText.SetVisible(true)
				return
			}
			f.dismiss()
		})
	}()
}

// dismiss takes the offer down for good.
func (f *netSaveOffer) dismiss() {
	if a := f.v.agent; a != nil && f.showing.UUID != "" {
		a.DismissSave(f.showing.UUID)
	}
	f.apply(network.SaveOffer{}, false)
}
