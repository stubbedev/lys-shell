package bar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// fakeEntry is one item of a fake vault: its listing and the values of
// its fields.
type fakeEntry struct {
	item   vault.Item
	values map[vault.Field]string
}

// entry lists it with the fields values has, in lys-secrets' order.
func entry(it vault.Item, values map[vault.Field]string) fakeEntry {
	it.Fields = nil
	for _, f := range []vault.Field{vault.FieldPassword, vault.FieldUsername, vault.FieldTOTP, vault.FieldCardNumber, vault.FieldCardCode, vault.FieldEmail, vault.FieldNotes} {
		if values[f] != "" {
			it.Fields = append(it.Fields, f)
		}
	}
	return fakeEntry{item: it, values: values}
}

// fakeVault records what the views ask of the vault and answers as
// lys-secrets would, with the errors the test sets.
type fakeVault struct {
	mu        sync.Mutex
	status    vault.Status
	entries   []fakeEntry
	master    string
	code      func() vault.Value
	logins    []vault.LoginRequest
	apiKeys   []vault.APIKeyRequest
	unlocks   []string
	reads     []string
	emails    int
	syncs     int
	loginErr  error
	unlockErr error
}

func (f *fakeVault) Status() vault.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeVault) Items() []vault.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status.State != vault.StateUnlocked {
		return nil
	}
	items := make([]vault.Item, len(f.entries))
	for i, e := range f.entries {
		items[i] = e.item
	}
	return items
}

func (f *fakeVault) Subscribe() (<-chan struct{}, func()) { return make(chan struct{}), func() {} }

func (f *fakeVault) Login(_ context.Context, req vault.LoginRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, req)
	if f.loginErr != nil {
		f.status.Err = f.loginErr
		return f.loginErr
	}
	f.status = vault.Status{State: vault.StateUnlocked, Email: req.Email}
	return nil
}

func (f *fakeVault) SendLoginEmail(context.Context, vault.LoginRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails++
	return nil
}

func (f *fakeVault) LoginAPIKey(_ context.Context, req vault.APIKeyRequest) error {
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
	f.status = vault.Status{State: vault.StateUnlocked, Email: f.status.Email}
	return nil
}

func (f *fakeVault) Sync(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	return nil
}

func (f *fakeVault) Lock(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = vault.Status{State: vault.StateLocked, Email: f.status.Email}
	return nil
}

func (f *fakeVault) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = vault.Status{State: vault.StateLoggedOut}
	return nil
}

// Field answers as lys-secrets does: the reprompt guards an item's
// secrets, a verification code comes from code.
func (f *fakeVault) Field(_ context.Context, id string, field vault.Field, password string) (vault.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, fmt.Sprintf("%s %d %s", id, field, password))
	if f.status.State != vault.StateUnlocked {
		return vault.Value{}, vault.ErrLocked
	}
	for _, e := range f.entries {
		if e.item.ID != id {
			continue
		}
		if !slices.Contains(e.item.Fields, field) {
			return vault.Value{}, vault.ErrNoSuchField
		}
		if e.item.Reprompt && field.Secret() {
			switch password {
			case "":
				return vault.Value{}, vault.ErrRepromptRequired
			case f.master:
			default:
				return vault.Value{}, vault.ErrWrongPassword
			}
		}
		if field == vault.FieldTOTP && f.code != nil {
			return f.code(), nil
		}
		return vault.Value{Text: e.values[field]}, nil
	}
	return vault.Value{}, vault.ErrNoSuchItem
}

func (f *fakeVault) set(fn func(f *fakeVault)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeVault) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reads)
}

// copied is one clipboard write the host made.
type copied struct {
	content transfer.Content
	ttl     time.Duration
}

func testBitwardenHost(v bwVault, cfg *config.Config, copies *[]copied) *bitwardenHost {
	return &bitwardenHost{
		life:   context.Background(),
		config: func() config.BitwardenConfig { return cfg.Bitwarden },
		copy: func(c transfer.Content, ttl time.Duration) error {
			*copies = append(*copies, copied{c, ttl})
			return nil
		},
		vault: func() (bwVault, error) { return v, nil },
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

func (focusHost) Focus(widget.Widget) error { return nil }

// waitIdle waits until the view's operation has applied.
func waitIdle(t *testing.T, v *bitwardenView) {
	t.Helper()
	waitHeadless(t, "the vault operation", func() bool { return !v.busy })
}

func page(v *bitwardenView) string { return onHeadlessLoop(v.pages.Visible) }

// The list shows at most max matches in vault order and counts them
// all.
func TestBitwardenMatches(t *testing.T) {
	items := []vault.Item{{Name: "Mail"}, {Name: "Bank"}, {Name: "Mail work"}, {Name: "Mailbox"}}
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
		vault.ErrWrongPassword:                           i18n.T("dropdown-bitwarden-error-wrong-password"),
		fmt.Errorf("unlock: %w", vault.ErrWrongPassword): i18n.T("dropdown-bitwarden-error-wrong-password"),
		vault.ErrInvalidCredentials:                      i18n.T("dropdown-bitwarden-error-credentials"),
		vault.ErrLoginRequired:                           i18n.T("dropdown-bitwarden-error-login-required"),
		&vault.ServerError{Status: 503}:                  i18n.T("dropdown-bitwarden-error-unavailable"),
		&vault.ServerError{Status: 400, Message: "no"}:   i18n.T("dropdown-bitwarden-error-server", i18n.Str("message", "no")),
		vault.ErrUnreachable:                             i18n.T("dropdown-bitwarden-error-network"),
		errors.New("odd"):                                "odd",
	} {
		if got := bwErrorText(err); got != want {
			t.Errorf("%v = %q, want %q", err, got, want)
		}
	}
}

// The label counts the open vault's items and is empty while locked.
func TestBitwardenLabel(t *testing.T) {
	if got := bitwardenLabel("{{ count }} items", vault.Status{State: vault.StateUnlocked, Items: 7}); got != "7 items" {
		t.Errorf("open = %q", got)
	}
	if got := bitwardenLabel("{{ count }}", vault.Status{State: vault.StateLocked, Items: 7}); got != "" {
		t.Errorf("locked = %q", got)
	}
}

// The bar icon follows the lock state; without the host the module
// refuses to build.
func TestBitwardenModuleIcon(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{status: vault.Status{State: vault.StateLocked}}
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
	v.set(func(f *fakeVault) { f.status.State = vault.StateUnlocked })
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
	v := &fakeVault{loginErr: &vault.TwoFactorRequiredError{Providers: []vault.TwoFactorProvider{vault.TwoFactorEmail, vault.TwoFactorAuthenticator}}}
	view, _ := bitwardenTestView(t, v, cfg)
	if page(view) != bwPageLogin || view.email.Text() != "ada@example.com" {
		t.Fatalf("page %s, email %q", page(view), view.email.Text())
	}
	view.password.entry.SetText("hunter2")
	onHeadlessLoop(func() struct{} { view.loginForm.Submit(); return struct{}{} })
	waitIdle(t, view)
	if len(v.logins) != 1 || v.logins[0].Server.String() != "https://vault.example.com" || v.logins[0].Password != "hunter2" || v.logins[0].TwoFactor != nil {
		t.Fatalf("logins = %+v", v.logins)
	}
	if page(view) != bwPageChallenge || view.errLabel.Visible() || view.sendEmail.Visible() {
		t.Fatalf("after the challenge: page %s, error %q shown %v, send email %v", page(view), view.errLabel.Text(), view.errLabel.Visible(), view.sendEmail.Visible())
	}
	v.set(func(f *fakeVault) { f.loginErr = nil })
	view.code.SetText("123456")
	onHeadlessLoop(func() struct{} { view.submitCode(); return struct{}{} })
	waitIdle(t, view)
	if len(v.logins) != 2 || v.logins[1].TwoFactor == nil || *v.logins[1].TwoFactor != (vault.TwoFactor{Provider: vault.TwoFactorAuthenticator, Code: "123456"}) {
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
	v := &fakeVault{loginErr: vault.ErrNewDeviceVerification}
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
		f.loginErr = &vault.TwoFactorRequiredError{Providers: []vault.TwoFactorProvider{vault.TwoFactorEmail}}
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
	v := &fakeVault{loginErr: vault.ErrInvalidCredentials}
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
	v := &fakeVault{status: vault.Status{State: vault.StateLocked, Email: "ada@example.com"}}
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
	return &fakeVault{status: vault.Status{State: vault.StateUnlocked}, master: "hunter2", entries: []fakeEntry{
		entry(vault.Item{ID: "1", Type: vault.ItemLogin, Name: "Mail", Username: "ada"}, map[vault.Field]string{vault.FieldUsername: "ada", vault.FieldPassword: "pw1"}),
		entry(vault.Item{ID: "2", Type: vault.ItemLogin, Name: "Bank", Username: "ada", Reprompt: true}, map[vault.Field]string{vault.FieldUsername: "ada", vault.FieldPassword: "pw2"}),
	}}
}

// copyAndWait copies f of it from the dropdown and waits for the copy
// to land.
func copyAndWait(t *testing.T, view *bitwardenView, it vault.Item, f vault.Field) {
	t.Helper()
	onHeadlessLoop(func() struct{} { view.copyField(it, f); return struct{}{} })
	waitIdle(t, view)
}

// A secret is copied marked for clipboard managers and cleared after
// clipboard-clear; the username is plain text kept; each copy reads the
// field from the vault.
func TestBitwardenCopy(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := bitwardenTestView(t, v, cfg)
	if page(view) != bwPageVault || rowCount(view.list) != 2 {
		t.Fatalf("page %s, %d rows", page(view), rowCount(view.list))
	}
	mail := v.entries[0].item
	copyAndWait(t, view, mail, vault.FieldPassword)
	copyAndWait(t, view, mail, vault.FieldUsername)
	if len(*copies) != 2 || !slices.Equal(v.reads, []string{"1 1 ", "1 0 "}) {
		t.Fatalf("%d copies, reads %q", len(*copies), v.reads)
	}
	pw, user := (*copies)[0], (*copies)[1]
	if b, _ := pw.content.Bytes("text/plain"); string(b) != "pw1" || !clipboard.IsSensitive(pw.content.Mimes) || pw.ttl != 30*time.Second {
		t.Errorf("password copy %q, mimes %v, ttl %v", b, pw.content.Mimes, pw.ttl)
	}
	if b, _ := user.content.Bytes("text/plain"); string(b) != "ada" || clipboard.IsSensitive(user.content.Mimes) || user.ttl != 0 {
		t.Errorf("username copy %q, mimes %v, ttl %v", b, user.content.Mimes, user.ttl)
	}
	cfg.Bitwarden.ClipboardClear = 0
	copyAndWait(t, view, mail, vault.FieldPassword)
	if got := (*copies)[2]; got.ttl != 0 || !clipboard.IsSensitive(got.content.Mimes) {
		t.Errorf("clipboard-clear 0: ttl %v, mimes %v", got.ttl, got.content.Mimes)
	}
}

// A protected item's secret waits for the master password, which goes
// to the vault with the read: a wrong one copies nothing and asks
// again, the right one copies; its username needs none.
func TestBitwardenReprompt(t *testing.T) {
	cfg := config.Defaults()
	v := unlockedVault()
	view, copies := bitwardenTestView(t, v, cfg)
	bank := v.entries[1].item
	copyAndWait(t, view, bank, vault.FieldUsername)
	if len(*copies) != 1 || page(view) != bwPageVault {
		t.Fatalf("username behind the reprompt: %d copies, page %s", len(*copies), page(view))
	}
	onHeadlessLoop(func() struct{} { view.copyField(bank, vault.FieldPassword); return struct{}{} })
	if len(*copies) != 1 || page(view) != bwPageReprompt || v.readCount() != 1 {
		t.Fatalf("password read without the reprompt: %d copies, page %s, reads %q", len(*copies), page(view), v.reads)
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
	if b, _ := (*copies)[1].content.Bytes("text/plain"); string(b) != "pw2" || !slices.Equal(v.reads, []string{"2 0 ", "2 1 wrong", "2 1 hunter2"}) {
		t.Errorf("copied %q after reads %q", b, v.reads)
	}
}

// Closing the popover empties every secret box and drops the code step
// and a pending reprompt; the email stays.
func TestBitwardenHideClearsSecrets(t *testing.T) {
	cfg := config.Defaults()
	v := &fakeVault{loginErr: vault.ErrNewDeviceVerification}
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
	onHeadlessLoop(func() struct{} { uv.copyField(u.entries[1].item, vault.FieldPassword); return struct{}{} })
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
	if !view.lockBtn.Visible() || !view.logoutBtn.Visible() {
		t.Errorf("open vault header: lock %v, logout %v", view.lockBtn.Visible(), view.logoutBtn.Visible())
	}
	onHeadlessLoop(func() struct{} { view.lock(); return struct{}{} })
	waitIdle(t, view)
	if page(view) != bwPageUnlock || view.lockBtn.Visible() || view.syncBtn.Visible() {
		t.Errorf("after lock: page %s, lock %v, sync %v", page(view), view.lockBtn.Visible(), view.syncBtn.Visible())
	}
}

// The vault's icons ship with the shell.
func TestBitwardenIconsAreBundled(t *testing.T) {
	cfg := config.DefaultsBitwarden()
	names := []string{cfg.IconLocked, cfg.IconUnlocked, "ld-shield-check-symbolic"}
	for f := vault.FieldUsername; f <= vault.FieldNotes; f++ {
		names = append(names, bwFieldIcon(f))
	}
	for _, ty := range []vault.ItemType{vault.ItemLogin, vault.ItemCard, vault.ItemIdentity, vault.ItemSecureNote, vault.ItemSSHKey} {
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
	locked, _ := bitwardenTestView(t, &fakeVault{status: vault.Status{State: vault.StateLocked}}, cfg)
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
	if got := newPanelBox(open, 0, 0).dropdownFocus(); got != widget.Widget(open.search) {
		t.Errorf("a sized panel forwards %T", got)
	}
	if got := newPanelBox(widget.NewBox(widget.Column, 0, 0), 0, 0).dropdownFocus(); got != nil {
		t.Errorf("plain content in a panel focuses %T", got)
	}
}

// hostFake is lys-secrets' side of the bus for the host's tests: the
// vault.Host a Daemon serves, recording what reached it.
type hostFake struct {
	mu      sync.Mutex
	status  vault.Status
	items   []vault.Item
	locks   int
	network map[string]vault.NetworkLogin
	created []vault.NewLogin
	sends   []vault.Send
}

func (h *hostFake) Status() vault.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *hostFake) Items() ([]vault.Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status.State != vault.StateUnlocked {
		return nil, vault.ErrLocked
	}
	return h.items, nil
}

func (h *hostFake) Field(string, vault.Field, string) (vault.Value, error) {
	return vault.Value{Text: "pw"}, nil
}

func (h *hostFake) Login(context.Context, vault.LoginRequest) error          { return nil }
func (h *hostFake) SendLoginEmail(context.Context, vault.LoginRequest) error { return nil }
func (h *hostFake) LoginAPIKey(context.Context, vault.APIKeyRequest) error   { return nil }
func (h *hostFake) Unlock(context.Context, string) error                     { return nil }
func (h *hostFake) Logout() error                                            { return nil }
func (h *hostFake) Sync(context.Context) error                               { return nil }

func (h *hostFake) Lock() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.locks++
	h.status.State = vault.StateLocked
}

func (h *hostFake) NetworkLogin(folder, name string) (vault.NetworkLogin, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.network[folder+"/"+name]
	return l, ok
}

func (h *hostFake) CreateLogin(_ context.Context, l vault.NewLogin) (vault.ItemID, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status.State != vault.StateUnlocked {
		return "", vault.ErrLocked
	}
	h.created = append(h.created, l)
	return vault.ItemID(fmt.Sprint("new-", len(h.created))), nil
}

func (h *hostFake) FindLogin(name string, scope vault.FolderScope) (vault.Item, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status.State != vault.StateUnlocked {
		return vault.Item{}, false, vault.ErrLocked
	}
	for _, it := range h.items {
		if it.Type == vault.ItemLogin && it.Name == name {
			return it, true, nil
		}
	}
	return vault.Item{}, false, nil
}

func (h *hostFake) Sends() ([]vault.Send, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status.State != vault.StateUnlocked {
		return nil, vault.ErrLocked
	}
	return h.sends, nil
}

func (h *hostFake) CreateTextSend(_ context.Context, s vault.NewTextSend) (vault.CreatedSend, error) {
	if err := s.Validate(time.Now()); err != nil {
		return vault.CreatedSend{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := vault.SendID(fmt.Sprint("send-", len(h.sends)+1))
	h.sends = append(h.sends, vault.Send{ID: id, Type: vault.SendText, Name: s.Name, MaxAccessCount: s.MaxAccessCount, HasPassword: s.Password != "", Deletion: s.Deletion})
	return vault.CreatedSend{ID: id, URL: "https://send.example/#" + string(id)}, nil
}

func (h *hostFake) DeleteSend(_ context.Context, id vault.SendID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, s := range h.sends {
		if s.ID == id {
			h.sends = append(h.sends[:i], h.sends[i+1:]...)
			return nil
		}
	}
	return vault.ErrNoSuchSend
}

func (h *hostFake) lockCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.locks
}

// serveVault serves host as lys-secrets' vault on the test's session
// bus, returning the daemon to signal changes with.
func serveVault(t *testing.T, host vault.Host) *vault.Daemon {
	t.Helper()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	d := vault.NewDaemon(context.Background(), host)
	release, err := d.Export(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return d
}

func sessionHost(t *testing.T, cfg *config.Config) *bitwardenHost {
	t.Helper()
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newBitwardenHost(life, nil, func() *config.Config { return cfg })
}

// The session lock locks lys-secrets' vault whether or not this shell
// opened it, and with no vault on the bus it starts none.
func TestBitwardenLocksWithTheSession(t *testing.T) {
	dbustest.Session(t)
	cfg := config.Defaults()
	if err := sessionHost(t, cfg).lock(context.Background()); err != nil {
		t.Fatalf("locking with no vault on the bus = %v", err)
	}
	host := &hostFake{status: vault.Status{State: vault.StateUnlocked}}
	serveVault(t, host)
	h := sessionHost(t, cfg)
	h.lockVault()
	waitFor(t, "the vault to lock", func() bool { return host.lockCount() == 1 })
}

// The host's vault mirrors lys-secrets: the status and listing as of
// the last Changed, re-read after an operation.
func TestBitwardenMirrorFollowsTheVault(t *testing.T) {
	dbustest.Session(t)
	host := &hostFake{status: vault.Status{State: vault.StateLocked, Email: "ada@example.com"}}
	d := serveVault(t, host)
	h := sessionHost(t, config.Defaults())
	v, err := h.vault()
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first read", func() bool { return v.Status().Email == "ada@example.com" })
	if v.Status().State != vault.StateLocked || v.Items() != nil {
		t.Errorf("locked mirror = %+v, %v", v.Status(), v.Items())
	}
	ticks, stop := v.Subscribe()
	defer stop()
	host.mu.Lock()
	host.status = vault.Status{State: vault.StateUnlocked, Email: "ada@example.com", Items: 1}
	host.items = []vault.Item{{ID: "1", Type: vault.ItemLogin, Name: "Mail", Fields: []vault.Field{vault.FieldPassword}}}
	host.mu.Unlock()
	if err := d.Changed(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick after Changed")
	}
	waitFor(t, "the listing", func() bool { return len(v.Items()) == 1 })
	if v.Items()[0].Name != "Mail" || v.Status().State != vault.StateUnlocked {
		t.Errorf("open mirror = %+v, %+v", v.Status(), v.Items())
	}
	if err := v.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v.Status().State != vault.StateLocked || v.Items() != nil {
		t.Errorf("after Lock the mirror = %+v, %v", v.Status(), v.Items())
	}
}

// The network agent's vault asks lys-secrets for the configured folder's
// login, custom fields by name; no folder configured asks nothing, and a
// vault that does not answer answers nothing.
func TestBitwardenNetworkVault(t *testing.T) {
	dbustest.Session(t)
	cfg := config.Defaults()
	cfg.Bitwarden.NetworkFolder = "Networks"
	h := sessionHost(t, cfg)
	nv := h.networkVault()
	if _, ok := nv.Entry("Home"); ok {
		t.Fatal("no vault on the bus answered")
	}
	serveVault(t, &hostFake{network: map[string]vault.NetworkLogin{
		"Networks/Home": {Username: "ann", Password: "hunter22", Fields: map[string]string{"pin": "1234"}},
	}})
	e, ok := nv.Entry("Home")
	if !ok || e.Username != "ann" || e.Password != "hunter22" || e.Fields["pin"] != "1234" {
		t.Fatalf("entry = %+v %v", e, ok)
	}
	if _, ok := nv.Entry("Office"); ok {
		t.Error("a connection with no login answered")
	}
	cfg.Bitwarden.NetworkFolder = ""
	if _, ok := nv.Entry("Home"); ok {
		t.Error("answered with no network folder configured")
	}
}

// waitFor polls cond until it holds or five seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Every periodic sync failure lys-secrets reports notifies; a forced
// logout says to log in again.
func TestBitwardenSyncNotifies(t *testing.T) {
	cfg := config.Defaults()
	h := testBitwardenHost(&fakeVault{}, cfg, &[]copied{})
	var notes []string
	h.notify = func(summary, body string) { notes = append(notes, summary+": "+body) }
	h.reportSync(vault.ErrUnreachable)
	if len(notes) != 1 || notes[0] != i18n.T("dropdown-bitwarden-notify-sync-failed")+": "+i18n.T("dropdown-bitwarden-error-network") {
		t.Fatalf("a failure notified %q", notes)
	}
	h.reportSync(fmt.Errorf("refresh: %w", vault.ErrLoginRequired))
	if len(notes) != 2 || !strings.HasPrefix(notes[1], i18n.T("dropdown-bitwarden-notify-logged-out")) {
		t.Errorf("a forced logout = %q", notes)
	}
}

// A verification code clears from the clipboard when it expires, if
// that is before clipboard-clear; the other secrets keep the setting.
func TestBitwardenTOTPCopyClearsAtExpiry(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bitwarden.ClipboardClear = 30
	h := testBitwardenHost(&fakeVault{}, cfg, &[]copied{})
	at := time.Unix(1_700_000_020, 0)
	code := vault.Value{Text: "123456", Expires: at.Add(20 * time.Second)}
	if ttl, err := h.copyValue(vault.FieldTOTP, code, at); err != nil || ttl != 20*time.Second {
		t.Errorf("TOTP ttl = %v, %v; want the code's 20 s left", ttl, err)
	}
	if ttl, _ := h.copyValue(vault.FieldPassword, vault.Value{Text: "pw"}, at); ttl != 30*time.Second {
		t.Errorf("password ttl = %v, want clipboard-clear", ttl)
	}
	cfg.Bitwarden.ClipboardClear = 5
	if ttl, _ := h.copyValue(vault.FieldTOTP, code, at); ttl != 5*time.Second {
		t.Errorf("TOTP ttl with a shorter clipboard-clear = %v", ttl)
	}
	cfg.Bitwarden.ClipboardClear = 0
	if ttl, _ := h.copyValue(vault.FieldTOTP, code, at); ttl != 20*time.Second {
		t.Errorf("TOTP ttl with clipboard-clear off = %v, want the code's expiry", ttl)
	}
}

// Copying from an item with a verification code shows that code live,
// with its seconds left, reading the next one once it expires, until
// the vault locks; an item behind the reprompt never shows it.
func TestBitwardenLiveTOTPRow(t *testing.T) {
	cfg := config.Defaults()
	at := time.Unix(1_700_000_020, 0)
	var clock sync.Mutex
	now := func() time.Time { clock.Lock(); defer clock.Unlock(); return at }
	period := func(t time.Time) time.Time { return t.Truncate(30 * time.Second).Add(30 * time.Second) }
	codes := map[int64]string{1_700_000_040: "123456", 1_700_000_070: "654321"}
	v := &fakeVault{status: vault.Status{State: vault.StateUnlocked}, master: "master", entries: []fakeEntry{
		entry(vault.Item{ID: "a", Type: vault.ItemLogin, Name: "Mail"}, map[vault.Field]string{vault.FieldPassword: "pw", vault.FieldTOTP: "seed"}),
		entry(vault.Item{ID: "b", Type: vault.ItemLogin, Name: "Bank", Reprompt: true}, map[vault.Field]string{vault.FieldUsername: "ada", vault.FieldPassword: "pw", vault.FieldTOTP: "seed"}),
	}}
	v.code = func() vault.Value {
		end := period(now())
		return vault.Value{Text: codes[end.Unix()], Expires: end}
	}
	view, _ := bitwardenTestView(t, v, cfg)
	onHeadlessLoop(func() bool { view.now = now; view.refreshTOTP(); return true })
	if onHeadlessLoop(func() bool { return view.totpSt.Visible() == "code" }) {
		t.Fatal("a live code shows before any copy")
	}
	caption := func() string {
		return onHeadlessLoop(func() string { view.refreshTOTP(); return strings.Join(treetest.Labels(view.totpSt), "|") })
	}
	copyAndWait(t, view, v.entries[0].item, vault.FieldPassword)
	waitFor(t, "the live code", func() bool { return strings.Contains(caption(), "123 456") })
	if c := caption(); !strings.Contains(c, "Mail") || !strings.Contains(c, "20") {
		t.Errorf("live row = %q, want Mail, 123 456 and 20 s", c)
	}
	clock.Lock()
	at = at.Add(30 * time.Second)
	clock.Unlock()
	waitFor(t, "the next period's code", func() bool { return strings.Contains(caption(), "654 321") })
	copyAndWait(t, view, v.entries[1].item, vault.FieldUsername)
	if c := caption(); strings.Contains(c, "Bank") {
		t.Errorf("a reprompt-protected item's code shows: %q", c)
	}
	v.set(func(f *fakeVault) { f.status.State = vault.StateLocked })
	onHeadlessLoop(func() bool { view.refreshTOTP(); return true })
	if onHeadlessLoop(func() bool { return view.totpSt.Visible() == "code" }) {
		t.Error("the live code shows on a locked vault")
	}
}
