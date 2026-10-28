package bar

import (
	"log"
	"os"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/spawn"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/useraccount"
)

// avatarPx is the user avatar's logical box in the session row.
const avatarPx = 32

// sessionButton is one session action's glyph, tooltip, and command
// (user_session/factory.rs make_session_btn's per-action arguments).
type sessionButton struct {
	icon    string
	tooltip string
	command string
}

// sessionButtonFor maps an action onto its button; the commands come
// from the dashboard's dropdown-*-command keys and the tooltips are
// the factory's i18n keys.
func sessionButtonFor(action config.SessionAction, cfg config.DashboardConfig) sessionButton {
	switch action {
	case config.SessionActionLock:
		return sessionButton{"ld-lock-symbolic", i18n.T("dropdown-dashboard-lock"), cfg.DropdownLockCommand}
	case config.SessionActionLogOut:
		return sessionButton{"ld-log-out-symbolic", i18n.T("dropdown-dashboard-logout"), cfg.DropdownLogoutCommand}
	case config.SessionActionReboot:
		return sessionButton{"ld-refresh-cw-symbolic", i18n.T("dropdown-dashboard-reboot"), cfg.DropdownRebootCommand}
	default:
		return sessionButton{"ld-power-symbolic", i18n.T("dropdown-dashboard-power-off"), cfg.DropdownPoweroffCommand}
	}
}

// sessionUsername is $USER, "user" when unset (dashboard mod.rs).
func sessionUsername() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "user"
}

// currentAccount is the user the dashboard shows (a seam for tests).
var currentAccount = useraccount.Current

// facePath is the user's avatar (the AccountsService icon, else
// ~/.face), looked up afresh; "" for none.
func facePath() string {
	acct, err := currentAccount()
	if err != nil {
		log.Printf("dashboard: %v", err)
		return ""
	}
	return useraccount.FindAvatar(acct.Name, acct.Home)
}

// setAvatar paints the face onto the tile as its background image —
// the Rust avatar's `background-image: url(file://…)` inline style —
// cover-fit inside the rounded box. The tile shows the user glyph
// when the file is absent.
func setAvatar(tile *widget.Box) {
	path := facePath()
	if path == "" {
		return
	}
	tile.SetInlineStylePriority("background-image: url('"+path+"');", apptheme.ComponentPriority)
}

// userSessionSection is the dashboard's user-session card
// (user_session/mod.rs): the card over the session row — the avatar
// (the account's avatar, else the user glyph) and the username in .user-info, one
// icon button per configured action in .session-actions pinned to the
// row's end. The face is read when the dropdown opens, which is when
// the Rust watcher's change would next be seen. The stylesheet inks
// everything through the classes (user-avatar image, user-name,
// button.icon.session-btn image), so nothing carries a tint here.
func userSessionSection(ctx ModuleContext) widget.Widget {
	font, _ := dropdownFont(ctx)
	cfg := ctx.Config.Dashboard
	card := widget.NewBox(widget.Column, 0, 0)
	card.AddClass(css.Card, "dashboard-card")
	row := widget.NewItem(font, sessionUsername(), "")
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("dashboard-user-session")
	tile := widget.NewBox(widget.Row, 0, 0)
	tile.AddClass("user-avatar")
	setAvatar(tile)
	tile.Append(widget.NewThemeIcon("ld-user-symbolic", avatarPx), false)
	row.SetMedia(tile, true)
	for _, action := range cfg.UserSession.Actions {
		spec := sessionButtonFor(action, cfg)
		glyph := widget.NewThemeIcon(spec.icon, itemIconPx)
		button := widget.NewButton(glyph, 0, 0)
		button.AddClass(css.Ghost, css.Icon, css.Small, "session-btn")
		button.SetTooltip(spec.tooltip)
		command := spec.command
		// process::run_if_set: an empty command is a no-op.
		button.OnClick = func() { _ = spawn.Quiet(command) }
		row.AddAction(button)
	}
	card.Append(row, false)
	return card
}
