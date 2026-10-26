package bar

import (
	"strconv"
	"sync"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/mail"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// mailView is the mail dropdown: an item card of the accounts (each
// one's icon, name, and unread count as a badge, secondary at zero),
// or the empty state, rebuilt as the service changes while open.
type mailView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	pages *widget.Stack
	list  *widget.ItemGroup

	once sync.Once
	stop chan struct{}
}

func mailDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &mailView{ctx: ctx, font: font, px: px, stop: make(chan struct{})}
	// The popover frame: the header strip full width over the content
	// (mail/mod.rs:36-78).
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "mail-dropdown")
	v.Append(dropdownHeader(font, "ld-mail-symbolic", i18n.T("dropdown-mail-title")), false)
	card, list := itemCard()
	card.AddClass("mail-dropdown-list")
	v.list = list
	v.pages = pageStack()
	v.pages.Add("accounts", growingScroll(scrolledCard(card, "")))
	v.pages.Add("empty", widgets.EmptyState(font, px, "ld-mail-symbolic", i18n.T("dropdown-mail-empty"), ""))
	content := dropdownContent()
	content.Append(v.pages, true)
	v.Append(content, true)
	v.rebuild()
	v.follow()
	return v
}

func (v *mailView) accounts() []mail.AccountUnread {
	if v.ctx.Mail == nil {
		return nil
	}
	return v.ctx.Mail.State().Accounts
}

// rebuild is rebuild_list.
func (v *mailView) rebuild() {
	v.list.Clear()
	accounts := v.accounts()
	if len(accounts) == 0 {
		v.pages.Show("empty")
		return
	}
	for _, a := range accounts {
		v.list.Append(v.row(a))
	}
	v.pages.Show("accounts")
}

// row is account_row on lys-ui's Item: the provider icon as the
// media, the account's name, and the unread count as a badge, the
// secondary one at zero.
func (v *mailView) row(a mail.AccountUnread) *widget.Item {
	row := widget.NewItem(v.font, a.Name, "")
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("mail-dropdown-row")
	row.SetMedia(widget.NewThemeIcon(a.Icon, itemIconPx), false)
	count := widget.NewBadge(v.font, strconv.FormatUint(uint64(a.Count), 10))
	count.AddClass("mail-account-count")
	if a.Count == 0 {
		count.SetVariant(widget.BadgeSecondary)
	}
	row.AddAction(count)
	return row
}

func (v *mailView) follow() {
	if v.ctx.App == nil || v.ctx.Mail == nil {
		return
	}
	ticks, stop := v.ctx.Mail.Subscribe()
	go func() {
		defer stop()
		for {
			select {
			case <-v.stop:
				return
			case _, ok := <-ticks:
				if !ok {
					return
				}
				v.ctx.Invoke(v.rebuild)
			}
		}
	}()
}

// dropdownClosed implements dropdownCloser.
func (v *mailView) dropdownClosed() { v.once.Do(func() { close(v.stop) }) }
