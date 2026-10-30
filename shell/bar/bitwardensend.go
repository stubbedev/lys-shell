package bar

import (
	"context"
	"time"

	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

const bwPageSends = "sends"

// bwSendLifetimes are the deletion delays a new Send offers, the
// longest lys-secrets accepts last.
var bwSendLifetimes = []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, vault.MaxSendLifetime}

// bwSendMaxAccess bounds the access limit a new Send can be given; 0 is
// none.
const bwSendMaxAccess = 1000

// bwSendLifetimeLabel names a deletion delay.
func bwSendLifetimeLabel(d time.Duration) string {
	if d < 24*time.Hour {
		return i18n.T("dropdown-bitwarden-send-hours", i18n.Int("hours", int64(d/time.Hour)))
	}
	return i18n.T("dropdown-bitwarden-send-days", i18n.Int("days", int64(d/(24*time.Hour))))
}

// bwSendCaption is a Send's second line: how often it was opened (of
// how often it may be), when it is deleted, and whether it is disabled
// or asks for a password.
func bwSendCaption(s vault.Send) string {
	accesses := i18n.T("dropdown-bitwarden-send-accesses", i18n.Int("count", int64(s.AccessCount)))
	if s.MaxAccessCount > 0 {
		accesses = i18n.T("dropdown-bitwarden-send-accesses-of", i18n.Int("count", int64(s.AccessCount)), i18n.Int("max", int64(s.MaxAccessCount)))
	}
	line := accesses
	if !s.Deletion.IsZero() {
		line += " · " + i18n.T("dropdown-bitwarden-send-deletes", i18n.Str("date", s.Deletion.Local().Format("2006-01-02 15:04")))
	}
	if s.HasPassword {
		line += " · " + i18n.T("dropdown-bitwarden-send-protected")
	}
	if s.Disabled {
		line += " · " + i18n.T("dropdown-bitwarden-send-disabled")
	}
	return line
}

// bwSendName is a Send's title, which lys-secrets may not have been
// able to decrypt.
func bwSendName(s vault.Send) string {
	if s.Undecryptable {
		return i18n.T("dropdown-bitwarden-undecryptable")
	}
	return s.Name
}

// bwSendIcon is a Send's glyph by what it carries.
func bwSendIcon(t vault.SendType) string {
	if t == vault.SendFile {
		return "ld-file-symbolic"
	}
	return "ld-file-text-symbolic"
}

func (v *bitwardenView) buildSends() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.sendForm = widget.NewForm()
	v.sendName = widget.NewEntry(v.font, v.px, 0)
	v.sendName.SetPlaceholder(i18n.T("dropdown-bitwarden-send-name-placeholder"))
	v.sendForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-send-name"), v.sendName), required(v.sendName))
	v.sendText = widget.NewTextArea(v.font, v.px, 0)
	v.sendText.SetWrap(true)
	v.sendText.SetPlaceholder(i18n.T("dropdown-bitwarden-send-text-placeholder"))
	v.sendForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-send-text"), v.sendText), func() string {
		if v.sendText.Text() == "" {
			return i18n.T("dropdown-bitwarden-required")
		}
		return ""
	})
	labels := make([]string, len(bwSendLifetimes))
	for i, d := range bwSendLifetimes {
		labels[i] = bwSendLifetimeLabel(d)
	}
	v.sendLife = widget.NewDropdown(v.font, v.px, labels, 2)
	v.sendForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-send-deletion"), v.sendLife), nil)
	v.sendMax = widget.NewSpinButton(v.font, v.px, 0, 0, bwSendMaxAccess, 1, 0)
	v.sendForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-send-max-access"), v.sendMax), nil)
	v.sendPw = newSecretEntry(v.font, v.px)
	v.sendPw.entry.SetPlaceholder(i18n.T("dropdown-bitwarden-send-password-placeholder"))
	v.sendForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-send-password"), v.sendPw.entry), nil)
	v.sendForm.OnSubmit = v.createSend
	page.Append(v.sendForm, false)
	page.Append(formActions(v.textButton(i18n.T("dropdown-bitwarden-back"), func() { v.setSending(false) }, css.Outline), v.textButton(i18n.T("dropdown-bitwarden-send-create"), v.sendForm.Submit, css.SuggestedAction)), false)
	v.sendStatus = v.caption(css.DimLabel)
	page.Append(v.sendStatus, false)
	card, list := itemCard()
	v.sendList = list
	v.sendSt = pageStack()
	v.sendSt.Add("sends", scrolledCard(card, ""))
	v.sendSt.Add("empty", widgets.EmptyState(v.font, v.px, "ld-send-symbolic", i18n.T("dropdown-bitwarden-send-none"), ""))
	page.Append(v.sendSt, true)
	return page
}

func (v *bitwardenView) toggleSends() { v.setSending(!v.sending) }

// setSending opens or closes the Sends page; opening reads the vault's
// Sends.
func (v *bitwardenView) setSending(on bool) {
	v.sending = on
	if on {
		v.generating, v.copying = false, nil
		v.repromptPw.reset()
		setCaption(v.sendStatus, "")
		v.loadSends()
	} else {
		v.clearSendForm()
	}
	v.apply()
}

// loadSends reads the Sends and lists them.
func (v *bitwardenView) loadSends() {
	var sends []vault.Send
	v.run(func(ctx context.Context) (err error) {
		sends, err = v.vault.Sends(ctx)
		return err
	}, func(err error) {
		if err == nil {
			v.listSends(sends)
		}
	})
}

// listSends shows sends, each with a delete button.
func (v *bitwardenView) listSends(sends []vault.Send) {
	v.sendList.Clear()
	for _, s := range sends {
		row := widget.NewItem(v.font, bwSendName(s), bwSendCaption(s))
		row.SetSmall(true)
		row.SetEllipsize(true)
		row.SetMedia(widget.NewThemeIcon(bwSendIcon(s.Type), int(v.px*1.2)), false)
		row.AddAction(v.iconButton("ld-trash-2-symbolic", i18n.T("dropdown-bitwarden-send-delete", i18n.Str("name", bwSendName(s))), func() { v.deleteSend(s.ID) }))
		v.sendList.Append(row)
	}
	if len(sends) == 0 {
		v.sendSt.Show("empty")
		return
	}
	v.sendSt.Show("sends")
}

// newTextSend is the form as a Send deleted after the chosen delay.
func (v *bitwardenView) newTextSend() vault.NewTextSend {
	life := bwSendLifetimes[min(max(v.sendLife.Selected(), 0), len(bwSendLifetimes)-1)]
	return vault.NewTextSend{
		Name: v.sendName.Text(), Text: v.sendText.Text(),
		Deletion:       v.now().Add(life),
		MaxAccessCount: uint32(v.sendMax.Value()),
		Password:       v.sendPw.entry.Text(),
	}
}

// createSend creates the form's Send, copies its link (which carries
// the Send's key) as a secret, and lists the Sends again.
func (v *bitwardenView) createSend() {
	s := v.newTextSend()
	var created vault.CreatedSend
	v.run(func(ctx context.Context) (err error) {
		created, err = v.vault.CreateTextSend(ctx, s)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		v.clearSendForm()
		ttl, err := v.host.copyText(created.URL, true, 0)
		reportCopy(v.sendStatus, i18n.T("dropdown-bitwarden-field-send-link"), ttl, err)
		v.loadSends()
	})
}

// deleteSend deletes Send id and lists the Sends again.
func (v *bitwardenView) deleteSend(id vault.SendID) {
	v.run(func(ctx context.Context) error { return v.vault.DeleteSend(ctx, id) }, func(err error) {
		if err == nil {
			setCaption(v.sendStatus, "")
			v.loadSends()
		}
	})
}

// clearSendForm empties the form, the text and the password above all.
func (v *bitwardenView) clearSendForm() {
	v.sendName.SetText("")
	v.sendText.SetText("")
	v.sendPw.reset()
	v.sendMax.SetValue(0)
	v.sendLife.SetSelected(2)
}
