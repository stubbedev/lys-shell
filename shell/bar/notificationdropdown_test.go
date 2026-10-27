package bar

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/shell/notifyui"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestNotifTimeLabel(t *testing.T) {
	if got := notifTimeLabel(notifyui.Age{}); got != i18n.T("notification-dropdown-time-just-now") {
		t.Errorf("just now = %q", got)
	}
	if got := notifTimeLabel(notifyui.Age{Minutes: 5}); got != i18n.T("notification-dropdown-time-minutes-ago", i18n.Str("minutes", "5")) {
		t.Errorf("minutes = %q", got)
	}
	if got := notifTimeLabel(notifyui.Age{Hours: 3}); got != i18n.T("notification-dropdown-time-hours-ago", i18n.Str("hours", "3")) {
		t.Errorf("hours = %q", got)
	}
}

func newNotifTestService(t *testing.T) *notifications.Service {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	svc := notifications.NewService()
	svc.SetDND(false)
	return svc
}

func TestNotificationDropdownEmptyAndDND(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := notificationDropdown(ctx).(*notificationView)
	if !v.empty.Visible() || v.scroll.Visible() || v.clearAll.Visible() {
		t.Errorf("no service: empty shown %v, list shown %v, clear all shown %v", v.empty.Visible(), v.scroll.Visible(), v.clearAll.Visible())
	}

	svc := newNotifTestService(t)
	ctx.Notifications = svc
	v = notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	if !v.empty.Visible() || v.scroll.Visible() || v.headerIcon.Name() != "ld-bell-symbolic" {
		t.Errorf("empty service: empty shown %v, list shown %v, icon %q", v.empty.Visible(), v.scroll.Visible(), v.headerIcon.Name())
	}
	// The switch writes DND; the service's change comes back as the
	// muted bells without writing again.
	v.dnd.SetOn(true)
	if !svc.DND() {
		t.Fatal("the switch did not enable DND")
	}
	waitHeadless(t, "the muted bells", func() bool {
		return v.headerIcon.Name() == "ld-bell-off-symbolic" && v.emptyIcon.Name() == "ld-bell-off-symbolic"
	})
	// A service-side change moves the switch.
	svc.SetDND(false)
	waitHeadless(t, "the switch off", func() bool { return !v.dnd.On() })
	if svc.DND() {
		t.Error("syncing the switch wrote DND back")
	}
}

func TestNotificationDropdownGroups(t *testing.T) {
	svc := newNotifTestService(t)
	for range 5 {
		svc.Notify("mail", 0, "", "Mail", "<b>body</b> text", nil, 0)
	}
	chat := svc.Notify("chat", 0, "", "Ping", "", []string{"reply", "Reply", "default", "Open"}, 0)
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()

	if !v.scroll.Visible() || v.empty.Visible() || !v.clearAll.Visible() || len(v.groups) != 2 {
		t.Fatalf("list shown %v, empty shown %v, %d groups", v.scroll.Visible(), v.empty.Visible(), len(v.groups))
	}
	chatGroup, mail := v.groups[0], v.groups[1]
	if chatGroup.app != "chat" || mail.app != "mail" {
		t.Fatalf("groups = %q, %q; want the newest app first", chatGroup.app, mail.app)
	}
	if chatGroup.count.Visible() || !mail.count.Visible() || mail.count.Text() != "5" {
		t.Errorf("counts: chat shown %v, mail %q", chatGroup.count.Visible(), mail.count.Text())
	}
	if len(mail.items) != notifMaxVisibleItems || mail.overflow != 2 || !mail.moreRow.Visible() {
		t.Errorf("mail: %d items, overflow %d", len(mail.items), mail.overflow)
	}
	if mail.moreLabel.Text() != i18n.T("notification-dropdown-group-more", i18n.Str("count", "2")) {
		t.Errorf("more = %q", mail.moreLabel.Text())
	}

	// Show all, then collapse back to the cap.
	mail.showAll()
	if len(mail.items) != 5 || mail.moreRow.Visible() {
		t.Errorf("show all: %d items, more shown %v", len(mail.items), mail.moreRow.Visible())
	}
	mail.toggle()
	if mail.expanded || mail.itemsBox.Visible() || itemText(mail.head, "description") != "Mail" || len(mail.items) != notifMaxVisibleItems || mail.overflow != 2 {
		t.Errorf("collapse: expanded %v, %d items, overflow %d", mail.expanded, len(mail.items), mail.overflow)
	}
	mail.toggle()

	// A new mail updates the group in place, keeping its widget, and
	// moves it to the top.
	svc.Notify("mail", 0, "", "Newest", "", nil, 0)
	waitHeadless(t, "the regroup", func() bool { return len(v.groups) == 2 && v.groups[0] == mail })
	if mail.items[0].n.Summary != "Newest" || mail.count.Text() != "6" {
		t.Errorf("updated mail group: first %q count %q", mail.items[0].n.Summary, mail.count.Text())
	}

	// The chat item: a default action row and one visible action.
	item := chatGroup.items[0]
	main := item.main
	if !main.HasClass("notification-dropdown-item-default") {
		t.Error("the default action did not mark the row")
	}
	actions := findByClass(item, "notification-dropdown-item-actions").(*widget.Box)
	if rows := actions.Children(); len(rows) != 1 || len(rows[0].(*widget.Grid).Children()) != 1 {
		t.Errorf("action rows = %v, want one row with Reply", rows)
	}
	pop := &fakePopover{}
	v.attachPopover(pop)
	widget.SizeWithin(item, widget.Size{W: 360, H: 400})
	item.Arrange(render.Rect{W: 360, H: 400})
	routeClick(item, treetest.WithClass(main, "title")[0])
	if pop.dismissed != 1 {
		t.Error("the default action did not close the dropdown")
	}
	waitHeadless(t, "the chat group gone", func() bool { return len(v.groups) == 1 })
	for _, n := range history(t, svc) {
		if n.ID == chat {
			t.Error("the invoked notification stayed")
		}
	}

	// Clearing the group empties the dropdown.
	mail.clear()
	waitHeadless(t, "the empty state", func() bool { return v.empty.Visible() && !v.scroll.Visible() })
	if v.clearAll.Visible() {
		t.Error("clear all stays without notifications")
	}
}

func TestNotificationItemBodyIconAndUrgency(t *testing.T) {
	svc := newNotifTestService(t)
	svc.NotifyHints("firefox", 0, "", "Download", "a &amp; <b>b</b>", nil,
		map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(2))}, 0)
	svc.Notify("plain", 0, "", "No body", "", nil, 0)
	cfg := config.Defaults()
	cfg.Notification.IconSource = config.IconSourceMapped
	ctx := newTestContext(t, cfg)
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()

	var ff, plain *notifItem
	for _, g := range v.groups {
		switch g.app {
		case "firefox":
			ff = g.items[0]
		case "plain":
			plain = g.items[0]
		}
	}
	if !ff.HasClass("critical") || plain.HasClass("critical") || !plain.HasClass("normal") {
		t.Error("urgency classes")
	}
	body := findByClass(ff, "notification-dropdown-item-body").(*widget.RichLabel)
	if body.Text() != "a & b" || body.MaxLines() != 2 || !body.Wrap() {
		t.Errorf("body = %q (max lines %d, wrap %v)", body.Text(), body.MaxLines(), body.Wrap())
	}
	if findByClass(plain, "notification-dropdown-item-body") != nil || len(plain.Children()) != 1 {
		t.Error("an empty body still shows a label")
	}
	icon := findByClass(ff, "notification-dropdown-item-icon").(*widget.Icon)
	if icon.Name() != "si-firefox-symbolic" || icon.HasClass("file-icon") || len(treetest.WithClass(ff.main, "media")) != 1 {
		t.Errorf("mapped icon = %q, want the themed glyph on the media tile", icon.Name())
	}
	if ff.time.Text() != i18n.T("notification-dropdown-time-just-now") {
		t.Errorf("time = %q", ff.time.Text())
	}
	v.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	v.refreshTimes()
	if ff.time.Text() != i18n.T("notification-dropdown-time-hours-ago", i18n.Str("hours", "2")) {
		t.Errorf("refreshed time = %q", ff.time.Text())
	}
}

// TestNotificationGroupHeaderClickTargets pins the header's click
// split: the whole header item toggles the group, and the clear button
// inside it dismisses without toggling.
func TestNotificationGroupHeaderClickTargets(t *testing.T) {
	svc := newNotifTestService(t)
	for range 5 {
		svc.Notify("mail", 0, "", "Mail", "", nil, 0)
	}
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	g := v.groups[0]
	widget.SizeWithin(v, widget.Size{W: 425, H: 725})
	v.Arrange(render.Rect{W: 425, H: 725})

	header := g.head
	routeClick(v, header)
	if g.expanded {
		t.Error("a header click did not collapse the group")
	}
	routeClick(v, header)
	if !g.expanded {
		t.Error("a second header click did not re-expand the group")
	}

	// The clear button's own click dismisses the group and never
	// bubbles to the header's toggle.
	routeClick(v, findByClass(g, "notification-dropdown-group-clear"))
	waitHeadless(t, "the cleared group", func() bool { return len(v.groups) == 0 })
	if !g.expanded {
		t.Error("the clear button's click toggled the group")
	}
}

// The more row is a link button: a click shows the hidden items and
// hides the row.
func TestNotificationMoreRowShowsAll(t *testing.T) {
	svc := newNotifTestService(t)
	for range 5 {
		svc.Notify("mail", 0, "", "Mail", "", nil, 0)
	}
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	g := v.groups[0]
	widget.SizeWithin(v, widget.Size{W: 425, H: 725})
	v.Arrange(render.Rect{W: 425, H: 725})

	more, ok := findByClass(g, "notification-dropdown-group-more").(*widget.Button)
	if !ok || !more.HasClass(css.Link) || !g.moreRow.Visible() {
		t.Fatal("the overflow row is not a shown link button")
	}
	routeClick(v, more)
	if len(g.items) != 5 || g.overflow != 0 || g.moreRow.Visible() {
		t.Errorf("the more click did not show all: %d items, overflow %d", len(g.items), g.overflow)
	}
}

// TestNotificationItemCursor pins the pointer on a default-action
// item, and no pointer without one; the actions sit in a homogeneous
// row.
func TestNotificationItemCursor(t *testing.T) {
	svc := newNotifTestService(t)
	svc.Notify("chat", 0, "", "Ping", "", []string{"default", "Open", "reply", "Reply"}, 0)
	svc.Notify("plain", 0, "", "Quiet", "", nil, 0)
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()

	var chat, plain *notifItem
	for _, g := range v.groups {
		switch g.app {
		case "chat":
			chat = g.items[0]
		case "plain":
			plain = g.items[0]
		}
	}
	if widget.CursorNameOf(chat.main) != "pointer" {
		t.Error("the default-action item does not ask for the pointer")
	}
	if widget.CursorNameOf(plain.main) != "" {
		t.Error("an item without a default action asks for the pointer")
	}
	row := treetest.First[*widget.Grid](t, chat)
	if !row.ColumnHomogeneous() || len(row.Children()) != 1 {
		t.Errorf("the action row = homogeneous %v, %d buttons; want Reply alone", row.ColumnHomogeneous(), len(row.Children()))
	}
}

// TestNotificationItemFileIconFallsBack pins apply_icon's load
// failure: an unloadable file picture shows the themed bell and its
// tile never takes the file-icon class; a loadable one keeps the file
// icon and the class.
func TestNotificationItemFileIconFallsBack(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone.png")
	kept := filepath.Join(t.TempDir(), "kept.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatalf("png: %v", err)
	}
	if err := os.WriteFile(kept, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc := newNotifTestService(t)
	svc.NotifyHints("a", 0, "", "Gone", "", nil, map[string]dbus.Variant{"image-path": dbus.MakeVariant(gone)}, 0)
	svc.NotifyHints("b", 0, "", "Kept", "", nil, map[string]dbus.Variant{"image-path": dbus.MakeVariant(kept)}, 0)
	cfg := config.Defaults()
	cfg.Notification.IconSource = config.IconSourceAutomatic
	ctx := newTestContext(t, cfg)
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()

	var missing, loaded *notifItem
	for _, g := range v.groups {
		switch g.app {
		case "a":
			missing = g.items[0]
		case "b":
			loaded = g.items[0]
		}
	}
	missIcon := findByClass(missing, "notification-dropdown-item-icon").(*widget.Icon)
	if missIcon.Name() != notifyui.FallbackIcon || missIcon.HasClass("file-icon") {
		t.Errorf("the unloadable file icon = %q (file-icon %v), want the bell", missIcon.Name(), missIcon.HasClass("file-icon"))
	}
	keepIcon := findByClass(loaded, "notification-dropdown-item-icon").(*widget.Icon)
	if keepIcon.Name() != "" || !keepIcon.HasClass("file-icon") {
		t.Errorf("the loadable file lost its icon (name %q, file-icon %v)", keepIcon.Name(), keepIcon.HasClass("file-icon"))
	}
}

// The sized panel forwards the popover handle to content that acts on
// its popover, and ignores content that does not.
func TestPanelBoxForwardsThePopover(t *testing.T) {
	inner := &notificationView{Box: widget.NewBox(widget.Column, 0, 0)}
	pop := &fakePopover{}
	newPanelBox(inner, 0, 0).attachPopover(pop)
	inner.popdown()
	entry := widget.NewEntry(testFont(t), 12, 0)
	inner.focus(entry)
	if pop.dismissed != 1 || pop.focused != entry {
		t.Errorf("forwarded: dismissed %d focused %v", pop.dismissed, pop.focused)
	}
	newPanelBox(widget.NewBox(widget.Column, 0, 0), 0, 0).attachPopover(pop) // ignored
	var unset popoverHook
	unset.popdown() // no popover yet: a no-op
	// A focus asked for before the popover exists lands on attach.
	unset.focus(entry)
	late := &fakePopover{}
	unset.attachPopover(late)
	if late.focused != entry {
		t.Error("the pending focus was lost")
	}
}

// TestNotificationItemDismissInsideADefaultActionRow pins the row's
// click split: the dismiss button inside a row with a default action
// dismisses, without invoking the action or closing the dropdown.
// Regression: the row was a button, a hit leaf, so its dismiss button
// could never be clicked.
func TestNotificationItemDismissInsideADefaultActionRow(t *testing.T) {
	svc := newNotifTestService(t)
	svc.Notify("chat", 0, "", "Ping", "", []string{"default", "Open"}, 0)
	var invoked []string
	svc.SetEmitter(func(member string, args ...any) {
		if strings.HasSuffix(member, ".ActionInvoked") {
			invoked = append(invoked, member)
		}
	})
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	pop := &fakePopover{}
	v.attachPopover(pop)
	item := v.groups[0].items[0]
	widget.SizeWithin(item, widget.Size{W: 360, H: 400})
	item.Arrange(render.Rect{W: 360, H: 400})
	routeClick(item, findByClass(item, "notification-dropdown-item-dismiss"))
	waitHeadless(t, "the dismissal", func() bool { return len(history(t, svc)) == 0 })
	if pop.dismissed != 0 || len(invoked) != 0 {
		t.Errorf("dismiss closed the dropdown (%d) or invoked %v", pop.dismissed, invoked)
	}
}

// barMinter answers every token request with token and err.
type barMinter struct {
	token string
	err   error
}

func (m barMinter) RequestActivationToken(done func(string, error)) { done(m.token, m.err) }

// An action pressed in the dropdown carries the token minted from the
// press, announced right before the ActionInvoked; without
// xdg-activation the action goes alone.
func TestNotificationItemActionCarriesItsToken(t *testing.T) {
	svc := newNotifTestService(t)
	var seen []string
	svc.SetEmitter(func(member string, args ...any) {
		if !strings.HasSuffix(member, ".NotificationClosed") {
			seen = append(seen, strings.TrimPrefix(member, notifications.Interface+".")+fmt.Sprint(args...))
		}
	})
	first := svc.Notify("chat", 0, "", "Ping", "", []string{"reply", "Reply"}, 0)
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	v.minter = barMinter{token: "tok-3"}
	v.groups[0].items[0].invoke("reply")
	waitHeadless(t, "the action", func() bool { return len(history(t, svc)) == 0 })
	if want := []string{"ActivationToken" + fmt.Sprint(first, "tok-3"), "ActionInvoked" + fmt.Sprint(first, "reply")}; !slices.Equal(seen, want) {
		t.Errorf("signals = %q, want %q", seen, want)
	}

	seen = nil
	second := svc.Notify("chat", 0, "", "Again", "", []string{"reply", "Reply"}, 0)
	waitHeadless(t, "the new item", func() bool {
		return len(v.groups) == 1 && len(v.groups[0].items) == 1 && v.groups[0].items[0].n.ID == second
	})
	v.minter = barMinter{err: app.ErrActivationUnavailable}
	v.groups[0].items[0].invoke("reply")
	if want := []string{"ActionInvoked" + fmt.Sprint(second, "reply")}; !slices.Equal(seen, want) {
		t.Errorf("without xdg-activation: %q, want %q", seen, want)
	}
}

// A body with no spaces to wrap at (a file path) wraps and ellipsizes
// inside its item instead of running out past the card.
func TestNotificationLongBodyStaysInItsItem(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	defer restore()
	ctx := styledContext(t, config.Defaults())
	svc := newNotifTestService(t)
	ctx.Notifications = svc
	svc.Notify("shot", 0, "", "Screenshot saved", "/home/stubbe/Pictures/Screenshot_2026-10-25_16-04-33_with_a_long_unbroken_name.png", nil, 0)
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()
	waitHeadless(t, "the item", func() bool { return v.scroll.Visible() })
	card, rev := dropdownTree(v, ctx.Config)
	rev.SetRevealed(true)
	ctx.Theme.Attach(card)
	onHeadlessLoop(func() bool {
		widget.SizeWithinAsChild(card, widget.Size{W: 425, H: 400})
		widget.ArrangeAsChild(card, render.Rect{W: 425, H: 400})
		item := treetest.WithClass(card, "notification-dropdown-item")[0].(widget.Boundser).Bounds()
		label := treetest.WithClass(card, "notification-dropdown-item-body")[0]
		body := label.(widget.Boundser).Bounds()
		if body.X+body.W > item.X+item.W {
			t.Errorf("the body %+v runs past its item %+v", body, item)
		}
		const W, H = 425, 400
		data := make([]byte, render.Stride(W)*H)
		label.Paint(render.NewScaled(data, render.Stride(W), W, H, 1, 1))
		inked, outside := 0, 0
		for y := range H {
			for x := range W {
				if data[(y*W+x)*4+3] == 0 {
					continue
				}
				inked++
				if x >= body.X+body.W {
					outside++
				}
			}
		}
		if inked == 0 {
			t.Fatal("the body painted nothing")
		}
		if outside != 0 {
			t.Errorf("the body inked %d pixels past its right edge x=%d", outside, body.X+body.W)
		}
		return true
	})
}

// TestNotificationDropdownPagesTheHistory pins the paging: the dropdown
// reads a page, the next only when the list scrolls near its end, and
// a change rereads as deep as it had read.
func TestNotificationDropdownPagesTheHistory(t *testing.T) {
	svc := newNotifTestService(t)
	memory, err := notifications.NewMemoryLimit(notifications.MaxHistoryMemory)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetHistoryLimits(memory, notifications.DefaultStoredLimit())
	const sent = 2*notifPageSize + 20
	for i := range sent {
		svc.Notify("app"+strconv.Itoa(i%4), 0, "", "n", "", nil, 0)
	}
	ctx := newTestContext(t, config.Defaults())
	ctx.Notifications = svc
	v := notificationDropdown(ctx).(*notificationView)
	defer v.dropdownClosed()

	read := func() int {
		n := 0
		for _, g := range v.groups {
			n += len(g.notifs)
		}
		return n
	}
	if len(v.notifs) != notifPageSize || read() != notifPageSize || !v.more {
		t.Fatalf("first read %d (grouped %d, more %v), want one page", len(v.notifs), read(), v.more)
	}
	if v.notifs[0].ID != sent {
		t.Errorf("newest read = %d, want %d", v.notifs[0].ID, sent)
	}
	// The groups collapse to a few items each: nothing overflows, so
	// nothing scrolls, and no page follows.
	v.scrolled()
	if len(v.notifs) != notifPageSize {
		t.Errorf("a list that does not scroll read %d", len(v.notifs))
	}
	v.loadMore()
	v.loadMore()
	if len(v.notifs) != sent || v.more || read() != sent {
		t.Fatalf("after two more pages read %d (grouped %d, more %v), want all %d", len(v.notifs), read(), v.more, sent)
	}
	for i, n := range v.notifs {
		if n.ID != uint32(sent-i) {
			t.Fatalf("read[%d] = %d, want %d", i, n.ID, sent-i)
		}
	}
	v.loadMore()
	if len(v.notifs) != sent {
		t.Errorf("past the end read %d", len(v.notifs))
	}
	svc.Notify("app0", 0, "", "new", "", nil, 0)
	waitHeadless(t, "the same depth reread from the newest", func() bool {
		return len(v.notifs) == sent && v.notifs[0].ID == sent+1 && v.more
	})
}
