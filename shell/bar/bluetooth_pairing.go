package bar

import (
	"strconv"
	"strings"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/bluetooth"
)

// pairingVariant is PairingVariant: which prompt the card shows. The
// Rust Failed variant is never entered there and is not carried.
type pairingVariant uint8

const (
	variantNone pairingVariant = iota
	variantDisplayPin
	variantRequestPasskey
	variantDisplayPasskey
	variantRequestConfirmation
	variantRequestAuthorization
	variantRequestServiceAuthorization
	variantRequestPinCode
)

// pairingOutputKind is PairingCardOutput.
type pairingOutputKind uint8

const (
	outputCancelled pairingOutputKind = iota
	outputPasskeySubmitted
	outputPasskeyConfirmed
	outputPasskeyRejected
	outputAuthorizationAccepted
	outputAuthorizationRejected
	outputServiceAuthorizationAccepted
	outputServiceAuthorizationRejected
	outputLegacyPinSubmitted
)

// pairingOutput is one answer from the card.
type pairingOutput struct {
	kind    pairingOutputKind
	pin     string
	passkey uint32
}

// passkeyTotal is the passkey's digit count (passkey_total).
const passkeyTotal = 6

// btPairingCard is pairing_card: the prompt for the pending agent
// request, above the device lists.
type btPairingCard struct {
	root    *widget.Box
	variant pairingVariant
	// onOutput receives the user's answer; the dropdown clears the
	// card and forwards it to the service.
	onOutput func(pairingOutput)

	device *widget.Item
	icon   *widget.Icon

	// sections holds each variant-scoped widget with the variants it
	// shows under.
	sections []cardSection

	pinCode        []*widget.Label
	passkey        *widget.InputOTP
	progress       *widget.Label
	serviceName    *widget.Label
	legacyPinEntry *widget.Entry

	leftLabel, rightLabel *widget.Label
	right                 *widget.Button
}

type cardSection struct {
	w        widget.Widget
	variants []pairingVariant
}

// newBtPairingCard builds the card on lys-ui's parts: a card holding
// the device as an Item with the close action over a body the theme
// spaces and insets in line with the item, the passkey as an
// InputOTP, and the dim, title and monospace classes for the text
// (pairing_card/mod.rs:47-101).
func newBtPairingCard(ctx ModuleContext) *btPairingCard {
	font, px := dropdownFont(ctx)
	c := &btPairingCard{}
	th := widget.Current()
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass(css.Card, "bluetooth-pairing-card")
	col := widget.NewBox(widget.Column, th.Spacing, 0)
	col.SetPadding(render.Insets{Left: 2 * th.Padding, Right: 2 * th.Padding, Bottom: 2 * th.Padding})

	c.device = widget.NewItem(font, "", "")
	c.device.SetSmall(true)
	c.device.SetEllipsize(true)
	c.icon = widget.NewThemeIcon("ld-bluetooth-symbolic", itemIconPx)
	c.icon.AddClass("bluetooth-icon")
	c.device.SetMedia(c.icon, false)
	closeBtn := dropdownButton(widget.NewThemeIcon("ld-x-symbolic", int(px)), nil, css.Ghost, css.Icon, css.Small)
	closeBtn.AddClass("bluetooth-pairing-close")
	closeBtn.OnClick = func() { c.emit(pairingOutput{kind: outputCancelled}) }
	c.device.AddAction(closeBtn)
	card.Append(c.device, false)
	card.Append(col, false)

	message := func(key string) *widget.Label {
		l := widget.NewLabel(font, px, btText(key), 0)
		l.SetWrap(true)
		l.AddClass("bluetooth-pairing-message", css.DimLabel)
		return l
	}
	codeBlock := func(labelKey string) *widget.Box {
		box := widget.NewBox(widget.Column, th.Spacing/2, 0)
		box.AddClass("bluetooth-pin-display")
		if labelKey != "" {
			box.Append(message(labelKey), false)
		}
		code := widget.NewLabel(font, px*1.8, "", 0)
		code.AddClass("bluetooth-pin-code", css.Title1, css.Monospace)
		box.Append(code, false)
		c.pinCode = append(c.pinCode, code)
		return box
	}
	add := func(w widget.Widget, variants ...pairingVariant) {
		c.sections = append(c.sections, cardSection{w: w, variants: variants})
		col.Append(w, false)
	}

	displayPin := codeBlock("dropdown-bluetooth-pairing-enter-pin")
	add(displayPin, variantDisplayPin)
	add(message("dropdown-bluetooth-pairing-type-on-device"), variantDisplayPin)
	add(message("dropdown-bluetooth-pairing-allow-pairing"), variantRequestAuthorization)
	add(message("dropdown-bluetooth-pairing-enter-shown-pin"), variantRequestPasskey)

	// The passkey prompt: lys-ui's code input, six digit slots
	// (pairing_card/mod.rs:147-190).
	c.passkey = widget.NewInputOTP(font, widget.OTPDigits, passkeyTotal)
	c.passkey.SetHAlign(widget.AlignCenter)
	add(c.passkey, variantRequestPasskey)

	add(message("dropdown-bluetooth-pairing-confirm-code"), variantRequestConfirmation)
	confirmPin := codeBlock("")
	add(confirmPin, variantRequestConfirmation)

	displayPasskey := codeBlock("dropdown-bluetooth-pairing-enter-pin")
	add(displayPasskey, variantDisplayPasskey)
	c.progress = message("dropdown-bluetooth-pairing-entering")
	add(c.progress, variantDisplayPasskey)

	c.serviceName = widget.NewLabel(font, px, "", 0)
	c.serviceName.AddClass("bluetooth-service-name", css.Heading)
	add(c.serviceName, variantRequestServiceAuthorization)
	add(message("dropdown-bluetooth-pairing-service-allow"), variantRequestServiceAuthorization)

	add(message("dropdown-bluetooth-pairing-enter-legacy-pin"), variantRequestPinCode)
	c.legacyPinEntry = widget.NewEntry(font, px, 0)
	c.legacyPinEntry.AddClass("bluetooth-legacy-pin-input")
	c.legacyPinEntry.SetPlaceholder(btText("dropdown-bluetooth-pairing-pin-placeholder"))
	c.legacyPinEntry.OnChanged = limitEntry(c.legacyPinEntry, 16, func(rune) bool { return true })
	add(c.legacyPinEntry, variantRequestPinCode)
	hint := widget.NewLabel(font, px, btText("dropdown-bluetooth-pairing-common-pins"), 0)
	hint.SetWrap(true)
	hint.AddClass("bluetooth-pin-hint", css.Caption, css.DimLabel)
	add(hint, variantRequestPinCode)

	// The actions: reject/cancel on the left, the confirm on the right,
	// splitting the width.
	actions := widget.NewBox(widget.Row, th.Spacing, 0)
	actions.AddClass("bluetooth-pairing-actions")
	c.leftLabel = widget.NewLabel(font, px, "", 0)
	left := dropdownButton(c.leftLabel, nil, css.Outline)
	left.OnClick = func() { c.emit(c.rejectOutput()) }
	actions.Append(left, true)
	c.rightLabel = widget.NewLabel(font, px, "", 0)
	c.right = dropdownButton(c.rightLabel, nil, css.SuggestedAction)
	c.right.OnClick = func() {
		if out, ok := c.confirmOutput(); ok {
			c.emit(out)
		}
	}
	actions.Append(c.right, true)
	col.Append(actions, false)

	c.root = card
	c.apply()
	return c
}

// limitEntry keeps an entry to max runes that pass keep (set_max_length
// plus the Rust key filter).
func limitEntry(e *widget.Entry, maxRunes int, keep func(rune) bool) func(string) {
	return func(text string) {
		var b strings.Builder
		n := 0
		for _, r := range text {
			if n == maxRunes {
				break
			}
			if keep(r) {
				b.WriteRune(r)
				n++
			}
		}
		if filtered := b.String(); filtered != text {
			e.SetText(filtered)
		}
	}
}

func (c *btPairingCard) emit(out pairingOutput) {
	if c.onOutput != nil {
		c.onOutput(out)
	}
}

// set shows req for the device (SetRequest + apply_request).
func (c *btPairingCard) set(req bluetooth.PairingRequest, display deviceDisplay) {
	c.device.SetTitle(display.name)
	c.device.SetDescription(btText(display.typeKey))
	c.icon.SetThemeName(display.icon)
	c.passkey.SetCode("")
	c.legacyPinEntry.SetText("")
	code := ""
	switch r := req.(type) {
	case bluetooth.DisplayPinCode:
		c.variant = variantDisplayPin
		code = r.PinCode
	case bluetooth.RequestPasskey:
		c.variant = variantRequestPasskey
	case bluetooth.DisplayPasskey:
		c.variant = variantDisplayPasskey
		code = formatPasskey(r.Passkey)
		c.progress.SetText(btText("dropdown-bluetooth-pairing-entering", "entered", r.Entered, "total", passkeyTotal))
	case bluetooth.RequestConfirmation:
		c.variant = variantRequestConfirmation
		code = formatPasskey(r.Passkey)
	case bluetooth.RequestAuthorization:
		c.variant = variantRequestAuthorization
	case bluetooth.RequestServiceAuthorization:
		c.variant = variantRequestServiceAuthorization
		c.serviceName.SetText(btText(serviceNameKey(r.UUID)))
	case bluetooth.RequestPinCode:
		c.variant = variantRequestPinCode
	default:
		c.variant = variantNone
	}
	for _, l := range c.pinCode {
		l.SetText(code)
	}
	c.apply()
}

// clear hides the card (PairingCardMsg::Clear).
func (c *btPairingCard) clear() {
	c.variant = variantNone
	c.apply()
}

// apply syncs visibility and the action labels to the variant.
func (c *btPairingCard) apply() {
	c.root.SetVisible(c.variant != variantNone)
	for _, s := range c.sections {
		shown := false
		for _, v := range s.variants {
			if v == c.variant {
				shown = true
			}
		}
		if setter, ok := s.w.(interface{ SetVisible(bool) }); ok {
			setter.SetVisible(shown)
		}
	}
	c.leftLabel.SetText(btText(c.leftActionKey()))
	c.rightLabel.SetText(btText(c.rightActionKey()))
	c.right.SetVisible(c.hasConfirmAction())
}

// leftActionKey is left_action_label.
func (c *btPairingCard) leftActionKey() string {
	switch c.variant {
	case variantRequestConfirmation, variantRequestPasskey:
		return "dropdown-bluetooth-reject"
	case variantRequestAuthorization, variantRequestServiceAuthorization:
		return "dropdown-bluetooth-deny"
	}
	return "dropdown-bluetooth-cancel"
}

// rightActionKey is right_action_label.
func (c *btPairingCard) rightActionKey() string {
	switch c.variant {
	case variantRequestPasskey, variantRequestPinCode:
		return "dropdown-bluetooth-pair"
	case variantRequestAuthorization, variantRequestServiceAuthorization:
		return "dropdown-bluetooth-allow"
	}
	return "dropdown-bluetooth-confirm"
}

// hasConfirmAction is has_confirm_action: the display requests only
// have a cancel.
func (c *btPairingCard) hasConfirmAction() bool {
	switch c.variant {
	case variantDisplayPin, variantDisplayPasskey, variantNone:
		return false
	}
	return true
}

// confirmOutput is build_confirm_output. The code input answers
// RequestPasskey with the number it spells (the Rust sends it as a
// PIN, which its own provider refuses); an incomplete passkey has
// nothing to send.
func (c *btPairingCard) confirmOutput() (pairingOutput, bool) {
	switch c.variant {
	case variantRequestPasskey:
		passkey, err := strconv.ParseUint(c.passkey.Code(), 10, 32)
		if err != nil {
			return pairingOutput{}, false
		}
		return pairingOutput{kind: outputPasskeySubmitted, passkey: uint32(passkey)}, true
	case variantRequestConfirmation:
		return pairingOutput{kind: outputPasskeyConfirmed}, true
	case variantRequestAuthorization:
		return pairingOutput{kind: outputAuthorizationAccepted}, true
	case variantRequestServiceAuthorization:
		return pairingOutput{kind: outputServiceAuthorizationAccepted}, true
	case variantRequestPinCode:
		return pairingOutput{kind: outputLegacyPinSubmitted, pin: c.legacyPinEntry.Text()}, true
	}
	return pairingOutput{}, false
}

// rejectOutput is build_reject_output: the yes/no prompts reject, the
// rest cancel.
func (c *btPairingCard) rejectOutput() pairingOutput {
	switch c.variant {
	case variantRequestConfirmation:
		return pairingOutput{kind: outputPasskeyRejected}
	case variantRequestAuthorization:
		return pairingOutput{kind: outputAuthorizationRejected}
	case variantRequestServiceAuthorization:
		return pairingOutput{kind: outputServiceAuthorizationRejected}
	}
	return pairingOutput{kind: outputCancelled}
}
