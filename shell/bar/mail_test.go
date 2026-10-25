package bar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/mail"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// staticNotmuch answers every query with one count and has no
// database, so Run publishes the seed and then idles.
type staticNotmuch uint32

func (n staticNotmuch) Count(context.Context, string) uint32             { return uint32(n) }
func (staticNotmuch) Newest(context.Context, string, int) []mail.Message { return nil }
func (staticNotmuch) DatabasePath(context.Context) (string, bool)        { return "", false }

func seededMail(t *testing.T, cfg config.MailConfig, count uint32) *mail.Service {
	t.Helper()
	svc := mail.New(cfg, staticNotmuch(count), nil)
	ticks, stop := svc.Subscribe()
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	select {
	case <-ticks:
	case <-time.After(2 * time.Second):
		t.Fatal("mail service never published its seed")
	}
	return svc
}

func TestMailModuleShowsTheServiceTotal(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	ctx.Mail = seededMail(t, cfg.Mail, 7)
	m, err := newMail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mm := m.(*mailModule)
	defer mm.Stop()
	if got := mm.btn.Label().Text(); got != "7" {
		t.Errorf("label = %q, want the service's 7", got)
	}
	// A bar button with the mail icon, as the Rust BarButton is.
	if _, ok := mm.Root().(*barButton); !ok || mm.icon == nil || mm.icon.Name() != cfg.Mail.IconName {
		t.Errorf("root %T icon %v, want a bar button with %q", mm.Root(), mm.icon, cfg.Mail.IconName)
	}
}

// hide-when-zero collapses the module root at zero and only then; the
// label keeps the count.
func TestMailHidesWhenZero(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mail.HideWhenZero = true
	ctx := newTestContext(t, cfg)
	ctx.Mail = seededMail(t, cfg.Mail, 0)
	m, err := newMail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mm := m.(*mailModule)
	defer mm.Stop()
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { mm.setChrome(chrome); return true })
	if onHeadlessLoop(func() bool { return chrome.Visible() }) {
		t.Error("zero unread under hide-when-zero is shown")
	}
	onHeadlessLoop(func() bool { mm.apply(2); return true })
	if !onHeadlessLoop(func() bool { return chrome.Visible() }) || mm.btn.Label().Text() != "2" {
		t.Errorf("two unread: visible %v label %q", chrome.Visible(), mm.btn.Label().Text())
	}
	cfg.Mail.HideWhenZero = false
	onHeadlessLoop(func() bool { mm.apply(0); return true })
	if !onHeadlessLoop(func() bool { return chrome.Visible() }) || mm.btn.Label().Text() != "0" {
		t.Errorf("zero without hide-when-zero: visible %v label %q", chrome.Visible(), mm.btn.Label().Text())
	}
}

func TestMailModuleWithoutTheServiceErrors(t *testing.T) {
	if _, err := newMail(newTestContext(t, config.Defaults())); !errors.Is(err, errMailNoService) {
		t.Errorf("err = %v, want errMailNoService", err)
	}
}

func TestMailLabel(t *testing.T) {
	cfg := config.DefaultsMail()
	if got := mailLabel(cfg.Format, 3); got != "3" {
		t.Errorf("= %q, want 3", got)
	}
	if got := mailLabel(cfg.Format, 0); got != "0" {
		t.Errorf("zero = %q, want 0", got)
	}
	if got := mailLabel("Unread: {{ count }}", 12); got != "Unread: 12" {
		t.Errorf("format = %q", got)
	}
}

func TestLoadFileAppliesMailAccounts(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	good := "modules:\n  mail:\n    accounts:\n      - name: Work\n        query: \"folder:work and tag:unread\"\n        provider: gmail\n    format: \"{{ count }} new\"\n    hide-when-zero: false\n"
	if err := osWrite(path, good); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(c.Mail.Accounts) != 1 {
		t.Fatalf("accounts = %+v", c.Mail.Accounts)
	}
	account := c.Mail.Accounts[0]
	if account.Name != "Work" || account.Query != "folder:work and tag:unread" || account.Provider != "gmail" {
		t.Errorf("account = %+v", account)
	}
	if c.Mail.Format != "{{ count }} new" || c.Mail.HideWhenZero {
		t.Errorf("mail = %+v", c.Mail)
	}

	for _, bad := range []string{
		"modules:\n  mail:\n    accounts:\n      - query: \"tag:unread\"\n",
		"modules:\n  mail:\n    accounts:\n      - name: Work\n",
	} {
		if err := osWrite(path, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Errorf("%q: want a load error", bad)
		}
	}
}

func TestMailDropdownRowsAndEmptyState(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	v := mailDropdown(ctx).(*mailView)
	if v.pages.Visible() != "empty" || len(mailRows(v)) != 0 {
		t.Fatalf("no service: page %q, %d rows; want the empty state", v.pages.Visible(), len(mailRows(v)))
	}
	if itemText(v.pages, "title") != i18n.T("dropdown-mail-empty") {
		t.Errorf("the empty state reads %q", itemText(v.pages, "title"))
	}
	mc := cfg.Mail
	mc.Accounts = []config.MailAccount{{Name: "Work", Query: "tag:work"}, {Name: "Home", Query: "tag:home"}}
	ctx.Mail = seededMail(t, mc, 0)
	v = mailDropdown(ctx).(*mailView)
	rows := mailRows(v)
	if v.pages.Visible() != "accounts" || len(rows) != 2 {
		t.Fatalf("page %q, rows = %d, want one per account", v.pages.Visible(), len(rows))
	}
	count := treetest.First[*widget.Badge](t, rows[0])
	if count.Text() != "0" || count.Variant() != widget.BadgeSecondary {
		t.Errorf("zero count = %q variant %v, want the secondary badge", count.Text(), count.Variant())
	}
	ctx.Mail = seededMail(t, mc, 3)
	v = mailDropdown(ctx).(*mailView)
	if count := treetest.First[*widget.Badge](t, mailRows(v)[0]); count.Text() != "3" || count.Variant() == widget.BadgeSecondary {
		t.Errorf("unread count = %q variant %v, want the default badge", count.Text(), count.Variant())
	}
}

// mailRows is the dropdown's account rows in order.
func mailRows(v *mailView) []*widget.Item {
	return treetest.All[*widget.Item](v.list)
}
