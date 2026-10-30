package bar

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// bitwardenListMax caps the rows the vault page builds: a vault holds
// thousands of items, the search narrows them.
const bitwardenListMax = 50

// Vault page names.
const (
	bwPageLogin     = "login"
	bwPageChallenge = "challenge"
	bwPageUnlock    = "unlock"
	bwPageVault     = "vault"
	bwPageReprompt  = "reprompt"
	bwPageError     = "error"
)

// bwFieldIcon is a field's copy button glyph.
func bwFieldIcon(f vault.Field) string {
	switch f {
	case vault.FieldUsername:
		return "ld-user-symbolic"
	case vault.FieldTOTP:
		return "ld-clock-symbolic"
	case vault.FieldCardNumber:
		return "ld-credit-card-symbolic"
	case vault.FieldEmail:
		return "ld-mail-symbolic"
	case vault.FieldNotes:
		return "ld-copy-symbolic"
	}
	return "ld-key-round-symbolic"
}

// bwFieldName is a field as the copy messages say it.
func bwFieldName(f vault.Field) string {
	switch f {
	case vault.FieldUsername:
		return i18n.T("dropdown-bitwarden-field-username")
	case vault.FieldTOTP:
		return i18n.T("dropdown-bitwarden-field-totp")
	case vault.FieldCardNumber:
		return i18n.T("dropdown-bitwarden-field-card-number")
	case vault.FieldCardCode:
		return i18n.T("dropdown-bitwarden-field-card-code")
	case vault.FieldEmail:
		return i18n.T("dropdown-bitwarden-field-email")
	case vault.FieldNotes:
		return i18n.T("dropdown-bitwarden-field-notes")
	}
	return i18n.T("dropdown-bitwarden-field-password")
}

// bwTOTPLine is the live code row's caption: the code in two halves and
// the seconds it has left at now.
func bwTOTPLine(code vault.Value, now time.Time) string {
	text := code.Text
	if half := len(text) / 2; len(text) == 6 || len(text) == 8 {
		text = text[:half] + " " + text[half:]
	}
	return i18n.T("dropdown-bitwarden-totp-live", i18n.Str("code", text), i18n.Int("seconds", int64(code.Expires.Sub(now)/time.Second)))
}

// bwItemIcon is an item's row glyph by its type.
func bwItemIcon(t vault.ItemType) string {
	switch t {
	case vault.ItemCard:
		return "ld-credit-card-symbolic"
	case vault.ItemIdentity:
		return "ld-id-card-symbolic"
	case vault.ItemSecureNote:
		return "ld-file-text-symbolic"
	case vault.ItemSSHKey:
		return "ld-terminal-symbolic"
	}
	return "ld-globe-symbolic"
}

// bwItemCaption is the row's second line: the login's username, the
// card's brand, the item's folder.
func bwItemCaption(it vault.Item) string {
	switch {
	case it.Undecryptable:
		return i18n.T("dropdown-bitwarden-undecryptable")
	case it.Type == vault.ItemLogin && it.Username != "":
		return it.Username
	case it.Type == vault.ItemCard && it.CardBrand != "":
		return it.CardBrand
	}
	return it.Folder
}

// bwMatches is the items matching query in vault order, at most max,
// and how many matched in all.
func bwMatches(items []vault.Item, query string, max int) ([]vault.Item, int) {
	var shown []vault.Item
	total := 0
	for _, it := range items {
		if !it.Match(query) {
			continue
		}
		total++
		if len(shown) < max {
			shown = append(shown, it)
		}
	}
	return shown, total
}

// bwErrorText is an operation's failure as the dropdown says it.
func bwErrorText(err error) string {
	switch {
	case errors.Is(err, vault.ErrWrongPassword):
		return i18n.T("dropdown-bitwarden-error-wrong-password")
	case errors.Is(err, vault.ErrInvalidCredentials):
		return i18n.T("dropdown-bitwarden-error-credentials")
	case errors.Is(err, vault.ErrLoginRequired):
		return i18n.T("dropdown-bitwarden-error-login-required")
	case errors.Is(err, vault.ErrUnreachable):
		return i18n.T("dropdown-bitwarden-error-network")
	}
	if se, ok := errors.AsType[*vault.ServerError](err); ok {
		if se.Retryable() {
			return i18n.T("dropdown-bitwarden-error-unavailable")
		}
		return i18n.T("dropdown-bitwarden-error-server", i18n.Str("message", se.Message))
	}
	return err.Error()
}

// bwChallenge is a login waiting on a code: the attempt to repeat and
// what the server asked for.
type bwChallenge struct {
	req       vault.LoginRequest
	newDevice bool
	provider  vault.TwoFactorProvider
}

// bwCopyRequest is a copy waiting on the master password reprompt.
type bwCopyRequest struct {
	item  vault.Item
	field vault.Field
}

// bitwardenView is the vault dropdown: the login (and its code step),
// the unlock, the vault search, the master password reprompt. Network
// operations run off the loop one at a time; secrets typed into it are
// cleared when the popover hides.
type bitwardenView struct {
	popoverHook
	*widget.Box

	ctx    ModuleContext
	host   *bitwardenHost
	vault  bwVault
	font   render.Font
	px     float64
	now    func() time.Time
	random io.Reader

	life   context.Context
	cancel context.CancelFunc
	once   sync.Once

	busy bool
	// state is the vault's state at the last apply; started says there
	// was one.
	state      vault.State
	started    bool
	pending    *bwChallenge
	copying    *bwCopyRequest
	generating bool

	spinner   *widget.Spinner
	genBtn    *widget.Button
	syncBtn   *widget.Button
	lockBtn   *widget.Button
	logoutBtn *widget.Button
	pages     *widget.Stack
	errLabel  *widget.Label

	loginForm  *widget.Form
	server     *widget.Label
	email      *widget.Entry
	password   *secretEntry
	apiKey     bool
	apiLabel   *widget.Label
	clientID   *widget.Field
	clientIDIn *widget.Entry
	secret     *widget.Field
	secretIn   *secretEntry

	codeTitle *widget.Label
	codeDesc  *widget.Label
	code      *widget.Entry
	sendEmail *widget.Button

	account  *widget.Label
	unlockPw *secretEntry

	search *widget.SearchEntry
	// totpRow is the live verification code of the item whose fields
	// were copied last, totpID that item and totpCode its code as last
	// read; hidden for none, and never shown for an item behind the
	// master password reprompt. totpReading holds while a code is read.
	totpRow     *widget.Item
	totpSt      *widget.Stack
	totpID      string
	totpCode    vault.Value
	totpReading bool
	list        *widget.ItemGroup
	listSt      *widget.Stack
	more        *widget.Label
	status      *widget.Label

	repromptDesc *widget.Label
	repromptPw   *secretEntry

	genList   *widget.ItemGroup
	genSt     *widget.Stack
	genRows   []*widget.Item
	genStatus *widget.Label

	// sending is whether the Sends page is open.
	sending    bool
	sendBtn    *widget.Button
	sendForm   *widget.Form
	sendName   *widget.Entry
	sendText   *widget.TextArea
	sendLife   *widget.Dropdown
	sendMax    *widget.SpinButton
	sendPw     *secretEntry
	sendStatus *widget.Label
	sendList   *widget.ItemGroup
	sendSt     *widget.Stack
}

func bitwardenDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &bitwardenView{ctx: ctx, host: ctx.Bitwarden, font: font, px: px, now: time.Now, random: rand.Reader}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "bitwarden-dropdown")
	v.spinner = widget.NewSpinner(int(px))
	v.spinner.SetVisible(false)
	v.genBtn = v.iconButton("ld-shuffle-symbolic", i18n.T("dropdown-bitwarden-generator"), v.toggleGenerator)
	v.sendBtn = v.iconButton("ld-send-symbolic", i18n.T("dropdown-bitwarden-sends"), v.toggleSends)
	v.syncBtn = v.iconButton("ld-refresh-cw-symbolic", i18n.T("dropdown-bitwarden-sync"), v.sync)
	v.lockBtn = v.iconButton("ld-lock-symbolic", i18n.T("dropdown-bitwarden-lock"), v.lock)
	v.logoutBtn = v.iconButton("ld-log-out-symbolic", i18n.T("dropdown-bitwarden-logout"), v.logout)
	v.Append(dropdownHeader(font, "ld-shield-check-symbolic", i18n.T("dropdown-bitwarden-title"), v.spinner, v.genBtn, v.sendBtn, v.syncBtn, v.lockBtn, v.logoutBtn), false)
	content := dropdownContent()
	v.errLabel = v.caption(css.Error)
	v.pages = pageStack()
	v.pages.Add(bwPageLogin, v.buildLogin())
	v.pages.Add(bwPageChallenge, v.buildChallenge())
	v.pages.Add(bwPageUnlock, v.buildUnlock())
	v.pages.Add(bwPageVault, v.buildVault())
	v.pages.Add(bwPageReprompt, v.buildReprompt())
	v.pages.Add(bwPageGenerator, v.buildGenerator())
	v.pages.Add(bwPageSends, v.buildSends())
	content.Append(v.errLabel, false)
	content.Append(v.pages, true)
	v.Append(content, true)
	if v.host == nil {
		v.showError(errBitwardenNoHost)
		return v
	}
	vt, err := v.host.vault()
	if err != nil {
		v.showError(err)
		return v
	}
	v.vault = vt
	v.apply()
	v.follow()
	return v
}

// follow re-applies on every change of the vault until the instance is
// released; the life also bounds the view's operations.
func (v *bitwardenView) follow() {
	life, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.life = life
	ticks, stop := v.vault.Subscribe()
	second := time.NewTicker(time.Second)
	go func() {
		defer stop()
		defer second.Stop()
		for {
			select {
			case <-life.Done():
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				v.ctx.Invoke(v.apply)
			case <-second.C:
				v.ctx.Invoke(v.refreshTOTP)
			}
		}
	}()
}

// liveTOTPItem is the item the live code row follows, false when there
// is none to show.
func (v *bitwardenView) liveTOTPItem() (vault.Item, bool) {
	if v.totpID == "" || v.vault == nil || v.vault.Status().State != vault.StateUnlocked {
		return vault.Item{}, false
	}
	for _, it := range v.vault.Items() {
		if it.ID == v.totpID && slices.Contains(it.Fields, vault.FieldTOTP) && !it.Reprompt {
			return it, true
		}
	}
	return vault.Item{}, false
}

// refreshTOTP shows the followed item's current code and its seconds
// left, reading the next code once the shown one expired.
func (v *bitwardenView) refreshTOTP() {
	it, ok := v.liveTOTPItem()
	if !ok {
		v.totpSt.Show("none")
		return
	}
	now := v.now()
	if !now.Before(v.totpCode.Expires) {
		v.totpSt.Show("none")
		v.readTOTP(it.ID)
		return
	}
	v.totpRow.SetTitle(it.Name)
	v.totpRow.SetDescription(bwTOTPLine(v.totpCode, now))
	v.totpSt.Show("code")
}

// readTOTP reads item id's current code off the loop; a code that does
// not come stops the row following the item.
func (v *bitwardenView) readTOTP(id string) {
	if v.totpReading || v.life == nil {
		return
	}
	v.totpReading = true
	go func() {
		code, err := v.vault.Field(v.life, id, vault.FieldTOTP, "")
		v.ctx.Invoke(func() {
			v.totpReading = false
			if id != v.totpID {
				return
			}
			if err != nil {
				log.Printf("bitwarden: the live code: %v", err)
				v.totpID = ""
			}
			v.totpCode = code
			v.refreshTOTP()
		})
	}()
}

// copyLiveTOTP copies the live row's code.
func (v *bitwardenView) copyLiveTOTP() {
	if _, ok := v.liveTOTPItem(); !ok || !v.now().Before(v.totpCode.Expires) {
		return
	}
	ttl, err := v.host.copyValue(vault.FieldTOTP, v.totpCode, v.now())
	reportCopy(v.status, bwFieldName(vault.FieldTOTP), ttl, err)
}

// showError replaces the pages with why there is no vault.
func (v *bitwardenView) showError(err error) {
	log.Printf("bitwarden: %v", err)
	v.pages.Add(bwPageError, widgets.EmptyState(v.font, v.px, "ld-shield-check-symbolic", i18n.T("dropdown-bitwarden-unavailable"), err.Error()))
	v.pages.Show(bwPageError)
	v.genBtn.SetVisible(false)
	v.sendBtn.SetVisible(false)
	v.syncBtn.SetVisible(false)
	v.lockBtn.SetVisible(false)
	v.logoutBtn.SetVisible(false)
}

func (v *bitwardenView) iconButton(icon, tooltip string, onClick func()) *widget.Button {
	b := dropdownButton(widget.NewThemeIcon(icon, int(v.px)), onClick, css.Ghost, css.Icon, css.Small)
	b.SetTooltip(tooltip)
	return b
}

func (v *bitwardenView) textButton(text string, onClick func(), classes ...string) *widget.Button {
	return dropdownButton(widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), text, 0), onClick, classes...)
}

// caption is a wrapped secondary line, hidden while empty.
func (v *bitwardenView) caption(classes ...string) *widget.Label {
	l := widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	l.AddClass(classes...)
	l.SetWrap(true)
	l.SetVisible(false)
	return l
}

func setCaption(l *widget.Label, text string) {
	l.SetText(text)
	l.SetVisible(text != "")
}

func (v *bitwardenView) buildLogin() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.server = widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	v.server.AddClass(css.DimLabel)
	v.server.SetEllipsize(widget.EllipsizeEnd)
	page.Append(v.server, false)
	v.loginForm = widget.NewForm()
	v.email = widget.NewEntry(v.font, v.px, 0)
	v.email.SetPlaceholder(i18n.T("dropdown-bitwarden-email-placeholder"))
	v.loginForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-email"), v.email), required(v.email))
	v.password = newSecretEntry(v.font, v.px)
	v.loginForm.Add(widget.NewField(v.font, i18n.T("dropdown-bitwarden-master-password"), v.password.entry), required(v.password.entry))
	v.clientIDIn = widget.NewEntry(v.font, v.px, 0)
	v.clientID = widget.NewField(v.font, i18n.T("dropdown-bitwarden-client-id"), v.clientIDIn)
	v.loginForm.Add(v.clientID, func() string {
		if !v.apiKey {
			return ""
		}
		return required(v.clientIDIn)()
	})
	v.secretIn = newSecretEntry(v.font, v.px)
	v.secret = widget.NewField(v.font, i18n.T("dropdown-bitwarden-client-secret"), v.secretIn.entry)
	v.loginForm.Add(v.secret, func() string {
		if !v.apiKey {
			return ""
		}
		return required(v.secretIn.entry)()
	})
	v.loginForm.OnSubmit = v.login
	page.Append(v.loginForm, false)
	v.apiLabel = widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	page.Append(formActions(dropdownButton(v.apiLabel, v.toggleAPIKey, css.Link), v.textButton(i18n.T("dropdown-bitwarden-login"), v.loginForm.Submit, css.SuggestedAction)), false)
	v.setAPIKey(false)
	return page
}

// required is a form validator refusing an empty entry.
func required(e *widget.Entry) func() string {
	return func() string {
		if e.Text() == "" {
			return i18n.T("dropdown-bitwarden-required")
		}
		return ""
	}
}

func (v *bitwardenView) toggleAPIKey() { v.setAPIKey(!v.apiKey) }

// setAPIKey switches the login between the master password alone and
// the personal API key (which gets around a second factor this client
// cannot complete) plus the master password.
func (v *bitwardenView) setAPIKey(on bool) {
	v.apiKey = on
	v.clientID.SetVisible(on)
	v.secret.SetVisible(on)
	label := i18n.T("dropdown-bitwarden-use-api-key")
	if on {
		label = i18n.T("dropdown-bitwarden-use-password")
	}
	v.apiLabel.SetText(label)
}

func (v *bitwardenView) buildChallenge() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.codeTitle = widget.NewLabel(v.font, v.px, "", 0)
	v.codeTitle.AddClass(css.Heading)
	page.Append(v.codeTitle, false)
	v.codeDesc = widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	v.codeDesc.AddClass(css.DimLabel)
	v.codeDesc.SetWrap(true)
	page.Append(v.codeDesc, false)
	v.code = widget.NewEntry(v.font, v.px, 0)
	v.code.SetPlaceholder(i18n.T("dropdown-bitwarden-code-placeholder"))
	v.code.OnActivate = func(string) { v.submitCode() }
	page.Append(v.code, false)
	v.sendEmail = v.textButton(i18n.T("dropdown-bitwarden-send-code"), v.sendCode, css.Link)
	page.Append(formActions(v.sendEmail, v.textButton(i18n.T("dropdown-bitwarden-back"), v.back, css.Outline), v.textButton(i18n.T("dropdown-bitwarden-verify"), v.submitCode, css.SuggestedAction)), false)
	return page
}

func (v *bitwardenView) buildUnlock() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.account = widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	v.account.AddClass(css.DimLabel)
	v.account.SetEllipsize(widget.EllipsizeEnd)
	page.Append(v.account, false)
	v.unlockPw = newSecretEntry(v.font, v.px)
	v.unlockPw.entry.SetPlaceholder(i18n.T("dropdown-bitwarden-master-password"))
	v.unlockPw.entry.OnActivate = func(string) { v.unlock() }
	page.Append(v.unlockPw.entry, false)
	page.Append(formActions(v.textButton(i18n.T("dropdown-bitwarden-unlock"), v.unlock, css.SuggestedAction)), false)
	return page
}

func (v *bitwardenView) buildVault() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.search = widget.NewSearchEntry(v.font, v.px, i18n.T("dropdown-bitwarden-search"))
	v.search.OnSearchChanged = func(string) { v.rebuildList() }
	page.Append(v.search, false)
	totpCard, totpGroup := itemCard()
	v.totpRow = widget.NewItem(v.font, "", "")
	v.totpRow.SetSmall(true)
	v.totpRow.SetEllipsize(true)
	v.totpRow.SetMedia(widget.NewThemeIcon(bwFieldIcon(vault.FieldTOTP), int(v.px*1.2)), false)
	v.totpRow.AddClass("bitwarden-totp-live")
	v.totpRow.SetOnActivate(v.copyLiveTOTP)
	totpGroup.Append(v.totpRow)
	v.totpSt = pageStack()
	v.totpSt.Add("none", widget.NewBox(widget.Column, 0, 0))
	v.totpSt.Add("code", totpCard)
	page.Append(v.totpSt, false)
	card, list := itemCard()
	v.list = list
	v.listSt = pageStack()
	v.listSt.Add("items", scrolledCard(card, ""))
	v.listSt.Add("empty", widgets.EmptyState(v.font, v.px, "ld-shield-check-symbolic", i18n.T("dropdown-bitwarden-no-items"), ""))
	page.Append(v.listSt, true)
	v.more = v.caption(css.DimLabel)
	page.Append(v.more, false)
	v.status = v.caption(css.DimLabel)
	page.Append(v.status, false)
	return page
}

func (v *bitwardenView) buildReprompt() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	title := widget.NewLabel(v.font, v.px, i18n.T("dropdown-bitwarden-reprompt-title"), 0)
	title.AddClass(css.Heading)
	page.Append(title, false)
	v.repromptDesc = widget.NewLabel(v.font, v.px*styling.Caption.Ratio(), "", 0)
	v.repromptDesc.AddClass(css.DimLabel)
	v.repromptDesc.SetWrap(true)
	page.Append(v.repromptDesc, false)
	v.repromptPw = newSecretEntry(v.font, v.px)
	v.repromptPw.entry.SetPlaceholder(i18n.T("dropdown-bitwarden-master-password"))
	v.repromptPw.entry.OnActivate = func(string) { v.confirmReprompt() }
	page.Append(v.repromptPw.entry, false)
	page.Append(formActions(v.textButton(i18n.T("dropdown-bitwarden-cancel"), v.cancelReprompt, css.Outline), v.textButton(i18n.T("dropdown-bitwarden-copy"), v.confirmReprompt, css.SuggestedAction)), false)
	return page
}

// apply follows the service: the page for its state, the header's
// actions, its last error, and the list while open.
func (v *bitwardenView) apply() {
	shown := v.pages.Visible()
	defer func() {
		if w := v.dropdownFocus(); w != nil && v.pages.Visible() != shown {
			v.focus(w)
		}
	}()
	st := v.vault.Status()
	changed := !v.started || st.State != v.state
	v.state, v.started = st.State, true
	v.spinner.SetVisible(v.busy || st.Syncing)
	v.spinner.SetSpinning(v.busy || st.Syncing)
	v.syncBtn.SetVisible(st.State == vault.StateUnlocked)
	v.sendBtn.SetVisible(st.State == vault.StateUnlocked)
	v.lockBtn.SetVisible(st.State == vault.StateUnlocked)
	v.logoutBtn.SetVisible(st.State != vault.StateLoggedOut)
	v.syncBtn.SetEnabled(!v.busy && !st.Syncing)
	v.pages.SetEnabled(!v.busy)
	if !v.busy {
		v.setError(st.Err)
	}
	cfg := v.host.config()
	v.server.SetText(i18n.T("dropdown-bitwarden-server", i18n.Str("server", cfg.Server.String())))
	v.account.SetText(i18n.T("dropdown-bitwarden-account", i18n.Str("email", st.Email)))
	if v.generating {
		v.pages.Show(bwPageGenerator)
		return
	}
	if v.sending && st.State != vault.StateUnlocked {
		v.sending = false
		v.clearSendForm()
	}
	if v.sending {
		v.pages.Show(bwPageSends)
		return
	}
	switch st.State {
	case vault.StateLoggedOut:
		if v.pending != nil {
			v.pages.Show(bwPageChallenge)
			return
		}
		if changed || v.pages.Visible() != bwPageLogin {
			v.clearSecrets()
			if v.email.Text() == "" {
				v.email.SetText(cfg.Email)
			}
			v.pages.Show(bwPageLogin)
		}
	case vault.StateLocked:
		if changed || v.pages.Visible() != bwPageUnlock {
			v.clearSecrets()
			setCaption(v.status, "")
			v.pages.Show(bwPageUnlock)
		}
	case vault.StateUnlocked:
		if v.copying == nil {
			v.pages.Show(bwPageVault)
		}
		v.rebuildList()
	}
	v.refreshTOTP()
}

// setError shows err under the page; the server asking for a code is
// the code step, not an error.
func (v *bitwardenView) setError(err error) {
	_, challenged := errors.AsType[*vault.TwoFactorRequiredError](err)
	text := ""
	if err != nil && !challenged && !errors.Is(err, vault.ErrNewDeviceVerification) {
		text = bwErrorText(err)
	}
	setCaption(v.errLabel, text)
}

// clearSecrets empties every secret box and drops a pending code step
// or reprompt.
func (v *bitwardenView) clearSecrets() {
	v.password.reset()
	v.secretIn.reset()
	v.unlockPw.reset()
	v.repromptPw.reset()
	v.code.SetText("")
	v.pending, v.copying = nil, nil
}

// dropdownHidden implements dropdownHider: nothing typed into a secret
// box outlives the popover.
func (v *bitwardenView) dropdownHidden() {
	v.clearSecrets()
	v.generating = false
	v.sending = false
	v.clearSendForm()
	setCaption(v.genStatus, "")
	if v.vault != nil {
		v.apply()
	}
}

// dropdownFocus implements dropdownFocuser: the visible page's first
// input takes the keyboard as the popover opens: the email, or the
// password once the email is filled; the code; the master password;
// the search.
func (v *bitwardenView) dropdownFocus() widget.Widget {
	switch v.pages.Visible() {
	case bwPageLogin:
		if v.email.Text() == "" {
			return v.email
		}
		return v.password.entry
	case bwPageChallenge:
		return v.code
	case bwPageUnlock:
		return v.unlockPw.entry
	case bwPageVault:
		return v.search
	case bwPageReprompt:
		return v.repromptPw.entry
	case bwPageGenerator:
		if len(v.genRows) > 0 {
			return v.genRows[0]
		}
	case bwPageSends:
		return v.sendName
	}
	return nil
}

// dropdownClosed implements dropdownCloser.
func (v *bitwardenView) dropdownClosed() {
	if v.cancel != nil {
		v.once.Do(func() { v.cancel() })
	}
}

// run runs op off the loop with the form disabled, then shows its
// error (or what then) on the loop.
func (v *bitwardenView) run(op func(context.Context) error, then func(error)) {
	if v.busy {
		return
	}
	v.busy = true
	v.setError(nil)
	v.apply()
	go func() {
		err := op(v.life)
		v.ctx.Invoke(func() {
			v.busy = false
			if then != nil {
				then(err)
			}
			v.apply()
			if err != nil {
				v.setError(err)
			}
		})
	}()
}

func (v *bitwardenView) login() {
	server := v.host.config().Server.Server()
	email, password := v.email.Text(), v.password.entry.Text()
	if v.apiKey {
		req := vault.APIKeyRequest{Server: server, Email: email, ClientID: v.clientIDIn.Text(), Secret: v.secretIn.entry.Text(), Password: password}
		v.run(func(ctx context.Context) error { return v.vault.LoginAPIKey(ctx, req) }, nil)
		return
	}
	v.attempt(vault.LoginRequest{Server: server, Email: email, Password: password})
}

// attempt logs in, turning the server's ask for a code into the code
// step.
func (v *bitwardenView) attempt(req vault.LoginRequest) {
	v.run(func(ctx context.Context) error { return v.vault.Login(ctx, req) }, func(err error) {
		tf, challenged := errors.AsType[*vault.TwoFactorRequiredError](err)
		switch {
		case challenged:
			v.challenge(&bwChallenge{req: req, provider: bwProvider(tf.Providers)})
		case errors.Is(err, vault.ErrNewDeviceVerification):
			v.challenge(&bwChallenge{req: req, newDevice: true})
		case err == nil:
			v.pending = nil
		}
	})
}

// bwProvider picks the second factor to ask for: the authenticator app
// when the account has one, else the emailed code.
func bwProvider(ps []vault.TwoFactorProvider) vault.TwoFactorProvider {
	for _, p := range ps {
		if p == vault.TwoFactorAuthenticator {
			return p
		}
	}
	return vault.TwoFactorEmail
}

// challenge shows the code step for c.
func (v *bitwardenView) challenge(c *bwChallenge) {
	v.pending = c
	v.code.SetText("")
	switch {
	case c.newDevice:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-new-device-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-new-device-desc", i18n.Str("email", c.req.Email)))
	case c.provider == vault.TwoFactorEmail:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-two-factor-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-two-factor-email", i18n.Str("email", c.req.Email)))
	default:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-two-factor-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-two-factor-app"))
	}
	v.sendEmail.SetVisible(!c.newDevice && c.provider == vault.TwoFactorEmail)
	v.pages.Show(bwPageChallenge)
	v.focus(v.code)
}

func (v *bitwardenView) sendCode() {
	if v.pending == nil {
		return
	}
	req := v.pending.req
	v.run(func(ctx context.Context) error { return v.vault.SendLoginEmail(ctx, req) }, nil)
}

func (v *bitwardenView) submitCode() {
	c := v.pending
	if c == nil || v.code.Text() == "" {
		return
	}
	req := c.req
	if c.newDevice {
		req.NewDeviceOTP = v.code.Text()
	} else {
		req.TwoFactor = &vault.TwoFactor{Provider: c.provider, Code: v.code.Text()}
	}
	v.attempt(req)
}

func (v *bitwardenView) back() {
	v.pending = nil
	v.code.SetText("")
	v.setError(nil)
	v.pages.Show(bwPageLogin)
}

func (v *bitwardenView) unlock() {
	password := v.unlockPw.entry.Text()
	if password == "" {
		return
	}
	v.unlockPw.reset()
	v.run(func(ctx context.Context) error { return v.vault.Unlock(ctx, password) }, nil)
}

func (v *bitwardenView) sync() {
	v.run(func(ctx context.Context) error { return v.vault.Sync(ctx) }, nil)
}

func (v *bitwardenView) lock() {
	v.run(func(ctx context.Context) error { return v.vault.Lock(ctx) }, nil)
}

func (v *bitwardenView) logout() {
	v.run(func(ctx context.Context) error { return v.vault.Logout(ctx) }, nil)
}

// rebuildList shows the items matching the search.
func (v *bitwardenView) rebuildList() {
	if v.vault == nil {
		return
	}
	shown, total := bwMatches(v.vault.Items(), v.search.Text(), bitwardenListMax)
	v.list.Clear()
	for _, it := range shown {
		v.list.Append(v.row(it))
	}
	if total == 0 {
		v.listSt.Show("empty")
	} else {
		v.listSt.Show("items")
	}
	more := ""
	if total > len(shown) {
		more = i18n.T("dropdown-bitwarden-more", i18n.Int("shown", int64(len(shown))), i18n.Int("total", int64(total)))
	}
	setCaption(v.more, more)
}

// row is one item: its type's glyph, name and caption, a copy button
// per field; a click copies the primary field.
func (v *bitwardenView) row(it vault.Item) widget.Widget {
	row := widget.NewItem(v.font, it.Name, bwItemCaption(it))
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.SetMedia(widget.NewThemeIcon(bwItemIcon(it.Type), int(v.px*1.2)), false)
	if len(it.Fields) > 0 {
		row.SetOnActivate(func() { v.copyField(it, it.Fields[0]) })
	}
	for _, f := range it.Fields {
		b := v.iconButton(bwFieldIcon(f), i18n.T("dropdown-bitwarden-copy-field", i18n.Str("field", bwFieldName(f))), func() { v.copyField(it, f) })
		row.AddAction(b)
	}
	return row
}

// copyField copies one field, asking for the master password first when
// the item is protected by the reprompt and the field is a secret.
func (v *bitwardenView) copyField(it vault.Item, f vault.Field) {
	if it.Reprompt && f.Secret() {
		v.copying = &bwCopyRequest{item: it, field: f}
		v.repromptDesc.SetText(i18n.T("dropdown-bitwarden-reprompt-desc", i18n.Str("field", bwFieldName(f)), i18n.Str("item", it.Name)))
		v.repromptPw.reset()
		v.pages.Show(bwPageReprompt)
		v.focus(v.repromptPw.entry)
		return
	}
	v.copyNow(it, f, "")
}

// copyNow reads the field from the vault, with the master password for
// a protected item, then puts it on the clipboard and says so. A copy
// from an item with a verification code makes the live row follow it.
func (v *bitwardenView) copyNow(it vault.Item, f vault.Field, password string) {
	var value vault.Value
	v.run(func(ctx context.Context) (err error) {
		value, err = v.vault.Field(ctx, it.ID, f, password)
		return err
	}, func(err error) {
		if err != nil {
			if v.copying != nil {
				v.focus(v.repromptPw.entry)
			}
			return
		}
		v.copying = nil
		ttl, err := v.host.copyValue(f, value, v.now())
		reportCopy(v.status, bwFieldName(f), ttl, err)
		if err == nil && slices.Contains(it.Fields, vault.FieldTOTP) && !it.Reprompt {
			v.totpID, v.totpCode = it.ID, vault.Value{}
			if f == vault.FieldTOTP {
				v.totpCode = value
			}
		}
	})
}

func reportCopy(l *widget.Label, field string, ttl time.Duration, err error) {
	if err != nil {
		log.Printf("bitwarden: copy %s: %v", field, err)
		l.AddClass(css.Error)
		setCaption(l, bwErrorText(err))
		return
	}
	msg := i18n.T("dropdown-bitwarden-copied", i18n.Str("field", field))
	if ttl > 0 {
		msg = i18n.T("dropdown-bitwarden-copied-clears", i18n.Str("field", field), i18n.Str("seconds", strconv.Itoa(int(ttl/time.Second))))
	}
	l.RemoveClass(css.Error)
	setCaption(l, msg)
}

func (v *bitwardenView) confirmReprompt() {
	req := v.copying
	password := v.repromptPw.entry.Text()
	if req == nil || password == "" {
		return
	}
	v.repromptPw.reset()
	v.copyNow(req.item, req.field, password)
}

func (v *bitwardenView) cancelReprompt() {
	v.copying = nil
	v.repromptPw.reset()
	v.setError(nil)
	v.pages.Show(bwPageVault)
}
