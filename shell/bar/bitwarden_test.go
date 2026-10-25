package bar

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/styling"
)

// fakeVault records what the dropdown asks of the vault and answers
// with the errors the test sets.
type fakeVault struct {
	mu        sync.Mutex
	status    bitwarden.Status
	items     []bitwarden.Item
	logins    []bitwarden.LoginRequest
	apiKeys   []bitwarden.APIKeyRequest
	unlocks   []string
	verifies  []string
	emails    int
	syncs     int
	touches   int
	idle      []time.Duration
	loginErr  error
	unlockErr error
	verifyOK  string
}

func (f *fakeVault) Status() bitwarden.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeVault) Items() []bitwarden.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.items)
}

func (f *fakeVault) Subscribe() (<-chan struct{}, func()) { return make(chan struct{}), func() {} }

func (f *fakeVault) Login(_ context.Context, req bitwarden.LoginRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, req)
	if f.loginErr != nil {
		f.status.Err = f.loginErr
		return f.loginErr
	}
	f.status = bitwarden.Status{State: bitwarden.StateUnlocked, Email: req.Email}
	return nil
}

func (f *fakeVault) SendLoginEmail(context.Context, bitwarden.LoginRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails++
	return nil
}

func (f *fakeVault) LoginAPIKey(_ context.Context, req bitwarden.APIKeyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apiKeys = append(f.apiKeys, req)
	return nil
}

func (f *fakeVault) Unlock(_ context.Context, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlocks = append(f.unlocks, password)
	if f.unlockErr != nil {
		return f.unlockErr
	}
	f.status = bitwarden.Status{State: bitwarden.StateUnlocked, Email: f.status.Email}
	return nil
}

func (f *fakeVault) Sync(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	return nil
}

func (f *fakeVault) Lock() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = bitwarden.Status{State: bitwarden.StateLocked, Email: f.status.Email}
}

func (f *fakeVault) Logout() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = bitwarden.Status{State: bitwarden.StateLoggedOut}
	return nil
}

func (f *fakeVault) VerifyPassword(password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifies = append(f.verifies, password)
	if password != f.verifyOK {
		return bitwarden.ErrWrongPassword
	}
	return nil
}

func (f *fakeVault) Touch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches++
}

func (f *fakeVault) LockIdle(idle time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idle = append(f.idle, idle)
	return false
}

func (f *fakeVault) set(fn func(f *fakeVault)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// copied is one clipboard write the host made.
type copied struct {
	content transfer.Content
	ttl     time.Duration
}

func testBitwardenHost(v vault, cfg *config.Config, copies *[]copied) *bitwardenHost {
	return &bitwardenHost{
		life:   context.Background(),
		config: func() config.BitwardenConfig { return cfg.Bitwarden },
		copy: func(c transfer.Content, ttl time.Duration) error {
			*copies = append(*copies, copied{c, ttl})
			return nil
		},
		vault: func() (vault, error) { return v, nil },
	}
}

func bitwardenTestView(t *testing.T, v *fakeVault, cfg *config.Config) (*bitwardenView, *[]copied) {
	t.Helper()
	copies := &[]copied{}
	style := computeStyle(cfg, styling.Default())
	ctx := ModuleContext{Config: cfg, Font: testFont(t), Style: &style, Bitwarden: testBitwardenHost(v, cfg, copies)}
	view := bitwardenDropdown(ctx).(*bitwardenView)
	widget.SetSurfaceHost(view.Box, focusHost{})
	view.Arrange(render.Rect{W: 382, H: 600})
	t.Cleanup(view.dropdownClosed)
	return view, copies
}

// focusHost hosts a headless tree whose forms move the focus.
type focusHost struct{}

func (focusHost) OpenMenu(widget.Boundser, render.Font, float64, []widget.MenuItem, widget.MenuOptions) {
}

func (focusHost) OpenPopover(widget.Boundser, widget.Widget, widget.PopoverOptions) func() {
	return func() {}
}

func (focusHost) Focus(widget.Widget) {}

// waitIdle waits until the view's operation has applied.
func waitIdle(t *testing.T, v *bitwardenView) {
	t.Helper()
	waitHeadless(t, "the vault operation", func() bool { return !v.busy })
}

func page(v *bitwardenView) string { return onHeadlessLoop(v.pages.Visible) }

var testNowBW = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// The periodic sync is due an interval after the last sync or the last
// attempt, whichever is later, and only for an open vault that is not
// already syncing.
func TestSyncDue(t *testing.T) {
	open := bitwarden.Status{State: bitwarden.StateUnlocked, LastSync: testNowBW.Add(-30 * time.Minute)}
	if !syncDue(open, 30, time.Time{}, testNowBW) {
		t.Error("an open vault synced 30 minutes ago is not due a 30 minute sync")
	}
	for name, c := range map[string]struct {
		st          bitwarden.Status
		every       uint32
		lastAttempt time.Time
	}{
		"interval 0":     {open, 0, time.Time{}},
		"too soon":       {open, 31, time.Time{}},
		"locked":         {bitwarden.Status{State: bitwarden.StateLocked}, 30, time.Time{}},
		"syncing":        {bitwarden.Status{State: bitwarden.StateUnlocked, Syncing: true}, 30, time.Time{}},
		"failed lately":  {open, 30, testNowBW.Add(-time.Minute)},
		"never unlocked": {bitwarden.Status{}, 30, time.Time{}},
	} {
		if syncDue(c.st, c.every, c.lastAttempt, testNowBW) {
			t.Errorf("%s: sync due", name)
		}
	}
}

// The upkeep locks after lock-after minutes (0 never asks) and syncs
// when due, returning the attempt.
func TestBitwardenUpkeep(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateUnlocked, LastSync: testNowBW.Add(-time.Hour)}}
	h := testBitwardenHost(v, cfg, &[]copied{})
	if got := h.upkeep(v, testNowBW, time.Time{}); !got.Equal(testNowBW) || v.syncs != 1 {
		t.Errorf("due upkeep: attempt %v, %d syncs", got, v.syncs)
	}
	if !slices.Equal(v.idle, []time.Duration{15 * time.Minute}) {
		t.Errorf("idle locks asked: %v", v.idle)
	}
	cfg.Bitwarden.LockAfter = 0
	if got := h.upkeep(v, testNowBW.Add(time.Minute), testNowBW); !got.Equal(testNowBW) || v.syncs != 1 || len(v.idle) != 1 {
		t.Errorf("upkeep a minute later: attempt %v, %d syncs, idle %v", got, v.syncs, v.idle)
	}
}

// A login copies its password first (the row's click), then the
// username and the code; empty fields are left out.
func TestBitwardenFields(t *testing.T) {
	for _, c := range []struct {
		it   bitwarden.Item
		want []bwField
	}{
		{bitwarden.Item{Type: bitwarden.ItemLogin, Username: "ada", Password: "pw", TOTP: "JBSWY3DPEHPK3PXP"}, []bwField{bwPassword, bwUsername, bwTOTP}},
		{bitwarden.Item{Type: bitwarden.ItemLogin, Username: "ada"}, []bwField{bwUsername}},
		{bitwarden.Item{Type: bitwarden.ItemCard, Card: bitwarden.Card{Number: "4111", Code: "123"}}, []bwField{bwCardNumber, bwCardCode}},
		{bitwarden.Item{Type: bitwarden.ItemSecureNote, Notes: "body"}, []bwField{bwNotes}},
		{bitwarden.Item{Type: bitwarden.ItemSecureNote}, nil},
		{bitwarden.Item{Type: bitwarden.ItemSSHKey}, nil},
	} {
		if got := bwFields(c.it); !slices.Equal(got, c.want) {
			t.Errorf("fields of %+v = %v, want %v", c.it, got, c.want)
		}
	}
	if bwUsername.secret() || bwEmail.secret() || !bwPassword.secret() || !bwTOTP.secret() || !bwCardCode.secret() {
		t.Error("secret fields are the password, codes, card and notes; not the username or email")
	}
	code, err := bwTOTP.value(bitwarden.Item{TOTP: "JBSWY3DPEHPK3PXP"}, time.Unix(59, 0))
	if err != nil || len(code) != 6 {
		t.Errorf("TOTP value = %q, %v", code, err)
	}
	if _, err := bwTOTP.value(bitwarden.Item{TOTP: "otpauth://nope"}, testNowBW); err == nil {
		t.Error("a malformed TOTP secret gave a code")
	}
}

// The list shows at most max matches in vault order and counts them
// all.
func TestBitwardenMatches(t *testing.T) {
	items := []bitwarden.Item{{Name: "Mail"}, {Name: "Bank"}, {Name: "Mail work"}, {Name: "Mailbox"}}
	shown, total := bwMatches(items, "mail", 2)
	if total != 3 || len(shown) != 2 || shown[0].Name != "Mail" || shown[1].Name != "Mail work" {
		t.Errorf("matches = %v of %d", shown, total)
	}
	if shown, total := bwMatches(items, "nothing", 2); total != 0 || shown != nil {
		t.Errorf("no match = %v of %d", shown, total)
	}
}

// Each failure reads as what the user can do about it; an unknown one
// keeps its own text.
func TestBitwardenErrorText(t *testing.T) {
	for err, want := range map[error]string{
		bitwarden.ErrWrongPassword:                      i18n.T("dropdown-bitwarden-error-wrong-password"),
		bitwarden.ErrInvalidCredentials:                 i18n.T("dropdown-bitwarden-error-credentials"),
		bitwarden.ErrLoginRequired:                      i18n.T("dropdown-bitwarden-error-login-required"),
		&bitwarden.APIError{Status: 503}:                i18n.T("dropdown-bitwarden-error-unavailable"),
		&bitwarden.APIError{Status: 400, Message: "no"}: i18n.T("dropdown-bitwarden-error-server", i18n.Str("message", "no")),
		&url.Error{Op: "Post", Err: errors.New("dial")}: i18n.T("dropdown-bitwarden-error-network"),
		errors.New("odd"):                               "odd",
	} {
		if got := bwErrorText(err); got != want {
			t.Errorf("%v = %q, want %q", err, got, want)
		}
	}
}

// The label counts the open vault's items and is empty while locked.
func TestBitwardenLabel(t *testing.T) {
	if got := bitwardenLabel("{{ count }} items", bitwarden.Status{State: bitwarden.StateUnlocked, Items: 7}); got != "7 items" {
		t.Errorf("open = %q", got)
	}
	if got := bitwardenLabel("{{ count }}", bitwarden.Status{State: bitwarden.StateLocked, Items: 7}); got != "" {
		t.Errorf("locked = %q", got)
	}
}

// The bar icon follows the lock state; without the host the module
// refuses to build.
func TestBitwardenModuleIcon(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateLocked}}
	style := computeStyle(cfg, styling.Default())
	ctx := ModuleContext{Config: cfg, Font: testFont(t), Style: &style, Bitwarden: testBitwardenHost(v, cfg, &[]copied{}), gen: newMountGen()}
	t.Cleanup(ctx.gen.retire)
	mod, err := newBitwarden(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := mod.(*bitwardenModule)
	if got := m.icon.Name(); got != "ld-lock-symbolic" {
		t.Errorf("locked icon = %q", got)
	}
	v.set(func(f *fakeVault) { f.status.State = bitwarden.StateUnlocked })
	onHeadlessLoop(func() struct{} { m.render(); return struct{}{} })
	if got := m.icon.Name(); got != "ld-lock-open-symbolic" {
		t.Errorf("unlocked icon = %q", got)
	}
	ctx.Bitwarden = nil
	if _, err := newBitwarden(ctx); !errors.Is(err, errBitwardenNoHost) {
		t.Errorf("no host = %v", err)
	}
}

// A logged-out vault shows the login with the configured email; the
// login goes to the configured server, and a second factor turns into
// the code step (not an error) whose code rides the retry.
func TestBitwardenLoginWithSecondFactor(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bitwarden.Server = mustServer(t, "https://vault.example.com")
	cfg.Bitwarden.Email = "ada@example.com"
	v := &fakeVault{loginErr: &bitwarden.TwoFactorRequiredError{Providers: []bitwarden.TwoFactorProvider{bitwarden.TwoFactorEmail, bitwarden.TwoFactorAuthenticator}}}
	view, _ := bitwardenTestView(t, v, cfg)
	if page(view) != bwPageLogin || view.email.Text() != "ada@example.com" {
		t.Fatalf("page %s, email %q", page(view), view.email.Text())
	}
	view.password.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if len(v.logins) != 1 || v.logins[0].Server.API != "https://vault.example.com/api" || v.logins[0].Password != "hunter2" || v.logins[0].TwoFactor != nil {
		t.Fatalf("logins = %+v", v.logins)
	}
	if page(view) != bwPageChallenge || view.errLabel.Visible() || view.sendEmail.Visible() {
		t.Fatalf("after the challenge: page %s, error %q shown %v, send email %v", page(view), view.errLabel.Text(), view.errLabel.Visible(), view.sendEmail.Visible())
	}
	v.set(func(f *fakeVault) { f.loginErr = nil })
	view.code.SetText("123456")
	onHeadlessLoop(func() struct{} { view.submitCode(); return struct{}{} })
	waitIdle(t, view)
	if len(v.logins) != 2 || v.logins[1].TwoFactor == nil || *v.logins[1].TwoFactor != (bitwarden.TwoFactor{Provider: bitwarden.TwoFactorAuthenticator, Code: "123456"}) {
		t.Fatalf("retry = %+v", v.logins)
	}
	if page(view) != bwPageVault || view.pending != nil {
		t.Errorf("after the code: page %s, pending %+v", page(view), view.pending)
	}
}

// A new device's code rides the retry as the device OTP; an emailed
// second factor offers to send the code and sends nothing unasked.
func TestBitwardenNewDeviceAndEmailCode(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{loginErr: bitwarden.ErrNewDeviceVerification}
	view, _ := bitwardenTestView(t, v, cfg)
	view.email.SetText("ada@example.com")
	view.password.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if page(view) != bwPageChallenge || !view.pending.newDevice {
		t.Fatalf("page %s, pending %+v", page(view), view.pending)
	}
	view.code.SetText("998877")
	onHeadlessLoop(func() struct{} { view.submitCode(); return struct{}{} })
	waitIdle(t, view)
	if last := v.logins[len(v.logins)-1]; last.NewDeviceOTP != "998877" || last.TwoFactor != nil {
		t.Errorf("new device retry = %+v", last)
	}
	v.set(func(f *fakeVault) {
		f.loginErr = &bitwarden.TwoFactorRequiredError{Providers: []bitwarden.TwoFactorProvider{bitwarden.TwoFactorEmail}}
	})
	onHeadlessLoop(func() struct{} { view.back(); view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if !view.sendEmail.Visible() || v.emails != 0 {
		t.Fatalf("email factor: send shown %v, %d emails sent unasked", view.sendEmail.Visible(), v.emails)
	}
	onHeadlessLoop(func() struct{} { view.sendCode(); return struct{}{} })
	waitIdle(t, view)
	if v.emails != 1 {
		t.Errorf("send code sent %d emails", v.emails)
	}
}

// A refused login shows why and stays on the login; an empty password
// is refused by the form without asking the server.
func TestBitwardenLoginRefused(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{loginErr: bitwarden.ErrInvalidCredentials}
	view, _ := bitwardenTestView(t, v, cfg)
	view.email.SetText("ada@example.com")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	if len(v.logins) != 0 {
		t.Fatalf("an empty password reached the server: %+v", v.logins)
	}
	view.password.entry.SetText("nope")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if page(view) != bwPageLogin || view.errLabel.Text() != i18n.T("dropdown-bitwarden-error-credentials") || !view.errLabel.Visible() {
		t.Errorf("page %s, error %q", page(view), view.errLabel.Text())
	}
}

// The API key login sends the key and the master password.
func TestBitwardenAPIKeyLogin(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{}
	view, _ := bitwardenTestView(t, v, cfg)
	onHeadlessLoop(func() struct{} { view.toggleAPIKey(); return struct{}{} })
	view.email.SetText("ada@example.com")
	view.password.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	if len(v.apiKeys) != 0 {
		t.Fatal("an API key login without the key reached the server")
	}
	view.clientIDIn.SetText("user.abc")
	view.secretIn.entry.SetText("s3cret")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if len(v.apiKeys) != 1 || v.apiKeys[0].ClientID != "user.abc" || v.apiKeys[0].Secret != "s3cret" || v.apiKeys[0].Password != "hunter2" || len(v.logins) != 0 {
		t.Errorf("API key logins %+v, password logins %+v", v.apiKeys, v.logins)
	}
}

// A locked vault shows the unlock; unlocking hands over the password
// and empties the box; an empty box asks nothing.
func TestBitwardenUnlock(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateLocked, Email: "ada@example.com"}}
	view, _ := bitwardenTestView(t, v, cfg)
	if page(view) != bwPageUnlock {
		t.Fatalf("page %s", page(view))
	}
	onHeadlessLoop(func() struct{} { view.unlock(); return struct{}{} })
	if len(v.unlocks) != 0 {
		t.Fatal("an empty password unlocked")
	}
	view.unlockPw.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.unlock(); return struct{}{} })
	waitIdle(t, view)
	if !slices.Equal(v.unlocks, []string{"hunter2"}) || view.unlockPw.entry.Text() != "" {
		t.Errorf("unlocks %v, box %q", v.unlocks, view.unlockPw.entry.Text())
	}
}

func unlockedVault() *fakeVault {
	return &fakeVault{status: bitwarden.Status{State: bitwarden.StateUnlocked}, verifyOK: "hunter2", items: []bitwarden.Item{
		{ID: "1", Type: bitwarden.ItemLogin, Name: "Mail", Username: "ada", Password: "pw1"},
		{ID: "2", Type: bitwarden.ItemLogin, Name: "Bank", Username: "ada", Password: "pw2", Reprompt: true},
	}}
}

// A secret is copied marked for clipboard managers and cleared after
// clipboard-clear; the username is plain text kept; each copy is use.
func TestBitwardenCopy(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := bitwardenTestView(t, v, cfg)
	if page(view) != bwPageVault || rowCount(view.list) != 2 {
		t.Fatalf("page %s, %d rows", page(view), rowCount(view.list))
	}
	mail := v.items[0]
	onHeadlessLoop(func() struct{} { view.copyField(mail, bwPassword); view.copyField(mail, bwUsername); return struct{}{} })
	if len(*copies) != 2 || v.touches != 2 {
		t.Fatalf("%d copies, %d touches", len(*copies), v.touches)
	}
	pw, user := (*copies)[0], (*copies)[1]
	if b, _ := pw.content.Bytes("text/plain"); string(b) != "pw1" || !clipboard.IsSensitive(pw.content.Mimes) || pw.ttl != 30*time.Second {
		t.Errorf("password copy %q, mimes %v, ttl %v", b, pw.content.Mimes, pw.ttl)
	}
	if b, _ := user.content.Bytes("text/plain"); string(b) != "ada" || clipboard.IsSensitive(user.content.Mimes) || user.ttl != 0 {
		t.Errorf("username copy %q, mimes %v, ttl %v", b, user.content.Mimes, user.ttl)
	}
	cfg.Bitwarden.ClipboardClear = 0
	onHeadlessLoop(func() struct{} { view.copyField(mail, bwPassword); return struct{}{} })
	if got := (*copies)[2]; got.ttl != 0 || !clipboard.IsSensitive(got.content.Mimes) {
		t.Errorf("clipboard-clear 0: ttl %v, mimes %v", got.ttl, got.content.Mimes)
	}
}

// A protected item's secret waits for the master password: a wrong one
// copies nothing, the right one copies; its username needs none.
func TestBitwardenReprompt(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := bitwardenTestView(t, v, cfg)
	bank := v.items[1]
	onHeadlessLoop(func() struct{} { view.copyField(bank, bwUsername); return struct{}{} })
	if len(*copies) != 1 || page(view) != bwPageVault {
		t.Fatalf("username behind the reprompt: %d copies, page %s", len(*copies), page(view))
	}
	onHeadlessLoop(func() struct{} { view.copyField(bank, bwPassword); return struct{}{} })
	if len(*copies) != 1 || page(view) != bwPageReprompt {
		t.Fatalf("password copied without the reprompt: %d copies, page %s", len(*copies), page(view))
	}
	view.repromptPw.entry.SetText("wrong")
	onHeadlessLoop(func() struct{} { view.confirmReprompt(); return struct{}{} })
	waitIdle(t, view)
	if len(*copies) != 1 || page(view) != bwPageReprompt || view.errLabel.Text() != i18n.T("dropdown-bitwarden-error-wrong-password") {
		t.Fatalf("wrong password: %d copies, page %s, error %q", len(*copies), page(view), view.errLabel.Text())
	}
	view.repromptPw.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.confirmReprompt(); return struct{}{} })
	waitIdle(t, view)
	if len(*copies) != 2 || page(view) != bwPageVault {
		t.Fatalf("right password: %d copies, page %s", len(*copies), page(view))
	}
	if b, _ := (*copies)[1].content.Bytes("text/plain"); string(b) != "pw2" || !slices.Equal(v.verifies, []string{"wrong", "hunter2"}) {
		t.Errorf("copied %q after verifying %v", b, v.verifies)
	}
}

// Closing the popover empties every secret box and drops the code step
// and a pending reprompt; the email stays.
func TestBitwardenHideClearsSecrets(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{loginErr: bitwarden.ErrNewDeviceVerification}
	view, _ := bitwardenTestView(t, v, cfg)
	view.email.SetText("ada@example.com")
	view.password.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	view.code.SetText("12")
	onHeadlessLoop(func() struct{} { view.dropdownHidden(); return struct{}{} })
	if view.password.entry.Text() != "" || view.code.Text() != "" || view.pending != nil || page(view) != bwPageLogin || view.email.Text() != "ada@example.com" {
		t.Errorf("after hiding: password %q, code %q, pending %+v, page %s, email %q", view.password.entry.Text(), view.code.Text(), view.pending, page(view), view.email.Text())
	}
	u := unlockedVault()
	uv, _ := bitwardenTestView(t, u, cfg)
	onHeadlessLoop(func() struct{} { uv.copyField(u.items[1], bwPassword); return struct{}{} })
	uv.repromptPw.entry.SetText("hunt")
	onHeadlessLoop(func() struct{} { uv.dropdownHidden(); return struct{}{} })
	if uv.copying != nil || uv.repromptPw.entry.Text() != "" || page(uv) != bwPageVault {
		t.Errorf("after hiding the reprompt: pending %+v, box %q, page %s", uv.copying, uv.repromptPw.entry.Text(), page(uv))
	}
}

// Locking from the header leaves the vault page for the unlock and
// empties the list.
func TestBitwardenLockFromHeader(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, _ := bitwardenTestView(t, v, cfg)
	if !view.lockBtn.Visible() || view.logoutBtn.Visible() != true {
		t.Errorf("open vault header: lock %v, logout %v", view.lockBtn.Visible(), view.logoutBtn.Visible())
	}
	v.set(func(f *fakeVault) { f.items = nil })
	onHeadlessLoop(func() struct{} { view.lock(); view.apply(); return struct{}{} })
	if page(view) != bwPageUnlock || view.lockBtn.Visible() || view.syncBtn.Visible() {
		t.Errorf("after lock: page %s, lock %v, sync %v", page(view), view.lockBtn.Visible(), view.syncBtn.Visible())
	}
}

// The vault's icons ship with the shell.
func TestBitwardenIconsAreBundled(t *testing.T) {
	cfg := config.DefaultsBitwarden()
	names := []string{cfg.IconLocked, cfg.IconUnlocked, "ld-shield-check-symbolic"}
	for _, f := range []bwField{bwUsername, bwPassword, bwTOTP, bwCardNumber, bwCardCode, bwEmail, bwNotes} {
		names = append(names, f.icon())
	}
	for _, ty := range []bitwarden.ItemType{bitwarden.ItemLogin, bitwarden.ItemCard, bitwarden.ItemIdentity, bitwarden.ItemSecureNote, bitwarden.ItemSSHKey} {
		names = append(names, bwItemIcon(ty))
	}
	for _, name := range names {
		assertBundledIcon(t, name)
	}
}

// rowCount is how many item rows w holds.
func rowCount(w widget.Widget) int {
	n := 0
	walkTree(w, func(c widget.Widget) bool {
		if _, ok := c.(*widget.Item); ok {
			n++
		}
		return true
	})
	return n
}

func assertBundledIcon(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join("..", "..", "resources", "icons", "hicolor", "scalable", "actions", name+".svg")); err != nil {
		t.Errorf("icon %q is not bundled: %v", name, err)
	}
}

func mustServer(t *testing.T, s string) config.BitwardenServer {
	t.Helper()
	b, err := config.ParseBitwardenServer(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The popover opens with the page's input focused: the email while it
// is empty, else the password; the master password while locked; the
// search while open. An unavailable vault focuses nothing.
func TestBitwardenInitialFocus(t *testing.T) {
	cfg := config.Defaults()
	view, _ := bitwardenTestView(t, &fakeVault{}, cfg)
	if got := view.dropdownFocus(); got != widget.Widget(view.email) {
		t.Errorf("empty login focuses %T", got)
	}
	view.email.SetText("ada@example.com")
	if got := view.dropdownFocus(); got != widget.Widget(view.password.entry) {
		t.Errorf("filled email focuses %T", got)
	}
	locked, _ := bitwardenTestView(t, &fakeVault{status: bitwarden.Status{State: bitwarden.StateLocked}}, cfg)
	if got := locked.dropdownFocus(); got != widget.Widget(locked.unlockPw.entry) {
		t.Errorf("locked focuses %T", got)
	}
	open, _ := bitwardenTestView(t, unlockedVault(), cfg)
	if got := open.dropdownFocus(); got != widget.Widget(open.search) {
		t.Errorf("open focuses %T", got)
	}
	style := computeStyle(cfg, styling.Default())
	broken := bitwardenDropdown(ModuleContext{Config: cfg, Font: testFont(t), Style: &style}).(*bitwardenView)
	if got := broken.dropdownFocus(); got != nil {
		t.Errorf("no vault focuses %T", got)
	}
	if got := newPanelBox(open).dropdownFocus(); got != widget.Widget(open.search) {
		t.Errorf("a sized panel forwards %T", got)
	}
	if got := newPanelBox(widget.NewBox(widget.Column, 0, 0)).dropdownFocus(); got != nil {
		t.Errorf("plain content in a panel focuses %T", got)
	}
}

// The session lock leaves a vault the shell never opened unopened (no
// state directory appears) and reaches the vault once it is open.
func TestBitwardenLocksWithTheSession(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := config.Defaults()
	h := newBitwardenHost(life, nil, func() *config.Config { return cfg })
	h.lockIfOpen()
	if _, err := os.Stat(filepath.Join(state, "lys-shell", "bitwarden")); !os.IsNotExist(err) {
		t.Fatalf("locking an unopened vault opened it: %v", err)
	}
	v, err := h.vault()
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	opened := h.opened
	h.mu.Unlock()
	if opened != v {
		t.Fatalf("the session lock does not reach the opened vault: %v", opened)
	}
	h.lockIfOpen()
	if st := v.Status().State; st != bitwarden.StateLoggedOut {
		t.Errorf("a logged-out vault locked into %v", st)
	}
}
