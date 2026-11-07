package bar

import (
	"context"
	"log"
	"os"
	"strconv"
	"syscall"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/privacy"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// cameraSource is the processes holding a camera (*privacy.Cameras).
type cameraSource interface {
	Users() []privacy.CameraUser
	Subscribe() (<-chan struct{}, func())
}

// privacyState is what records the user now.
type privacyState struct {
	mics    []privacy.MicUser
	cameras []privacy.CameraUser
	shares  int
}

// apps counts the apps the indicator names.
func (s privacyState) apps() int { return len(s.mics) + len(s.cameras) + s.shares }

// selfPID is lys-shell's own process, whose recorder and visualizer
// record without counting as an app listening in.
func selfPID() uint32 { return intconv.Clamp[uint32](os.Getpid()) }

// readPrivacy reads the state from the sources there are.
func readPrivacy(ctx ModuleContext) privacyState {
	var s privacyState
	if ctx.Pulse != nil {
		s.mics = privacy.MicUsers(ctx.Pulse.RecordingStreams(), ctx.Pulse.InputDevices(), selfPID())
	}
	if ctx.Cameras != nil {
		s.cameras = ctx.Cameras.Users()
	}
	if ctx.ScreenShares != nil {
		s.shares = len(ctx.ScreenShares.List())
	}
	return s
}

// privacySubscriptions are the change feeds of the sources there are.
func privacySubscriptions(ctx ModuleContext, life context.Context) []subscription {
	var subs []subscription
	if ctx.Pulse != nil {
		subs = append(subs, func() (<-chan struct{}, func()) {
			ticks, stop, err := ctx.Pulse.Subscribe(life)
			if err != nil {
				log.Printf("privacy: audio: %v", err)
				return nil, func() {}
			}
			return ticks, stop
		})
	}
	if ctx.Cameras != nil {
		subs = append(subs, ctx.Cameras.Subscribe)
	}
	if ctx.ScreenShares != nil {
		subs = append(subs, ctx.ScreenShares.Subscribe)
	}
	return subs
}

// privacyModule is the privacy indicator: hidden while nothing records,
// otherwise an icon for each of microphone, camera and screen in use.
// It waits on the sources' change feeds and polls nothing.
type privacyModule struct {
	ctx                 ModuleContext
	btn                 *barButton
	mic, camera, screen *widget.Icon
	chrome              moduleChrome
	state               privacyState
	stop                func()
}

func newPrivacy(ctx ModuleContext) (Module, error) {
	cfg := ctx.Config.Privacy
	m := &privacyModule{ctx: ctx}
	icons := widget.NewBox(widget.Row, 2, 0)
	icons.AddClass("privacy-icons")
	for _, slot := range []struct {
		icon  **widget.Icon
		name  string
		class string
	}{{&m.mic, cfg.Icon, "privacy-mic"}, {&m.camera, cfg.CameraIcon, "privacy-camera"}, {&m.screen, cfg.ScreenIcon, "privacy-screen"}} {
		icon := widget.NewThemeIcon(slot.name, iconPx)
		icon.AddClass("bar-button-icon", slot.class)
		icon.SetVisible(false)
		icons.Append(icon, false)
		*slot.icon = icon
	}
	icons.SetVisible(cfg.IconShow)
	m.btn = newBarButtonAround(ctx, icons)
	life, stop := context.WithCancel(context.Background())
	m.stop = stop
	m.apply(readPrivacy(ctx))
	for _, sub := range privacySubscriptions(ctx, life) {
		followTicks(life, sub, func() {
			s := readPrivacy(ctx)
			ctx.Invoke(func() { m.apply(s) })
		})
	}
	return m, nil
}

// apply shows the state.
func (m *privacyModule) apply(s privacyState) {
	m.state = s
	m.mic.SetVisible(len(s.mics) > 0)
	m.camera.SetVisible(len(s.cameras) > 0)
	m.screen.SetVisible(s.shares > 0)
	label := ""
	if s.apps() > 0 {
		label = strconv.Itoa(s.apps())
	}
	m.btn.SetLabel(label)
	m.showChrome()
}

func (m *privacyModule) showChrome() {
	if m.chrome != nil {
		m.chrome.SetVisible(m.state.apps() > 0)
	}
}

func (m *privacyModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.showChrome()
}

// Root is the indicator's button.
func (m *privacyModule) Root() widget.Widget { return m.btn }

// Stop stops following the sources.
func (m *privacyModule) Stop() { m.stop() }

// privacyView is the privacy dropdown: a row per app recording the
// microphone, with a button that mutes that stream alone; per process
// holding a camera, with a button that ends it; and per screen share,
// with its Stop.
type privacyView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	pages *widget.Stack
	list  *widget.ItemGroup

	*openFollow
	// signal ends a camera's process (syscall.Kill; a test seam).
	signal func(pid int, sig syscall.Signal) error
}

func privacyDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &privacyView{ctx: ctx, font: font, px: px, signal: syscall.Kill}
	v.openFollow = newOpenFollow(v.follow)
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "privacy-dropdown")
	icon := ctx.Config.Privacy.Icon
	v.Append(dropdownHeader(font, icon, i18n.T("dropdown-privacy-title")), false)
	card, list := itemCard()
	card.AddClass("privacy-dropdown-list")
	v.list = list
	v.pages = pageStack()
	v.pages.Add("apps", growingScroll(scrolledCard(card, "")))
	v.pages.Add("empty", widgets.EmptyState(font, px, icon, i18n.T("dropdown-privacy-empty"), ""))
	content := dropdownContent()
	content.Append(v.pages, true)
	v.Append(content, true)
	v.startFollowing()
	return v
}

// rebuild lists what records now.
func (v *privacyView) rebuild() {
	s := readPrivacy(v.ctx)
	v.list.Clear()
	if s.apps() == 0 {
		v.pages.Show("empty")
		return
	}
	cfg := v.ctx.Config.Privacy
	for _, u := range s.mics {
		key, muted := u.Stream, u.Muted
		label := "dropdown-privacy-mute"
		if muted {
			label = "dropdown-privacy-unmute"
		}
		v.list.Append(v.row(u.Name, i18n.T("dropdown-privacy-mic", i18n.Str("source", u.Source)), cfg.Icon, i18n.T(label), func() {
			go func() {
				if err := v.ctx.Pulse.SetStreamMute(context.Background(), key, !muted); err != nil {
					log.Printf("privacy: mute %s: %v", u.Name, err)
				}
			}()
		}))
	}
	for _, c := range s.cameras {
		pid := c.PID
		v.list.Append(v.row(c.Name, i18n.T("dropdown-privacy-camera", i18n.Str("device", c.Device), i18n.Str("pid", strconv.Itoa(pid))), cfg.CameraIcon, i18n.T("dropdown-privacy-end"), func() {
			if err := v.signal(pid, syscall.SIGTERM); err != nil {
				log.Printf("privacy: end %d: %v", pid, err)
			}
		}))
	}
	if s.shares > 0 {
		for _, sh := range v.ctx.ScreenShares.List() {
			session := sh.Session
			v.list.Append(v.row(shareApp(sh), shareTargets(sh), cfg.ScreenIcon, i18n.T("dropdown-screen-share-stop"), func() {
				go func() {
					if err := v.ctx.ScreenShares.Stop(context.Background(), session); err != nil {
						log.Printf("privacy: %v", err)
					}
				}()
			}))
		}
	}
	v.pages.Show("apps")
}

// row is one app: its name over what it uses, and its action.
func (v *privacyView) row(title, subtitle, icon, action string, run func()) *widget.Item {
	row := widget.NewItem(v.font, title, subtitle)
	row.SetSmall(true)
	row.SetEllipsize(true)
	row.AddClass("privacy-dropdown-row")
	row.SetMedia(widget.NewThemeIcon(icon, itemIconPx), false)
	row.AddAction(dropdownButton(widget.NewLabel(v.font, v.px, action, 0), run, "privacy-action", css.DestructiveAction, css.Small))
	return row
}

func (v *privacyView) follow(life context.Context) {
	v.rebuild()
	for _, sub := range privacySubscriptions(v.ctx, life) {
		followTicks(life, sub, func() { v.ctx.Invoke(v.rebuild) })
	}
}
