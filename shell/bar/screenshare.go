package bar

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/screenshare"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// shareSource is the portal's screen shares (*screenshare.Shares).
type shareSource interface {
	List() []screenshare.Share
	Subscribe() (<-chan struct{}, func())
	Stop(ctx context.Context, session dbus.ObjectPath) error
}

// screenShareSource follows the portal's shares, once for every bar.
var screenShareSource = sync.OnceValues(func() (shareSource, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return screenshare.Follow(conn)
})

// newScreenShare is the screen share indicator: shown while the portal
// streams the screen or a window, the number of shares beside its icon
// under label-show.
func newScreenShare(ctx ModuleContext) (Module, error) {
	src, err := screenShareSource()
	if err != nil {
		return nil, fmt.Errorf("screen-share: %w", err)
	}
	cfg := ctx.Config.ScreenShare
	return newCountIndicator(ctx, config.IconWith(cfg.IconShow, cfg.Icon, cfg.IconColor), cfg.LabelShow, func() int { return len(src.List()) }, src.Subscribe), nil
}

// screenShareView is the screen share dropdown: a row per share, its app
// and what it streams, with a button that stops it; or the empty state.
type screenShareView struct {
	ctx  ModuleContext
	src  shareSource
	font render.Font
	px   float64

	*widget.Box
	pages *widget.Stack
	list  *widget.ItemGroup

	*openFollow
}

func screenShareDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &screenShareView{ctx: ctx, font: font, px: px}
	if src, err := screenShareSource(); err == nil {
		v.src = src
	}
	v.openFollow = newOpenFollow(v.follow)
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "screen-share-dropdown")
	icon := ctx.Config.ScreenShare.Icon
	v.Append(dropdownHeader(font, icon, i18n.T("dropdown-screen-share-title")), false)
	card, list := itemCard()
	card.AddClass("screen-share-dropdown-list")
	v.list = list
	v.pages = pageStack()
	v.pages.Add("shares", growingScroll(scrolledCard(card, "")))
	v.pages.Add("empty", widgets.EmptyState(font, px, icon, i18n.T("dropdown-screen-share-empty"), ""))
	content := dropdownContent()
	content.Append(v.pages, true)
	v.Append(content, true)
	v.startFollowing()
	return v
}

// rebuild lists the shares running now.
func (v *screenShareView) rebuild() {
	v.list.Clear()
	var shares []screenshare.Share
	if v.src != nil {
		shares = v.src.List()
	}
	if len(shares) == 0 {
		v.pages.Show("empty")
		return
	}
	for _, s := range shares {
		v.list.Append(v.row(s))
	}
	v.pages.Show("shares")
}

// row is one share: its app over what it streams, and Stop.
func (v *screenShareView) row(s screenshare.Share) *widget.Item {
	row := widget.NewItem(v.font, shareApp(s), shareTargets(s))
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("screen-share-dropdown-row")
	row.SetMedia(widget.NewThemeIcon(v.ctx.Config.ScreenShare.Icon, itemIconPx), false)
	session := s.Session
	stop := dropdownButton(widget.NewLabel(v.font, v.px, i18n.T("dropdown-screen-share-stop"), 0), func() {
		go func() {
			if err := v.src.Stop(context.Background(), session); err != nil {
				log.Printf("screen-share: %v", err)
			}
		}()
	}, "screen-share-stop", css.DestructiveAction, css.Small)
	row.AddAction(stop)
	return row
}

// shareApp names the app sharing: its id, or "An app" for one outside a
// sandbox, which the portal knows no id of.
func shareApp(s screenshare.Share) string {
	app := s.App
	if app == "" {
		app = i18n.T("dropdown-screen-share-unknown-app")
	}
	if s.Kind == screenshare.KindRemoteDesktop {
		return i18n.T("dropdown-screen-share-remote", i18n.Str("app", app))
	}
	return app
}

// shareTargets says what a share streams: each screen by its connector,
// each window by its title.
func shareTargets(s screenshare.Share) string {
	parts := make([]string, 0, len(s.Targets))
	for _, t := range s.Targets {
		switch t.Kind {
		case screenshare.TargetWindow:
			title := t.Title
			if title == "" {
				title = t.Name
			}
			parts = append(parts, i18n.T("dropdown-screen-share-window", i18n.Str("title", title)))
		case screenshare.TargetRegion:
			parts = append(parts, i18n.T("dropdown-screen-share-region", i18n.Str("name", t.Name)))
		default:
			parts = append(parts, i18n.T("dropdown-screen-share-output", i18n.Str("name", t.Name)))
		}
	}
	return strings.Join(parts, ", ")
}

func (v *screenShareView) follow(life context.Context) {
	v.rebuild()
	if v.src == nil {
		return
	}
	followTicks(life, v.src.Subscribe, func() { v.ctx.Invoke(v.rebuild) })
}
