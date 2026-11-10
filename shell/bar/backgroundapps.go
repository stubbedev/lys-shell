package bar

import (
	"context"
	"errors"
	"log"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/backgroundapps"
)

// genericAppIcon stands in for an app whose desktop entry names no icon
// or that has no entry.
const genericAppIcon = "ld-app-window-symbolic"

// backgroundAppSource is the portal's background apps
// (*backgroundapps.Apps).
type backgroundAppSource interface {
	List() []backgroundapps.App
	Subscribe() (<-chan struct{}, func())
	Stop(ctx context.Context, instance backgroundapps.InstanceID) error
}

// startBackgroundApps follows the background monitor on the shell's
// session bus for every bar, leaving ctx.BackgroundApps nil without a
// bus; the returned func stops following.
func startBackgroundApps(ctx *ModuleContext, conn *dbus.Conn) func() {
	if conn == nil {
		return func() {}
	}
	apps, err := backgroundapps.Follow(conn, describeApp)
	if err != nil {
		log.Printf("background-apps: %v", err)
		return func() {}
	}
	ctx.BackgroundApps = apps
	return apps.Close
}

var errBackgroundAppsNoService = errors.New("background-apps: requires the session bus")

// newBackgroundApps is the background apps indicator: shown while a
// sandboxed app runs without a window, the number of them beside its
// icon under label-show.
func newBackgroundApps(ctx ModuleContext) (Module, error) {
	src := ctx.BackgroundApps
	if src == nil {
		return nil, errBackgroundAppsNoService
	}
	cfg := ctx.Config.BackgroundApps
	return newCountIndicator(ctx, config.IconWith(cfg.IconShow, cfg.Icon, cfg.IconColor), func() int { return len(src.List()) }, src.Subscribe), nil
}

// backgroundAppsView is the background apps dropdown: a row per app,
// its name and icon over its status message, with a button that stops
// it; or the empty state.
type backgroundAppsView struct {
	ctx  ModuleContext
	src  backgroundAppSource
	font render.Font
	px   float64

	*widget.Box
	pages *widget.Stack
	list  *widget.ItemGroup

	*openFollow
}

func backgroundAppsDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &backgroundAppsView{ctx: ctx, src: ctx.BackgroundApps, font: font, px: px}
	v.openFollow = newOpenFollow(v.follow)
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "background-apps-dropdown")
	icon := ctx.Config.BackgroundApps.Icon
	v.Append(dropdownHeader(font, icon, i18n.T("dropdown-background-apps-title")), false)
	card, list := itemCard()
	card.AddClass("background-apps-dropdown-list")
	v.list = list
	v.pages = pageStack()
	v.pages.Add("apps", growingScroll(scrolledCard(card, "")))
	v.pages.Add("empty", widgets.EmptyState(font, px, icon, i18n.T("dropdown-background-apps-empty"), ""))
	content := dropdownContent()
	content.Append(v.pages, true)
	v.Append(content, true)
	v.startFollowing()
	return v
}

// rebuild lists the apps running in the background now.
func (v *backgroundAppsView) rebuild() {
	v.list.Clear()
	var apps []backgroundapps.App
	if v.src != nil {
		apps = v.src.List()
	}
	if len(apps) == 0 {
		v.pages.Show("empty")
		return
	}
	for _, a := range apps {
		v.list.Append(v.row(a))
	}
	v.pages.Show("apps")
}

// row is one app: its icon, its name over its status message, and Stop.
func (v *backgroundAppsView) row(a backgroundapps.App) *widget.Item {
	row := widget.NewItem(v.font, a.Name, a.Message)
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("background-apps-dropdown-row")
	var icon widget.Widget = widget.NewThemeIcon(genericAppIcon, itemIconPx)
	if named := widgets.NewAppIcon(a.Icon, itemIconPx, v.ctx.Invoke); named != nil {
		icon = named
	}
	row.SetMedia(icon, false)
	instance := a.Instance
	stop := dropdownButton(widget.NewLabel(v.font, v.px, i18n.T("dropdown-background-apps-stop"), 0), func() {
		go func() {
			if err := v.src.Stop(context.Background(), instance); err != nil {
				log.Printf("background-apps: %v", err)
			}
		}()
	}, "background-apps-stop", css.DestructiveAction, css.Small)
	row.AddAction(stop)
	return row
}

func (v *backgroundAppsView) follow(life context.Context) {
	v.rebuild()
	if v.src == nil {
		return
	}
	followTicks(life, v.src.Subscribe, func() { v.ctx.Invoke(v.rebuild) })
}
