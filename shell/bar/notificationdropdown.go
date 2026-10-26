package bar

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/shell/notifyui"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// Notification dropdown constants (notification_group/methods.rs,
// notification_item/methods.rs, watchers.rs).
const (
	notifMaxVisibleItems = 3
	notifIconPx          = 32
	notifTimeTick        = 30 * time.Second
)

// notifTimeLabel is time_to_string.
func notifTimeLabel(age notifyui.Age) string { return notifyui.TimeLabel(age, "notification-dropdown") }

// notificationView is the notification dropdown: the header (bell or
// muted bell, clear all), the do-not-disturb row, and the app groups or
// the empty state, following the service while open.
type notificationView struct {
	ctx  ModuleContext
	svc  *notifications.Service
	font render.Font
	px   float64
	now  func() time.Time
	// minter mints the activation tokens action presses carry.
	minter notifyui.TokenMinter
	popoverHook

	*widget.Box
	headerIcon, emptyIcon *widget.Icon
	clearAll              *widget.Button
	dnd                   *widget.Switch
	syncing               bool
	// body is the content: the empty state and the list swap by
	// visibility, no Stack (the Rust tree sets visible in place).
	body   *widget.Box
	empty  *widget.StatusPage
	scroll *widget.Scroll
	list   *widget.Box
	groups []*notifGroup

	once sync.Once
	stop chan struct{}
}

func notificationDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &notificationView{ctx: ctx, svc: ctx.Notifications, font: font, px: px, now: time.Now, minter: notifyui.Minter(ctx.App), stop: make(chan struct{})}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "notification-dropdown")

	clearLabel := widget.NewLabel(font, px, i18n.T("notification-dropdown-clear-all"), 0)
	v.clearAll = dropdownButton(clearLabel, v.dismissAll, "notification-dropdown-clear-all", css.Ghost, css.Small)
	header, headerIcon := dropdownHeaderIcon(font, "ld-bell-symbolic", i18n.T("notification-dropdown-title"), v.clearAll)
	v.headerIcon = headerIcon
	v.Append(header, false)

	dndCard, dndGroup := itemCard()
	dndCard.AddClass("notification-dropdown-dnd-row")
	dnd := widget.NewItem(font, i18n.T("notification-dropdown-dnd-label"), "")
	dnd.SetSmall(true)
	v.dnd = widget.NewSwitch(false)
	v.dnd.OnChanged = v.dndToggled
	dnd.AddAction(v.dnd)
	dndGroup.Append(dnd)

	empty, emptyIcon := widgets.EmptyStateIcon(font, px, "ld-bell-symbolic", i18n.T("notification-dropdown-empty-title"), i18n.T("notification-dropdown-empty-description"))
	v.emptyIcon = emptyIcon
	v.empty = empty
	v.list = widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	v.list.AddClass("notification-dropdown-groups")
	scroll := dropdownScroll(v.list, "notification-dropdown-scroll")
	v.scroll = scroll
	v.body = dropdownContent("notification-dropdown-content")
	v.body.Append(dndCard, false)
	v.body.Append(empty, true)
	v.body.Append(scroll, true)
	v.Append(v.body, true)

	v.syncDND()
	v.rebuild()
	v.follow()
	return v
}

// syncDND is DndChanged: the switch (without writing back) and the
// bell icons.
func (v *notificationView) syncDND() {
	on := v.svc != nil && v.svc.DND()
	v.syncing = true
	v.dnd.SetOn(on)
	v.syncing = false
	icon := "ld-bell-symbolic"
	if on {
		icon = "ld-bell-off-symbolic"
	}
	v.headerIcon.SetThemeName(icon)
	v.emptyIcon.SetThemeName(icon)
}

func (v *notificationView) dndToggled(on bool) {
	if v.syncing || v.svc == nil {
		return
	}
	v.svc.SetDND(on)
}

func (v *notificationView) dismissAll() {
	if v.svc != nil {
		v.svc.DismissAll()
	}
}

// dismiss is Notification::dismiss.
func (v *notificationView) dismiss(id uint32) {
	v.svc.Close(id, notifications.Dismissed)
}

// rebuild is rebuild_groups: stale groups go, existing ones take their
// new notifications (keeping their expansion), new ones are added, and
// the list takes the newest-first order.
func (v *notificationView) rebuild() {
	var notifs []*notifications.Notification
	if v.svc != nil {
		notifs = v.svc.Notifications()
	}
	v.clearAll.SetVisible(len(notifs) > 0)
	v.empty.SetVisible(len(notifs) == 0)
	v.scroll.SetVisible(len(notifs) > 0)
	byApp := make(map[string]*notifGroup, len(v.groups))
	for _, g := range v.groups {
		byApp[g.app] = g
	}
	next := make([]*notifGroup, 0, len(v.groups))
	for _, data := range notifyui.GroupBy(notifs, notifyui.AppKey) {
		g, ok := byApp[data.Key]
		if ok {
			g.update(data.Notifs)
		} else {
			g = newNotifGroup(v, data)
		}
		next = append(next, g)
	}
	v.groups = next
	v.list.Clear()
	for _, g := range next {
		v.list.Append(g, false)
	}
}

// refreshTimes is TimeTick.
func (v *notificationView) refreshTimes() {
	for _, g := range v.groups {
		for _, it := range g.items {
			it.refreshTime()
		}
	}
}

// follow rebuilds on history changes, syncs DND, and refreshes the
// relative times every 30s, until the dropdown closes.
func (v *notificationView) follow() {
	if v.svc == nil {
		return
	}
	events, stop := v.svc.Subscribe()
	go func() {
		defer stop()
		tick := time.NewTicker(notifTimeTick)
		defer tick.Stop()
		for {
			select {
			case <-v.stop:
				return
			case <-tick.C:
				v.ctx.Invoke(v.refreshTimes)
			case ev, ok := <-events:
				if !ok {
					return
				}
				switch ev.Kind {
				case notifications.EventDnd:
					v.ctx.Invoke(v.syncDND)
				case notifications.EventAdd, notifications.EventRemove:
					v.ctx.Invoke(v.rebuild)
				}
			}
		}
	}()
}

// dropdownClosed implements dropdownCloser.
func (v *notificationView) dropdownClosed() { v.once.Do(func() { close(v.stop) }) }

// notifGroup is NotificationGroup: lys-ui's card whose header Item
// (the app's icon and name, the newest summary while collapsed, the
// count, clear, and the chevron) toggles the group, over the visible
// items and the "N more" link.
type notifGroup struct {
	*widget.Box
	view     *notificationView
	app      string
	expanded bool
	notifs   []*notifications.Notification
	items    []*notifItem
	overflow int

	head      *widget.Item
	icon      *widget.Icon
	count     *widget.Badge
	chevron   *widget.Icon
	itemsBox  *widget.Box
	list      *widget.ItemGroup
	moreRow   *widget.Box
	moreLabel *widget.Label
}

func newNotifGroup(v *notificationView, data notifyui.Group) *notifGroup {
	g := &notifGroup{view: v, app: data.Key, expanded: true, Box: widget.NewBox(widget.Column, 0, 0)}
	g.AddClass(css.Card, "notification-dropdown-group")

	appName := data.Key
	if appName == "" {
		appName = i18n.T("notification-dropdown-unknown-app")
	}
	// The whole header is the click target; its actions (the clear
	// button) keep their own clicks.
	g.head = widget.NewItem(v.font, appName, "")
	g.head.SetSmall(true)
	g.head.SetEllipsize(true)
	g.head.AddClass("notification-dropdown-group-header")
	g.icon = widget.NewThemeIcon(notifyui.FallbackIcon, itemIconPx)
	g.head.SetMedia(g.icon, false)
	g.head.SetOnActivate(g.toggle)
	g.count = widget.NewBadge(v.font, "")
	g.count.SetVariant(widget.BadgeSecondary)
	g.count.AddClass("notification-dropdown-group-count")
	g.head.AddAction(g.count)
	clearLabel := widget.NewLabel(v.font, v.px, i18n.T("notification-dropdown-group-clear"), 0)
	g.head.AddAction(dropdownButton(clearLabel, g.clear, "notification-dropdown-group-clear", css.Ghost, css.Small))
	g.chevron = widget.NewThemeIcon("ld-chevron-up-symbolic", itemIconPx)
	g.head.AddAction(g.chevron)
	g.Append(g.head, false)

	g.itemsBox = widget.NewBox(widget.Column, 0, 0)
	g.itemsBox.AddClass("notification-dropdown-group-items")
	g.list = widget.NewItemGroup()
	g.itemsBox.Append(g.list, false)
	g.moreLabel = widget.NewLabel(v.font, v.px, "", 0)
	g.moreRow = itemBody()
	g.moreRow.Append(dropdownButton(g.moreLabel, g.showAll, "notification-dropdown-group-more", css.Link, css.Small), false)
	g.itemsBox.Append(g.moreRow, false)
	g.Append(g.itemsBox, false)

	g.update(data.Notifs)
	return g
}

// update is UpdateNotifications: the group icon (always the mapped one
// of the newest) and reconcile_items.
func (g *notifGroup) update(notifs []*notifications.Notification) {
	g.icon.SetThemeName(notifyui.ResolveIcon(config.IconSourceMapped, notifs[0]).Name)
	g.notifs = notifs
	g.count.SetText(strconv.Itoa(len(notifs)))
	g.count.SetVisible(len(notifs) > 1)
	limit := notifMaxVisibleItems
	if len(g.items) > notifMaxVisibleItems {
		limit = len(notifs)
	}
	visible := notifs[:min(len(notifs), limit)]
	g.overflow = len(notifs) - len(visible)
	same := slices.EqualFunc(g.items, visible, func(it *notifItem, n *notifications.Notification) bool {
		return it.n.ID == n.ID
	})
	if !same {
		g.items = g.items[:0]
		g.list.Clear()
		g.appendItems(visible)
	}
	g.sync()
}

func (g *notifGroup) appendItems(notifs []*notifications.Notification) {
	for _, n := range notifs {
		it := newNotifItem(g.view, n)
		g.items = append(g.items, it)
		g.list.Append(it)
	}
}

// sync applies the #[watch]es on expanded and overflow.
func (g *notifGroup) sync() {
	g.itemsBox.SetVisible(g.expanded)
	if g.expanded {
		g.head.SetDescription("")
	} else {
		g.head.SetDescription(g.notifs[0].Summary)
	}
	if g.expanded {
		g.chevron.SetThemeName("ld-chevron-up-symbolic")
	} else {
		g.chevron.SetThemeName("ld-chevron-down-symbolic")
	}
	g.moreLabel.SetText(i18n.T("notification-dropdown-group-more", i18n.Str("count", strconv.Itoa(g.overflow))))
	g.moreRow.SetVisible(g.overflow > 0)
}

// toggle is ToggleExpanded: collapsing drops back to the default cap
// (reset_to_default_cap).
func (g *notifGroup) toggle() {
	g.expanded = !g.expanded
	if !g.expanded && len(g.items) > notifMaxVisibleItems {
		g.items = g.items[:notifMaxVisibleItems]
		g.list.Clear()
		for _, it := range g.items {
			g.list.Append(it)
		}
		g.overflow = len(g.notifs) - notifMaxVisibleItems
	}
	g.sync()
}

// showAll is show_all_items.
func (g *notifGroup) showAll() {
	g.appendItems(g.notifs[len(g.items):])
	g.overflow = 0
	g.sync()
}

// clear is ClearGroup.
func (g *notifGroup) clear() {
	for _, n := range g.notifs {
		g.view.dismiss(n.ID)
	}
}

// notifItem is NotificationItem: the notification as lys-ui's Item
// (its icon, the summary, the relative time and the dismiss button)
// over the two-line body and the non-default actions in rows of three.
// A default action makes the item a control that closes the dropdown
// and invokes it.
type notifItem struct {
	*widget.Box
	view *notificationView
	n    *notifications.Notification
	main *widget.Item
	time *widget.Label
}

// itemIcon builds the item's icon: an unloadable file picture falls
// back to the themed bell (apply_icon's load failure) and reports
// whether the glyph still draws a picture, so it sits bare, without
// the media tile, only when the file actually shows.
func itemIcon(icon notifyui.Icon, size int) (*widget.Icon, bool) {
	if icon.File != "" {
		if _, err := os.Stat(icon.File); err != nil {
			icon = notifyui.Icon{Name: notifyui.FallbackIcon}
		}
	}
	return notifyui.NewIcon(icon, size, 0), notifyui.IsFileIcon(icon)
}

func newNotifItem(v *notificationView, n *notifications.Notification) *notifItem {
	it := &notifItem{view: v, n: n, Box: widget.NewBox(widget.Column, 0, 0)}
	it.AddClass("notification-dropdown-item", notifyui.UrgencyClass(n.Urgency))

	it.main = widget.NewItem(v.font, n.Summary, "")
	it.main.SetSmall(true)
	it.main.SetEllipsize(true)
	it.main.AddClass("notification-dropdown-item-main")
	icon := notifyui.ResolveIcon(v.ctx.Config.Notification.IconSource, n)
	size := itemIconPx
	if notifyui.IsFileIcon(icon) {
		size = notifIconPx
	}
	glyph, isFile := itemIcon(icon, size)
	glyph.AddClass("notification-dropdown-item-icon")
	if isFile {
		glyph.AddClass("file-icon")
	}
	it.main.SetMedia(glyph, isFile)
	it.time = widget.NewLabel(v.font, v.px, "", 0)
	it.time.AddClass("notification-dropdown-item-time", css.Caption, css.DimLabel)
	it.main.AddAction(it.time)
	id := n.ID
	dismiss := dropdownButton(widget.NewThemeIcon("ld-x-symbolic", itemIconPx), func() { v.dismiss(id) }, "notification-dropdown-item-dismiss", css.Ghost, css.Icon, css.Small)
	it.main.AddAction(dismiss)
	// A default action makes the item open on a click anywhere the
	// dismiss button does not take (setup_default_action's gesture).
	if _, ok := n.DefaultAction(); ok {
		it.main.AddClass("notification-dropdown-item-default")
		it.main.SetCursorName("pointer")
		it.main.SetOnActivate(it.invokeDefault)
	}
	it.Append(it.main, false)

	body := itemBody()
	if n.Body != "" {
		text := widget.NewRichLabel(v.font, v.px, notifyui.BodyMarkup(n.Body), 0)
		text.SetVariants(app.FontVariantsOf(v.font))
		text.AddClass("notification-dropdown-item-body", css.DimLabel)
		text.SetWrap(true)
		text.SetEllipsize(widget.EllipsizeEnd)
		text.SetMaxLines(2)
		body.Append(text, false)
	}
	if actions := it.actionRows(); actions != nil {
		body.Append(actions, false)
	}
	if len(body.Children()) > 0 {
		it.Append(body, false)
	}
	it.refreshTime()
	return it
}

// actionRows is build_action_buttons; nil without visible actions.
func (it *notifItem) actionRows() widget.Widget {
	visible := notifyui.VisibleActions(it.n)
	if len(visible) == 0 {
		return nil
	}
	v := it.view
	gap := widget.Current().Spacing / 2
	box := widget.NewBox(widget.Column, gap, 0)
	box.AddClass("notification-dropdown-item-actions")
	for chunk := range slices.Chunk(visible, notifyui.ActionsPerRow) {
		// The homogeneous row: a column-homogeneous grid gives every
		// button the widest button's width, gtk Box homogeneous's
		// equal shares.
		row := widget.NewGrid(gap, 0)
		row.SetColumnHomogeneous(true)
		row.AddClass("notification-dropdown-item-action-row")
		for i, a := range chunk {
			label := widget.NewLabel(v.font, v.px, a.Label, 0)
			label.SetAlignment(render.AlignCenter)
			key := a.ID
			btn := dropdownButton(label, func() { it.invoke(key) }, "notification-dropdown-item-action-btn", css.Outline, css.Small)
			row.Attach(btn, i, 0, 1, 1)
		}
		box.Append(row, false)
	}
	return box
}

// invoke runs an action with a token from the press and dismisses the
// notification, as the Rust button does (invoke, then dismiss,
// resident or not).
func (it *notifItem) invoke(key string) {
	svc, id := it.view.svc, it.n.ID
	notifyui.WithActivationToken(it.view.minter, func(token string) {
		svc.InvokeAction(id, key, token)
		svc.Close(id, notifications.Dismissed)
	})
}

// invokeDefault closes the dropdown first: the default action usually
// raises another window (setup_default_action).
func (it *notifItem) invokeDefault() {
	it.view.popdown()
	it.invoke(notifications.DefaultActionID)
}

// refreshTime is RefreshTime.
func (it *notifItem) refreshTime() {
	it.time.SetText(notifTimeLabel(notifyui.RelativeTime(it.view.now(), it.n.Added)))
}
