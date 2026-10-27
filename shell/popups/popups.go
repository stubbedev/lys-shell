// Package popups renders the notification popup window: stacks of
// cards on one output, following the notification service's feed,
// auto-dismissing after the configured duration, hidden while DND.
// A stack shows its newest card in front of the older ones and fans
// out into a list while the pointer is over it.
package popups

import (
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/shell/layering"
	"github.com/stubbedev/lys-shell/shell/notifyui"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// Card geometry (templates.rs, card/methods.rs).
const (
	cardMaxWidthChars = 40
	// cardBodyLines clamps a resting card's body; an expanded card
	// shows all of it.
	cardBodyLines = 2
	// cardWidthPx is the card's width at scale 1, cardIconPx a file
	// picture's size and cardGlyphPx a themed glyph's on its tile.
	cardWidthPx = 340
	cardIconPx  = 32
	cardGlyphPx = 16
	// popupGapBasePx and popupMarginBasePx are the bases the popup-gap
	// and popup-margin-x/y multipliers scale (POPUP_GAP_BASE_REM 0.5,
	// POPUP_MARGIN_BASE_REM 0.75, at 16px a rem).
	popupGapBasePx    = 8
	popupMarginBasePx = 12
)

// Popups owns the popup surface for one output. It belongs to the
// goroutine that owns its widget tree, the application's loop (the one
// calling it, headless), and every method runs there.
type Popups struct {
	app   *app.Application
	svc   *notifications.Service
	cfg   *config.Config
	font  render.Font
	sheet *widget.Stylesheet
	// outputs lists the connected outputs, the primary first; the
	// surface maps on popup-monitor among them.
	outputs func() []*app.Output
	now     func() time.Time
	// minter mints the activation tokens action presses carry.
	minter notifyui.TokenMinter
	win    *app.LayerWindow
	// winOut is the output win is on.
	winOut *app.Output
	root   *widget.Box
	// cards are the live popups by id; stacks are every stack on
	// screen top to bottom, those whose cards are all still playing
	// their exit included.
	cards  map[uint32]*card
	stacks []*stack
}

// stack is the cards one popup-stack key groups: the newest in front,
// the older ones behind it, fanned out into a list while hovered.
type stack struct {
	key string
	// cards are newest first, those still playing their exit included.
	cards []*card
	// hovered is whether the pointer is over the stack, which expands it.
	hovered bool
	root    *widget.Box
	pile    *widget.Pile
	// clear dismisses the whole stack once expanded.
	clear *widget.Button
}

// card is one popup (NotificationPopupCard) on lys-ui's card: the top
// row as an Item (the app icon, the app name, the count of the cards
// behind it, the age and the close button), the summary, the body, and
// the action rows.
type card struct {
	id uint32
	// leaving marks a card playing its exit.
	leaving bool
	// swipe takes the card away on a sideways swipe; rev inside it
	// plays the card's enter and exit (the LysShellRevealer around it).
	swipe *widget.SwipeDismiss
	rev   *widget.Revealer
	root  *widget.Box
	head  *widget.Item
	// count shows, on a collapsed stack's front card, how many cards
	// sit behind it.
	count   *widget.Badge
	time    *widget.Label
	summary *widget.Label
	body    *widget.RichLabel
	close   *widget.Button
	actions *widget.Box
}

// New builds the popup host; it maps on popup-monitor among outputs
// when the first popup arrives. The service's popup countdowns take the
// configured popup-duration; sheet is the shell's stylesheet, which
// styles the cards (nil leaves them unstyled).
func New(application *app.Application, svc *notifications.Service, cfg *config.Config, font render.Font, sheet *widget.Stylesheet, outputs func() []*app.Output) *Popups {
	svc.SetPopupDuration(time.Duration(cfg.Notification.PopupDuration) * time.Millisecond)
	return &Popups{
		app:     application,
		svc:     svc,
		cfg:     cfg,
		font:    font,
		sheet:   sheet,
		outputs: outputs,
		now:     time.Now,
		minter:  notifyui.Minter(application),
		cards:   make(map[uint32]*card),
	}
}

// DetachOutput closes the surface when its output went away; the
// stacks remap on the configured monitor (the primary fallback) at
// once, collapsed, as the pointer left with the surface.
func (p *Popups) DetachOutput(name string) {
	if p.win == nil || p.winOut == nil || p.winOut.Name != name {
		return
	}
	win := p.win
	p.win, p.winOut, p.root = nil, nil, nil
	p.unhover()
	p.layout()
	win.Close()
}

// unhover collapses every hovered stack, as when the pointer leaves it,
// resuming the countdowns the hover paused.
func (p *Popups) unhover() {
	for _, st := range p.stacks {
		if st.hovered {
			p.setHovered(st, false)
		}
	}
}

// SetConfig applies a reloaded config on the loop goroutine: the
// surface closes and the stacks rebuild at the new position, layer,
// gap, limits, grouping, and card behavior. The countdowns a hovered
// stack paused resume, as the pointer leaves with the surface.
func (p *Popups) SetConfig(cfg *config.Config) {
	p.svc.SetPopupDuration(time.Duration(cfg.Notification.PopupDuration) * time.Millisecond)
	p.unhover()
	p.cfg = cfg
	win := p.win
	p.win, p.winOut, p.root = nil, nil, nil
	p.cards = make(map[uint32]*card)
	p.stacks = nil
	if win != nil {
		win.Close()
	}
	p.sync()
}

// Run follows the service's feed until the process exits; each change
// reconciles on the loop goroutine, which owns the widget tree, or,
// headless, on Run's own.
func (p *Popups) Run() {
	events, _ := p.svc.Subscribe()
	for range events {
		if p.app == nil {
			p.sync()
			continue
		}
		p.app.Invoke(p.sync)
	}
}

// stackKey is the popup-stack grouping: by app, everything in one
// stack, or every popup on its own.
func stackKey(mode config.PopupStack) func(*notifications.Notification) string {
	switch mode {
	case config.PopupStackAll:
		return func(*notifications.Notification) string { return "" }
	case config.PopupStackOff:
		return func(n *notifications.Notification) string { return strconv.FormatUint(uint64(n.ID), 10) }
	}
	return notifyui.AppKey
}

// sync reconciles the stacks with the service's popup set (reconcile),
// the newest popup-max-visible grouped by popup-stack: a card no longer
// listed, or now grouped elsewhere, plays its exit and leaves its stack
// once it has; a new one enters at the front of its stack. The stacks
// order by their newest card, newest first or oldest first, and a stack
// whose cards are all leaving keeps its place until they are gone.
// Headless construction tracks the cards; only the window needs the
// application.
func (p *Popups) sync() {
	visible := p.svc.Popups()
	cfg := p.cfg.Notification
	if limit := int(cfg.PopupMaxVisible); len(visible) > limit {
		visible = visible[:limit]
	}
	groups := notifyui.GroupBy(visible, stackKey(cfg.PopupStack))
	live := make(map[uint32]string, len(visible))
	for _, g := range groups {
		for _, n := range g.Notifs {
			live[n.ID] = g.Key
		}
	}
	var leaving, entering []*card
	byKey := make(map[string]*stack, len(p.stacks))
	for _, st := range p.stacks {
		byKey[st.key] = st
		for _, c := range st.cards {
			if c.leaving {
				continue
			}
			if key, ok := live[c.id]; ok && key == st.key {
				continue
			}
			c.leaving = true
			delete(p.cards, c.id)
			leaving = append(leaving, c)
		}
	}
	order := make([]*stack, 0, len(groups)+len(p.stacks))
	for _, g := range groups {
		st, ok := byKey[g.Key]
		if !ok {
			st = p.newStack(g.Key)
		}
		for _, n := range slices.Backward(g.Notifs) {
			if _, ok := p.cards[n.ID]; ok {
				continue
			}
			c := p.newCard(n)
			p.cards[n.ID] = c
			st.cards = slices.Insert(st.cards, 0, c)
			entering = append(entering, c)
			if st.hovered && cfg.PopupHoverPause {
				p.svc.InhibitPopup(n.ID)
			}
		}
		order = append(order, st)
	}
	if cfg.PopupStackingOrder == config.StackingOrderOldestFirst {
		slices.Reverse(order)
	}
	for i, st := range p.stacks {
		if !slices.Contains(order, st) {
			order = slices.Insert(order, min(i, len(order)), st)
		}
	}
	p.stacks = order
	for _, st := range p.stacks {
		p.refreshStack(st)
	}
	p.layout()
	anims := p.cfg.Animations
	// An exit that lands at once (animations off) drops its card through
	// dropCard right away.
	for _, c := range leaving {
		reveal.Hide(c.rev, anims, config.AnimNotifications, func() { p.dropCard(c) })
	}
	for _, c := range entering {
		reveal.Show(c.rev, anims, config.AnimNotifications)
	}
}

// dropCard takes a card whose exit has played off its stack, the stack
// off the screen with its last card, and the window down with the last
// stack.
func (p *Popups) dropCard(c *card) {
	for i, st := range p.stacks {
		j := slices.Index(st.cards, c)
		if j < 0 {
			continue
		}
		st.cards = slices.Delete(st.cards, j, j+1)
		if len(st.cards) == 0 {
			p.stacks = slices.Delete(p.stacks, i, i+1)
		} else {
			p.refreshStack(st)
		}
		p.layout()
		return
	}
}

// newStack builds an empty stack for key: the clear-all button over
// the pile of cards.
func (p *Popups) newStack(key string) *stack {
	th := widget.Current()
	st := &stack{key: key}
	st.root = widget.NewBox(widget.Column, th.Spacing, 0)
	st.root.AddClass("notification-popup-stack")
	// The stack is the card width exactly, lys-ui's explicit width for
	// content that fills it: its cards fill the pile's rects, the
	// peeking ones narrower, and their Items would otherwise fill the
	// output.
	st.root.SetInlineStyle(styling.WidthStyle(int(cardWidthPx * float64(p.cfg.Styling.Scale))))
	label := widget.NewLabel(p.font, widgets.TextPx(styling.Body, float64(p.cfg.Styling.Scale)), i18n.T("notification-popup-clear-all"), 0)
	st.clear = widget.NewButton(label, 0, 0)
	st.clear.AddClass("notification-popup-clear-all", css.Ghost, css.Small)
	st.clear.SetHAlign(widget.AlignEnd)
	st.clear.OnClick = func() { p.clearStack(st) }
	st.root.Append(st.clear, false)
	st.pile = widget.NewPile()
	st.pile.AddClass("notification-popup-pile")
	st.pile.SetSpacing(th.Spacing)
	st.pile.SetPeekOffset(th.Spacing)
	st.pile.SetPeekEdge(peekEdge(p.cfg.Notification.PopupPosition))
	st.root.Append(st.pile, false)
	st.root.SetOnHoverWithin(func(on bool) { p.hoverStack(st, on) })
	return st
}

// refreshStack lays the stack's cards into its pile, front first, and
// shows what its state calls for: collapsed, the front card over the
// edges of the ones behind it, counting them in its top row; expanded,
// every card in full and the clear-all button.
func (p *Popups) refreshStack(st *stack) {
	expanded := st.hovered
	want := make([]widget.Widget, len(st.cards))
	ids := st.liveIDs()
	for i, c := range st.cards {
		want[i] = c.swipe
		c.setFull(expanded)
		front := len(ids) > 0 && c.id == ids[0] && !c.leaving
		c.count.SetText("+" + strconv.Itoa(len(ids)-1))
		c.count.SetVisible(front && !expanded && len(ids) > 1)
	}
	reconcileChildren(pileChildren(st.pile), want)
	st.pile.SetExpanded(expanded)
	st.clear.SetVisible(expanded && len(ids) > 1)
}

// children is a container's child list as reconcileChildren edits it.
type children struct {
	list   func() []widget.Widget
	remove func(widget.Widget)
	insert func(i int, w widget.Widget)
	// move takes the child at from to to, keeping it attached.
	move func(from, to int)
}

// reconcileChildren makes the children want, in order, detaching none
// that stays: each keeps its layout motion and the pointer's hover.
func reconcileChildren(c children, want []widget.Widget) {
	for _, w := range c.list() {
		if !slices.Contains(want, w) {
			c.remove(w)
		}
	}
	for i, w := range want {
		have := c.list()
		switch at := slices.Index(have, w); {
		case at == i:
		case at >= 0:
			c.move(at, i)
		default:
			c.insert(i, w)
		}
	}
}

// boxChildren edits a box's children.
func boxChildren(b *widget.Box) children {
	return children{
		list:   b.Children,
		remove: func(w widget.Widget) { b.Remove(w) },
		insert: func(i int, w widget.Widget) { b.InsertAt(i, w, false) },
		move:   b.Move,
	}
}

// pileChildren edits a pile's cards. A pile has no move; its cards
// never reorder (a card enters at the front and leaves), so a move
// detaches and reinserts.
func pileChildren(p *widget.Pile) children {
	return children{
		list:   p.Children,
		remove: p.Remove,
		insert: p.Insert,
		move: func(from, to int) {
			w := p.Children()[from]
			p.Remove(w)
			p.Insert(to, w)
		},
	}
}

// hoverStack expands the stack while the pointer is over it and
// collapses it when the pointer leaves; under popup-hover-pause the
// countdowns of its cards pause meanwhile and resume with the time
// they had left.
func (p *Popups) hoverStack(st *stack, on bool) {
	if st.hovered != on {
		p.setHovered(st, on)
	}
}

// setHovered moves the stack to hovered or not, its countdowns with it.
func (p *Popups) setHovered(st *stack, on bool) {
	st.hovered = on
	p.refreshStack(st)
	if !p.cfg.Notification.PopupHoverPause {
		return
	}
	for _, id := range st.liveIDs() {
		if on {
			p.svc.InhibitPopup(id)
		} else {
			p.svc.ReleasePopup(id)
		}
	}
}

// clearStack is the clear-all button: every popup of the stack's
// group closes under popup-close-behavior, those popup-max-visible
// keeps off screen included, so none moves up into the cleared stack.
func (p *Popups) clearStack(st *stack) {
	key := stackKey(p.cfg.Notification.PopupStack)
	for _, n := range p.svc.Popups() {
		if key(n) == st.key {
			p.closeCard(n.ID)
		}
	}
}

// liveIDs are the ids of the stack's cards not leaving, front first.
func (st *stack) liveIDs() []uint32 {
	ids := make([]uint32, 0, len(st.cards))
	for _, c := range st.cards {
		if !c.leaving {
			ids = append(ids, c.id)
		}
	}
	return ids
}

// newCard builds one card (card/mod.rs) on lys-ui's parts: a card
// holding the top row as an Item, then the summary, the body and the
// action rows inset in line with it.
func (p *Popups) newCard(n *notifications.Notification) *card {
	cfg := p.cfg.Notification
	scale := float64(p.cfg.Styling.Scale)
	th := widget.Current()
	px := widgets.TextPx(styling.Body, scale)
	c := &card{id: n.ID}
	c.root = widget.NewBox(widget.Column, 0, 0)
	c.root.AddClass(css.Card, css.Glass, "notification-popup-card", notifyui.UrgencyClass(n.Urgency))
	if cfg.PopupShadow {
		c.root.AddClass("shadow")
	}
	if notifyui.UrgencyBarVisible(n.Urgency, cfg.PopupUrgencyBar) {
		c.root.AddClass("urgency-bar")
	}

	appName := n.AppName
	if appName == "" {
		appName = i18n.T("notification-popup-unknown-app")
	}
	c.head = widget.NewItem(p.font, appName, "")
	c.head.SetSmall(true)
	c.head.SetEllipsize(true)
	c.head.AddClass("notification-popup-content")
	icon := notifyui.ResolveIcon(cfg.IconSource, n)
	size := cardGlyphPx
	if notifyui.IsFileIcon(icon) {
		size = cardIconPx
	}
	glyph := notifyui.NewIcon(icon, size, 0)
	glyph.AddClass("notification-popup-icon")
	if notifyui.IsFileIcon(icon) {
		glyph.AddClass("file-icon")
	}
	c.head.SetMedia(glyph, notifyui.IsFileIcon(icon))
	c.count = widget.NewBadge(p.font, "")
	c.count.AddClass("notification-popup-count")
	c.count.SetVisible(false)
	c.head.AddAction(c.count)
	c.time = widget.NewLabel(p.font, px, notifyui.TimeLabel(notifyui.RelativeTime(p.now(), n.Added), "notification-popup"), 0)
	c.time.AddClass("notification-popup-time", css.Caption, css.DimLabel)
	c.head.AddAction(c.time)
	c.close = widget.NewButton(widget.NewThemeIcon("ld-x-symbolic", cardGlyphPx), 0, 0)
	c.close.AddClass("notification-popup-close", css.Ghost, css.Icon, css.Small)
	id := n.ID
	c.close.OnClick = func() { p.closeCard(id) }
	c.head.AddAction(c.close)
	c.root.Append(c.head, false)

	content := widget.NewBox(widget.Column, th.Spacing, 0)
	content.SetPadding(render.Insets{Left: 2 * th.Padding, Right: 2 * th.Padding, Bottom: 2 * th.Padding})
	c.summary = widget.NewLabel(p.font, px, n.Summary, 0)
	c.summary.AddClass("notification-popup-summary", css.Heading)
	c.summary.SetWrap(true)
	c.summary.SetEllipsize(widget.EllipsizeEnd)
	c.summary.SetVisible(n.Summary != "")
	content.Append(c.summary, false)
	c.body = widget.NewRichLabel(p.font, px, notifyui.BodyMarkup(n.Body), 0)
	c.body.SetVariants(app.FontVariantsOf(p.font))
	c.body.AddClass("notification-popup-body", css.DimLabel)
	c.body.SetWrap(true)
	c.body.SetMaxWidthChars(cardMaxWidthChars)
	c.body.SetEllipsize(widget.EllipsizeEnd)
	c.body.SetVisible(n.Body != "")
	content.Append(c.body, false)
	c.actions = p.actionRows(n, px)
	content.Append(c.actions, false)
	content.SetVisible(c.summary.Visible() || c.body.Visible() || len(c.actions.Children()) > 0)
	c.root.Append(content, false)
	c.setFull(false)

	// A default action opens the card on a click anywhere its buttons
	// do not take (setup_default_action's gesture on the root): the
	// Item's own activation, and the root's click for the text under it.
	if _, ok := n.DefaultAction(); ok {
		c.root.SetCursorName("pointer")
		c.head.SetOnActivate(func() { p.invokeDefault(id) })
		c.root.SetOnClickWithin(func() { p.invokeDefault(id) })
	}
	c.rev = widget.NewRevealer(c.root)
	c.rev.SetGenieEdge(reveal.GenieEdge(popupAtTop(cfg.PopupPosition)))
	c.swipe = widget.NewSwipeDismiss(c.rev)
	c.swipe.OnDismiss = func(int) { p.closeCard(id) }
	return c
}

// setFull shows the card whole, as its expanded stack does: the summary
// and the body unclamped and the action rows; at rest the summary keeps
// to a line, the body to cardBodyLines, and the actions stay hidden.
func (c *card) setFull(full bool) {
	if full {
		c.summary.SetMaxLines(0)
		c.body.SetMaxLines(0)
	} else {
		c.summary.SetMaxLines(1)
		c.body.SetMaxLines(cardBodyLines)
	}
	c.actions.SetVisible(full && len(c.actions.Children()) > 0)
}

// popupAtTop is whether the popups sit along the top edge, the edge a
// genie collapses toward (genie_edge).
func popupAtTop(position config.PopupPosition) bool {
	switch position {
	case config.PopupPositionTopLeft, config.PopupPositionTopCenter, config.PopupPositionTopRight:
		return true
	}
	return false
}

// peekEdge is the edge a stack's older cards peek past: away from the
// screen edge the popups hang from.
func peekEdge(position config.PopupPosition) widget.Edge {
	if popupAtTop(position) {
		return widget.EdgeBottom
	}
	return widget.EdgeTop
}

// actionRows is setup_action_buttons: the non-default actions as
// lys-ui's small outline buttons, three to a row, each row homogeneous
// so its buttons split its width evenly.
func (p *Popups) actionRows(n *notifications.Notification, px float64) *widget.Box {
	gap := widget.Current().Spacing / 2
	box := widget.NewBox(widget.Column, gap, 0)
	box.AddClass("notification-popup-actions")
	actions := notifyui.VisibleActions(n)
	for start := 0; start < len(actions); start += notifyui.ActionsPerRow {
		row := widget.NewBox(widget.Row, gap, 0)
		row.AddClass("notification-popup-action-row")
		row.SetHomogeneous(true)
		for _, a := range actions[start:min(start+notifyui.ActionsPerRow, len(actions))] {
			label := widget.NewLabel(p.font, px, a.Label, 0)
			label.SetAlignment(render.AlignCenter)
			b := widget.NewButton(label, 0, 0)
			b.AddClass("notification-popup-action-btn", css.Outline, css.Small)
			id, key := n.ID, a.ID
			b.OnClick = func() { p.invokeAction(id, key) }
			row.Append(b, true)
		}
		box.Append(row, false)
	}
	return box
}

// closeCard is the close button under popup-close-behavior: dismiss
// hides the popup and keeps the notification in the history, remove
// deletes it.
func (p *Popups) closeCard(id uint32) {
	if p.cfg.Notification.PopupCloseBehavior == config.PopupCloseBehaviorRemove {
		p.svc.Close(id, notifications.Dismissed)
		return
	}
	p.svc.DismissPopup(id)
}

// invokeAction is an action button: the popup goes, the action is
// invoked with a token from the press, and the notification is
// dismissed (build_action_button).
func (p *Popups) invokeAction(id uint32, key string) {
	notifyui.WithActivationToken(p.minter, func(token string) {
		p.svc.DismissPopup(id)
		p.svc.InvokeAction(id, key, token)
		p.svc.Close(id, notifications.Dismissed)
	})
}

// invokeDefault is the card's click: the default action with a token
// from the click, then the close behavior. Invoked first, while the
// notification still exists for the service to emit ActionInvoked on.
func (p *Popups) invokeDefault(id uint32) {
	notifyui.WithActivationToken(p.minter, func(token string) {
		p.svc.InvokeAction(id, notifications.DefaultActionID, token)
		p.closeCard(id)
	})
}

// layout puts the stacks into the root, mapping the window for the
// first card and closing it after the last has gone.
func (p *Popups) layout() {
	if len(p.stacks) == 0 {
		if p.win != nil {
			win := p.win
			p.win, p.winOut, p.root = nil, nil, nil
			win.Close()
		}
		return
	}
	if p.win == nil && p.app != nil {
		out := layering.Output(p.outputs(), p.cfg.Notification.PopupMonitor)
		if out == nil {
			return
		}
		win, root, err := p.ensureWindow(out)
		if err != nil {
			log.Printf("notification popups: cannot map on %s: %v", out.Name, err)
			return
		}
		p.win, p.winOut, p.root = win, out, root
	}
	if p.root == nil {
		// Headless (or window creation failed): the card set tracks the
		// popup state without a surface.
		return
	}
	want := make([]widget.Widget, len(p.stacks))
	for i, st := range p.stacks {
		want[i] = st.root
	}
	reconcileChildren(boxChildren(p.root), want)
}

// ensureWindow maps the popup surface on out at the configured
// position, margins, and layer, sized by its stacks. The host window is
// transparent (.notification-popup-host) and asks the compositor to
// blur behind its frosted cards; the list spaces the stacks by
// popup-gap and each card draws its own plate from the stylesheet. It
// returns the list the stacks go into.
func (p *Popups) ensureWindow(out *app.Output) (*app.LayerWindow, *widget.Box, error) {
	cfg := p.cfg.Notification
	scale := float64(p.cfg.Styling.Scale)
	host, list := p.newHost()
	win, err := p.app.NewLayer(app.LayerConfig{
		Output:         out,
		Layer:          layering.For(p.cfg.General, cfg.PopupLayer),
		Anchor:         popupAnchors(cfg.PopupPosition),
		Margin:         popupMargins(cfg, scale),
		Namespace:      "lys-shell-notification-popup",
		Root:           host,
		BackgroundBlur: true,
	})
	return win, list, err
}

// newHost builds the surface's widget tree: the transparent host
// carrying the stylesheet, around the list that spaces the stacks by
// popup-gap and keeps that same gap from every edge of the surface,
// room the cards' shadows paint into.
func (p *Popups) newHost() (host, list *widget.Box) {
	gap := int(p.cfg.Notification.PopupGap.ResolvePx(popupGapBasePx, float64(p.cfg.Styling.Scale)))
	list = widget.NewBox(widget.Column, gap, gap)
	list.AddClass("notification-popup-list")
	host = widget.NewBox(widget.Column, 0, 0)
	host.SetElement("window")
	host.AddClass("background", "notification-popup-host")
	if p.sheet != nil {
		host.AttachStylesheet(p.sheet)
	}
	host.Append(list, false)
	return host, list
}

// popupMargins is apply_position's margins: popup-margin-y on the
// anchored top or bottom edge, popup-margin-x on the anchored side,
// truncated as the Rust `as i32`.
func popupMargins(cfg config.NotificationConfig, scale float64) app.Margins {
	mx := int32(cfg.PopupMarginX.ResolvePx(popupMarginBasePx, scale))
	my := int32(cfg.PopupMarginY.ResolvePx(popupMarginBasePx, scale))
	var m app.Margins
	anchors := popupAnchors(cfg.PopupPosition)
	if anchors&app.AnchorTop != 0 {
		m.Top = my
	}
	if anchors&app.AnchorBottom != 0 {
		m.Bottom = my
	}
	if anchors&app.AnchorLeft != 0 {
		m.Left = mx
	}
	if anchors&app.AnchorRight != 0 {
		m.Right = mx
	}
	return m
}

// popupAnchors maps the position onto layer-shell anchors
// (notification_popup/methods.rs apply_position): corners take both
// edges, centered positions only the one edge.
func popupAnchors(position config.PopupPosition) app.Anchor {
	switch position {
	case config.PopupPositionTopLeft:
		return app.AnchorTop | app.AnchorLeft
	case config.PopupPositionTopCenter:
		return app.AnchorTop
	case config.PopupPositionTopRight:
		return app.AnchorTop | app.AnchorRight
	case config.PopupPositionBottomLeft:
		return app.AnchorBottom | app.AnchorLeft
	case config.PopupPositionBottomCenter:
		return app.AnchorBottom
	case config.PopupPositionBottomRight:
		return app.AnchorBottom | app.AnchorRight
	case config.PopupPositionCenterLeft:
		return app.AnchorLeft
	case config.PopupPositionCenterRight:
		return app.AnchorRight
	}
	return app.AnchorTop | app.AnchorRight
}

// Visible reports the card count (tests).
func (p *Popups) Visible() int {
	return len(p.cards)
}

// stackOrder is the stacks on screen top to bottom, each its card ids
// front first, cards still playing their exit included (tests).
func (p *Popups) stackOrder() [][]uint32 {
	out := make([][]uint32, len(p.stacks))
	for i, st := range p.stacks {
		for _, c := range st.cards {
			out[i] = append(out[i], c.id)
		}
	}
	return out
}
