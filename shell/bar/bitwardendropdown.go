package bar

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/shell/widgets"
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

// bwField is one value an item row copies.
type bwField int

const (
	bwUsername bwField = iota
	bwPassword
	bwTOTP
	bwCardNumber
	bwCardCode
	bwEmail
	bwNotes
)

// secret reports whether the field is a secret: offered marked for
// clipboard managers to skip, cleared after clipboard-clear, and behind
// the item's master password reprompt.
func (f bwField) secret() bool {
	switch f {
	case bwUsername, bwEmail:
		return false
	}
	return true
}

// icon is the field's copy button glyph.
func (f bwField) icon() string {
	switch f {
	case bwUsername:
		return "ld-user-symbolic"
	case bwTOTP:
		return "ld-clock-symbolic"
	case bwCardNumber:
		return "ld-credit-card-symbolic"
	case bwEmail:
		return "ld-mail-symbolic"
	case bwNotes:
		return "ld-copy-symbolic"
	}
	return "ld-key-round-symbolic"
}

// name is the field as the copy messages say it.
func (f bwField) name() string {
	switch f {
	case bwUsername:
		return i18n.T("dropdown-bitwarden-field-username")
	case bwTOTP:
		return i18n.T("dropdown-bitwarden-field-totp")
	case bwCardNumber:
		return i18n.T("dropdown-bitwarden-field-card-number")
	case bwCardCode:
		return i18n.T("dropdown-bitwarden-field-card-code")
	case bwEmail:
		return i18n.T("dropdown-bitwarden-field-email")
	case bwNotes:
		return i18n.T("dropdown-bitwarden-field-notes")
	}
	return i18n.T("dropdown-bitwarden-field-password")
}

// value is the field's text in it; a TOTP is the code current at now.
func (f bwField) value(it bitwarden.Item, now time.Time) (string, error) {
	switch f {
	case bwUsername:
		return it.Username, nil
	case bwPassword:
		return it.Password, nil
	case bwTOTP:
		t, err := bitwarden.ParseTOTP(it.TOTP)
		if err != nil {
			return "", err
		}
		code, _ := t.Code(now)
		return code, nil
	case bwCardNumber:
		return it.Card.Number, nil
	case bwCardCode:
		return it.Card.Code, nil
	case bwEmail:
		return it.Identity.Email, nil
	}
	return it.Notes, nil
}

// bwFields is what an item's row copies, its primary field first (the
// one a click on the row copies).
func bwFields(it bitwarden.Item) []bwField {
	var fs []bwField
	add := func(f bwField, v string) {
		if v != "" {
			fs = append(fs, f)
		}
	}
	switch it.Type {
	case bitwarden.ItemLogin:
		add(bwPassword, it.Password)
		add(bwUsername, it.Username)
		add(bwTOTP, it.TOTP)
	case bitwarden.ItemCard:
		add(bwCardNumber, it.Card.Number)
		add(bwCardCode, it.Card.Code)
	case bitwarden.ItemIdentity:
		add(bwEmail, it.Identity.Email)
		add(bwUsername, it.Identity.Username)
	case bitwarden.ItemSecureNote:
		add(bwNotes, it.Notes)
	}
	return fs
}

// bwItemIcon is an item's row glyph by its type.
func bwItemIcon(t bitwarden.ItemType) string {
	switch t {
	case bitwarden.ItemCard:
		return "ld-credit-card-symbolic"
	case bitwarden.ItemIdentity:
		return "ld-id-card-symbolic"
	case bitwarden.ItemSecureNote:
		return "ld-file-text-symbolic"
	case bitwarden.ItemSSHKey:
		return "ld-terminal-symbolic"
	}
	return "ld-globe-symbolic"
}

// bwItemCaption is the row's second line: the login's username, the
// card's brand, the item's folder.
func bwItemCaption(it bitwarden.Item) string {
	switch {
	case it.Undecryptable:
		return i18n.T("dropdown-bitwarden-undecryptable")
	case it.Type == bitwarden.ItemLogin && it.Username != "":
		return it.Username
	case it.Type == bitwarden.ItemCard && it.Card.Brand != "":
		return it.Card.Brand
	}
	return it.Folder
}

// bwMatches is the items matching query in vault order, at most max,
// and how many matched in all.
func bwMatches(items []bitwarden.Item, query string, max int) ([]bitwarden.Item, int) {
	var shown []bitwarden.Item
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
	var api *bitwarden.APIError
	var netErr *url.Error
	switch {
	case errors.Is(err, bitwarden.ErrWrongPassword):
		return i18n.T("dropdown-bitwarden-error-wrong-password")
	case errors.Is(err, bitwarden.ErrInvalidCredentials):
		return i18n.T("dropdown-bitwarden-error-credentials")
	case errors.Is(err, bitwarden.ErrLoginRequired):
		return i18n.T("dropdown-bitwarden-error-login-required")
	case errors.As(err, &api) && api.Retryable():
		return i18n.T("dropdown-bitwarden-error-unavailable")
	case errors.As(err, &api):
		return i18n.T("dropdown-bitwarden-error-server", i18n.Str("message", api.Message))
	case errors.As(err, &netErr):
		return i18n.T("dropdown-bitwarden-error-network")
	}
	return err.Error()
}

// bwStateUnknown is the view's state before its first apply, so that
// apply sets up whatever page the vault starts on.
const bwStateUnknown bitwarden.State = -1

// bwChallenge is a login waiting on a code: the attempt to repeat and
// what the server asked for.
type bwChallenge struct {
	req       bitwarden.LoginRequest
	newDevice bool
	provider  bitwarden.TwoFactorProvider
}

// bwCopyRequest is a copy waiting on the master password reprompt.
type bwCopyRequest struct {
	item  bitwarden.Item
	field bwField
}

// bitwardenView is the vault dropdown: the login (and its code step),
// the unlock, the vault search, the master password reprompt. Network
// operations run off the loop one at a time; secrets typed into it are
// cleared when the popover hides.
type bitwardenView struct {
	popoverHook
	*widget.Box

	ctx   ModuleContext
	host  *bitwardenHost
	vault vault
	font  render.Font
	px    float64
	now   func() time.Time

	life   context.Context
	cancel context.CancelFunc
	once   sync.Once

	busy    bool
	state   bitwarden.State
	pending *bwChallenge
	copying *bwCopyRequest

	spinner   *widget.Spinner
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
	list   *widget.ItemGroup
	listSt *widget.Stack
	more   *widget.Label
	status *widget.Label

	repromptDesc *widget.Label
	repromptPw   *secretEntry
}

func bitwardenDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &bitwardenView{ctx: ctx, host: ctx.Bitwarden, font: font, px: px, now: time.Now, state: bwStateUnknown}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "bitwarden-dropdown")
	v.spinner = widget.NewSpinner(int(px))
	v.spinner.SetVisible(false)
	v.syncBtn = v.iconButton("ld-refresh-cw-symbolic", i18n.T("dropdown-bitwarden-sync"), v.sync)
	v.lockBtn = v.iconButton("ld-lock-symbolic", i18n.T("dropdown-bitwarden-lock"), v.lock)
	v.logoutBtn = v.iconButton("ld-log-out-symbolic", i18n.T("dropdown-bitwarden-logout"), v.logout)
	v.Append(dropdownHeader(font, "ld-shield-check-symbolic", i18n.T("dropdown-bitwarden-title"), v.spinner, v.syncBtn, v.lockBtn, v.logoutBtn), false)
	content := dropdownContent()
	v.errLabel = v.caption(css.Error)
	v.pages = pageStack()
	v.pages.Add(bwPageLogin, v.buildLogin())
	v.pages.Add(bwPageChallenge, v.buildChallenge())
	v.pages.Add(bwPageUnlock, v.buildUnlock())
	v.pages.Add(bwPageVault, v.buildVault())
	v.pages.Add(bwPageReprompt, v.buildReprompt())
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
	go func() {
		defer stop()
		for {
			select {
			case <-life.Done():
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				v.ctx.Invoke(v.apply)
			}
		}
	}()
}

// showError replaces the pages with why there is no vault.
func (v *bitwardenView) showError(err error) {
	log.Printf("bitwarden: %v", err)
	v.pages.Add(bwPageError, widgets.EmptyState(v.font, v.px, "ld-shield-check-symbolic", i18n.T("dropdown-bitwarden-unavailable"), err.Error()))
	v.pages.Show(bwPageError)
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
	return dropdownButton(widget.NewLabel(v.font, v.px*0.85, text, 0), onClick, classes...)
}

// caption is a wrapped secondary line, hidden while empty.
func (v *bitwardenView) caption(classes ...string) *widget.Label {
	l := widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	v.server = widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	v.apiLabel = widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	v.codeDesc = widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	v.account = widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	v.repromptDesc = widget.NewLabel(v.font, v.px*0.85, "", 0)
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
	changed := st.State != v.state
	v.state = st.State
	v.spinner.SetVisible(v.busy || st.Syncing)
	v.spinner.SetSpinning(v.busy || st.Syncing)
	v.syncBtn.SetVisible(st.State == bitwarden.StateUnlocked)
	v.lockBtn.SetVisible(st.State == bitwarden.StateUnlocked)
	v.logoutBtn.SetVisible(st.State != bitwarden.StateLoggedOut)
	v.syncBtn.SetEnabled(!v.busy && !st.Syncing)
	v.pages.SetEnabled(!v.busy)
	if !v.busy {
		v.setError(st.Err)
	}
	cfg := v.host.config()
	v.server.SetText(i18n.T("dropdown-bitwarden-server", i18n.Str("server", cfg.Server.String())))
	v.account.SetText(i18n.T("dropdown-bitwarden-account", i18n.Str("email", st.Email)))
	switch st.State {
	case bitwarden.StateLoggedOut:
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
	case bitwarden.StateLocked:
		if changed || v.pages.Visible() != bwPageUnlock {
			v.clearSecrets()
			setCaption(v.status, "")
			v.pages.Show(bwPageUnlock)
		}
	case bitwarden.StateUnlocked:
		if v.copying == nil {
			v.pages.Show(bwPageVault)
		}
		v.rebuildList()
	}
}

// setError shows err under the page; the server asking for a code is
// the code step, not an error.
func (v *bitwardenView) setError(err error) {
	var tf *bitwarden.TwoFactorRequiredError
	text := ""
	if err != nil && !errors.As(err, &tf) && !errors.Is(err, bitwarden.ErrNewDeviceVerification) {
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
		req := bitwarden.APIKeyRequest{Server: server, Email: email, ClientID: v.clientIDIn.Text(), Secret: v.secretIn.entry.Text(), Password: password}
		v.run(func(ctx context.Context) error { return v.vault.LoginAPIKey(ctx, req) }, nil)
		return
	}
	v.attempt(bitwarden.LoginRequest{Server: server, Email: email, Password: password})
}

// attempt logs in, turning the server's ask for a code into the code
// step.
func (v *bitwardenView) attempt(req bitwarden.LoginRequest) {
	v.run(func(ctx context.Context) error { return v.vault.Login(ctx, req) }, func(err error) {
		var tf *bitwarden.TwoFactorRequiredError
		switch {
		case errors.As(err, &tf):
			v.challenge(&bwChallenge{req: req, provider: bwProvider(tf.Providers)})
		case errors.Is(err, bitwarden.ErrNewDeviceVerification):
			v.challenge(&bwChallenge{req: req, newDevice: true})
		case err == nil:
			v.pending = nil
		}
	})
}

// bwProvider picks the second factor to ask for: the authenticator app
// when the account has one, else the emailed code.
func bwProvider(ps []bitwarden.TwoFactorProvider) bitwarden.TwoFactorProvider {
	for _, p := range ps {
		if p == bitwarden.TwoFactorAuthenticator {
			return p
		}
	}
	return bitwarden.TwoFactorEmail
}

// challenge shows the code step for c.
func (v *bitwardenView) challenge(c *bwChallenge) {
	v.pending = c
	v.code.SetText("")
	switch {
	case c.newDevice:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-new-device-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-new-device-desc", i18n.Str("email", c.req.Email)))
	case c.provider == bitwarden.TwoFactorEmail:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-two-factor-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-two-factor-email", i18n.Str("email", c.req.Email)))
	default:
		v.codeTitle.SetText(i18n.T("dropdown-bitwarden-two-factor-title"))
		v.codeDesc.SetText(i18n.T("dropdown-bitwarden-two-factor-app"))
	}
	v.sendEmail.SetVisible(!c.newDevice && c.provider == bitwarden.TwoFactorEmail)
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
		req.TwoFactor = &bitwarden.TwoFactor{Provider: c.provider, Code: v.code.Text()}
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
	v.vault.Lock()
}

func (v *bitwardenView) logout() {
	v.run(func(context.Context) error { return v.vault.Logout() }, nil)
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
func (v *bitwardenView) row(it bitwarden.Item) widget.Widget {
	row := widget.NewItem(v.font, it.Name, bwItemCaption(it))
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.SetMedia(widget.NewThemeIcon(bwItemIcon(it.Type), int(v.px*1.2)), false)
	fields := bwFields(it)
	if len(fields) > 0 {
		row.SetOnActivate(func() { v.copyField(it, fields[0]) })
	}
	for _, f := range fields {
		b := v.iconButton(f.icon(), i18n.T("dropdown-bitwarden-copy-field", i18n.Str("field", f.name())), func() { v.copyField(it, f) })
		row.AddAction(b)
	}
	return row
}

// copyField copies one field, asking for the master password first when
// the item is protected by the reprompt and the field is a secret.
func (v *bitwardenView) copyField(it bitwarden.Item, f bwField) {
	if it.Reprompt && f.secret() {
		v.copying = &bwCopyRequest{item: it, field: f}
		v.repromptDesc.SetText(i18n.T("dropdown-bitwarden-reprompt-desc", i18n.Str("field", f.name()), i18n.Str("item", it.Name)))
		v.repromptPw.reset()
		v.pages.Show(bwPageReprompt)
		v.focus(v.repromptPw.entry)
		return
	}
	v.copyNow(it, f)
}

// copyNow puts the field on the clipboard and says so.
func (v *bitwardenView) copyNow(it bitwarden.Item, f bwField) {
	ttl, err := v.host.copyField(v.vault, it, f, v.now())
	if err != nil {
		v.setStatus(err)
		return
	}
	msg := i18n.T("dropdown-bitwarden-copied", i18n.Str("field", f.name()))
	if ttl > 0 {
		msg = i18n.T("dropdown-bitwarden-copied-clears", i18n.Str("field", f.name()), i18n.Str("seconds", strconv.Itoa(int(ttl/time.Second))))
	}
	v.status.RemoveClass(css.Error)
	setCaption(v.status, msg)
}

func (v *bitwardenView) setStatus(err error) {
	log.Printf("bitwarden: copy: %v", err)
	v.status.AddClass(css.Error)
	setCaption(v.status, bwErrorText(err))
}

func (v *bitwardenView) confirmReprompt() {
	req := v.copying
	password := v.repromptPw.entry.Text()
	if req == nil || password == "" {
		return
	}
	v.repromptPw.reset()
	v.run(func(context.Context) error { return v.vault.VerifyPassword(password) }, func(err error) {
		if err != nil {
			v.focus(v.repromptPw.entry)
			return
		}
		v.copying = nil
		v.copyNow(req.item, req.field)
	})
}

func (v *bitwardenView) cancelReprompt() {
	v.copying = nil
	v.repromptPw.reset()
	v.setError(nil)
	v.pages.Show(bwPageVault)
}
