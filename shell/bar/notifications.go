package bar

import (
	"errors"
	"fmt"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/notifications"
)

// notificationLabel is helpers.rs's format_label: the count
// zero-padded to two digits.
func notificationLabel(count int) string {
	return fmt.Sprintf("%02d", count)
}

// notificationIconName is helpers.rs's select_icon: dnd wins, then
// the unread dot, then the resting bell.
func notificationIconName(cfg config.NotificationConfig, count int, dnd bool) string {
	if dnd {
		return cfg.IconDnd
	}
	if count > 0 {
		return cfg.IconUnread
	}
	return cfg.Icon().Name
}

// notificationModule is the bell: count and dnd from the shared
// notification service.
type notificationModule struct {
	ctx  ModuleContext
	src  *notifications.Service
	btn  *barButton
	icon *widget.Icon
}

func newNotification(ctx ModuleContext) (Module, error) {
	m := &notificationModule{ctx: ctx, src: ctx.Notifications}
	if m.src == nil {
		return nil, errors.New("notifications: no notification service available")
	}
	m.icon = moduleIcon(ctx, ctx.Config.Notification.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	m.refresh()
	// Follow the service's change feed (subscribed before returning, so
	// no event after construction is missed). Headless construction
	// refreshes inline.
	events, stop := m.src.Subscribe()
	follow(m.ctx, events, stop, func(notifications.Event) { m.refresh() })
	return m, nil
}

// refresh applies the count and dnd state to label and icon.
func (m *notificationModule) refresh() {
	cfg := m.ctx.Config.Notification
	count := m.src.Count()
	dnd := m.src.DND()
	text := ""
	if cfg.LabelShow {
		text = notificationLabel(count)
	}
	m.btn.SetLabel(text)
	m.btn.applyThresholds(float64(count), cfg.Thresholds)
	if setter := m.icon; setter != nil {
		setter.SetThemeName(notificationIconName(cfg, count, dnd))
	}
}

func (m *notificationModule) Root() widget.Widget { return m.btn }

// RunAction handles the dnd toggle the Rust module exposes through
// `lys-shell notify dnd`; the shell command path routes here.
func (m *notificationModule) RunAction(action config.ClickAction) {
	if action.Command == "toggle-dnd" {
		m.src.ToggleDND()
		return
	}
	runClickAction(m.ctx, action)
}
