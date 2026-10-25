// Package popups renders the notification popup window: a stack of
// cards on one output, following the notification service's feed,
// auto-dismissing after the configured duration, hidden while DND.
package popups

import (
	"log"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/shell/layering"
	"github.com/stubbedev/lys-shell/shell/notifyui"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/styling"
)

// Card geometry (templates.rs, card/methods.rs).
const (
	cardMaxWidthChars = 40
	cardBodyLines     = 3
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

// Popups owns the popup surface for one output.
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
	mu     sync.Mutex
	win    *app.LayerWindow
	// winOut is the output win is on.
	winOut *app.Output
	root   *widget.Box
	// cards are the live popups by id; stack is every card on screen
	// top to bottom, those still playing their exit included.
	cards map[uint32]*card
	stack []*card
}

// card is one popup (NotificationPopupCard): the notification as
// lys-ui's Item (the icon, the summary over the app, the age and the
// close button), the body, and the action rows.
type card struct {
	id uint32
	// rev plays the card's enter and exit (the LysShellRevealer around it).
	rev     *widget.Revealer
	root    *widget.Box
	head    *widget.Item
	time    *widget.Label
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

// DetachOutput closes the surface when its output went away; the stack
// remaps on the configured monitor (the primary fallback) at once.
func (p *Popups) DetachOutput(name string) {
	p.mu.Lock()
	if p.win == nil || p.winOut == nil || p.winOut.Name != name {
		p.mu.Unlock()
		return
	}
	win := p.win
	p.win, p.winOut, p.root = nil, nil, nil
	p.layoutLocked()
	p.mu.Unlock()
	win.Close()
}

// SetConfig applies a reloaded config on the loop goroutine: the
// surface closes and the stack rebuilds at the new position, layer,
// gap, limits, and card behavior.
func (p *Popups) SetConfig(cfg *config.Config) {
	p.svc.SetPopupDuration(time.Duration(cfg.Notification.PopupDuration) * time.Millisecond)
	p.mu.Lock()
	p.cfg = cfg
	win := p.win
	p.win, p.winOut, p.root = nil, nil, nil
	p.cards = make(map[uint32]*card)
	p.stack = nil
	p.mu.Unlock()
	if win != nil {
		win.Close()
	}
	p.sync()
}

// Run follows the service's feed until the process exits; each change
// reconciles on the loop goroutine, which owns the widget tree.
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

// sync reconciles the card stack with the service's popup set
// (reconcile): a card no longer listed plays its exit and leaves the
// stack once it has; a new one enters at the top (newest first) or the
// bottom (oldest first), the insert_new_cards prepend. Headless
// construction tracks the cards; only the window needs the application.
func (p *Popups) sync() {
	visible := p.svc.Popups()
	p.mu.Lock()
	if limit := int(p.cfg.Notification.PopupMaxVisible); len(visible) > limit {
		visible = visible[:limit]
	}
	live := make(map[uint32]bool, len(visible))
	for _, n := range visible {
		live[n.ID] = true
	}
	var leaving, entering []*card
	for id, c := range p.cards {
		if !live[id] {
			delete(p.cards, id)
			leaving = append(leaving, c)
		}
	}
	newestFirst := p.cfg.Notification.PopupStackingOrder != config.StackingOrderOldestFirst
	// The service lists popups newest first; inserting the new ones
	// oldest first puts the newest on top of a newest-first stack and
	// at the bottom of an oldest-first one.
	for _, n := range slices.Backward(visible) {
		if _, ok := p.cards[n.ID]; ok {
			continue
		}
		c := p.newCard(n)
		p.cards[n.ID] = c
		entering = append(entering, c)
		if newestFirst {
			p.stack = slices.Insert(p.stack, 0, c)
		} else {
			p.stack = append(p.stack, c)
		}
	}
	p.layoutLocked()
	anims := p.cfg.Animations
	p.mu.Unlock()
	// Transitions start unlocked: an exit that lands at once (animations
	// off) drops its card through dropCard, which takes the lock.
	for _, c := range leaving {
		reveal.Hide(c.rev, anims, config.AnimNotifications, func() { p.dropCard(c) })
	}
	for _, c := range entering {
		reveal.Show(c.rev, anims, config.AnimNotifications)
	}
}

// dropCard takes a card whose exit has played off the stack, and the
// window down with the last one.
func (p *Popups) dropCard(c *card) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.Index(p.stack, c)
	if i < 0 {
		return
	}
	p.stack = slices.Delete(p.stack, i, i+1)
	p.layoutLocked()
}

// newCard builds one card (card/mod.rs) on lys-ui's parts: a card
// holding the notification as an Item, over the body and the action
// rows inset in line with it.
func (p *Popups) newCard(n *notifications.Notification) *card {
	cfg := p.cfg.Notification
	scale := float64(p.cfg.Styling.Scale)
	px := widget.Current().TextSize * scale
	c := &card{id: n.ID}
	c.root = widget.NewBox(widget.Column, 0, 0)
	c.root.AddClass(css.Card, "notification-popup-card", notifyui.UrgencyClass(n.Urgency))
	// The card is its width exactly, lys-ui's explicit width for
	// content that fills it: the Item would otherwise fill the output.
	c.root.SetInlineStyle(styling.WidthStyle(int(cardWidthPx * scale)))
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
	c.head = widget.NewItem(p.font, n.Summary, appName)
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
	c.time = widget.NewLabel(p.font, px, notifyui.TimeLabel(notifyui.RelativeTime(p.now(), n.Added), "notification-popup"), 0)
	c.time.AddClass("notification-popup-time", css.Caption, css.DimLabel)
	c.head.AddAction(c.time)
	c.close = widget.NewButton(widget.NewThemeIcon("ld-x-symbolic", cardGlyphPx), 0, 0)
	c.close.AddClass("notification-popup-close", css.Ghost, css.Icon, css.Small)
	id := n.ID
	c.close.OnClick = func() { p.closeCard(id) }
	c.head.AddAction(c.close)
	c.root.Append(c.head, false)

	th := widget.Current()
	body := widget.NewBox(widget.Column, th.Spacing, 0)
	body.SetPadding(render.Insets{Left: 2 * th.Padding, Right: 2 * th.Padding, Bottom: 2 * th.Padding})
	c.body = widget.NewRichLabel(p.font, px, notifyui.BodyMarkup(n.Body), 0)
	c.body.SetVariants(app.FontVariantsOf(p.font))
	c.body.AddClass("notification-popup-body", css.DimLabel)
	c.body.SetWrap(true)
	c.body.SetMaxWidthChars(cardMaxWidthChars)
	c.body.SetMaxLines(cardBodyLines)
	c.body.SetEllipsize(widget.EllipsizeEnd)
	c.body.SetVisible(n.Body != "")
	body.Append(c.body, false)
	c.actions = p.actionRows(n, px)
	body.Append(c.actions, false)
	body.SetVisible(c.body.Visible() || c.actions.Visible())
	c.root.Append(body, false)

	// A default action opens the card on a click anywhere its buttons
	// do not take (setup_default_action's gesture on the root): the
	// Item's own activation, and the root's click for the body under it.
	if _, ok := n.DefaultAction(); ok {
		c.root.SetCursorName("pointer")
		c.head.SetOnActivate(func() { p.invokeDefault(id) })
		c.root.SetOnClickWithin(func() { p.invokeDefault(id) })
	}
	if cfg.PopupHoverPause {
		// The countdown lives in the service; hovering pauses it and
		// leaving resumes it with the time that was left.
		c.root.SetOnHoverWithin(func(on bool) {
			if on {
				p.svc.InhibitPopup(id)
				return
			}
			p.svc.ReleasePopup(id)
		})
	}
	c.rev = widget.NewRevealer(c.root)
	c.rev.SetGenieEdge(reveal.GenieEdge(popupAtTop(cfg.PopupPosition)))
	return c
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

// actionRows is setup_action_buttons: the non-default actions as
// lys-ui's small outline buttons, three to a row, each row homogeneous
// so its buttons split its width evenly; hidden without any.
func (p *Popups) actionRows(n *notifications.Notification, px float64) *widget.Box {
	gap := widget.Current().Spacing / 2
	box := widget.NewBox(widget.Column, gap, 0)
	box.AddClass("notification-popup-actions")
	actions := notifyui.VisibleActions(n)
	box.SetVisible(len(actions) > 0)
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

// layoutLocked rebuilds the root from the stack, mapping the window for
// the first card and closing it after the last has gone. The caller
// holds mu.
func (p *Popups) layoutLocked() {
	if len(p.stack) == 0 {
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
	p.root.Clear()
	for _, c := range p.stack {
		p.root.Append(c.rev, false)
	}
}

// ensureWindow maps the popup surface on out at the configured
// position, margins, and layer, sized by its cards. The host window is
// transparent (.notification-popup-host); the list spaces the cards by
// popup-gap and each card draws its own plate from the stylesheet. It
// returns the list the cards go into.
func (p *Popups) ensureWindow(out *app.Output) (*app.LayerWindow, *widget.Box, error) {
	cfg := p.cfg.Notification
	scale := float64(p.cfg.Styling.Scale)
	list := widget.NewBox(widget.Column, int(cfg.PopupGap.ResolvePx(popupGapBasePx, scale)), 0)
	list.AddClass("notification-popup-list")
	host := widget.NewBox(widget.Column, 0, 0)
	host.SetElement("window")
	host.AddClass("background", "notification-popup-host")
	if p.sheet != nil {
		host.AttachStylesheet(p.sheet)
	}
	host.Append(list, false)
	win, err := p.app.NewLayer(app.LayerConfig{
		Output:    out,
		Layer:     layering.For(p.cfg.General, cfg.PopupLayer),
		Anchor:    popupAnchors(cfg.PopupPosition),
		Margin:    popupMargins(cfg, scale),
		Namespace: "lys-shell-notification-popup",
		Root:      host,
	})
	return win, list, err
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
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.cards)
}

// stackOrder is the on-screen stack top to bottom by id, cards still
// playing their exit included (tests).
func (p *Popups) stackOrder() []uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]uint32, len(p.stack))
	for i, c := range p.stack {
		ids[i] = c.id
	}
	return ids
}
