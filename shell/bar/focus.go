package bar

import (
	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/notifications"
)

func focusText(f notifications.Focus) string {
	at := func(id string) string { return i18n.T(id, i18n.Str("time", f.Until.Format("15:04"))) }
	switch f.Reason {
	case notifications.FocusManual:
		return i18n.T("notification-dnd-reason-manual")
	case notifications.FocusUntil:
		return at("notification-dnd-reason-until")
	case notifications.FocusOverride:
		if f.Until.IsZero() {
			return i18n.T("notification-dnd-reason-override-open")
		}
		return at("notification-dnd-reason-override")
	case notifications.FocusScheduled:
		if f.Until.IsZero() {
			return i18n.T("notification-dnd-reason-scheduled-open")
		}
		return at("notification-dnd-reason-scheduled")
	case notifications.FocusSharing:
		return i18n.T("notification-dnd-reason-sharing")
	case notifications.FocusFullscreen:
		return i18n.T("notification-dnd-reason-fullscreen")
	}
	return ""
}
