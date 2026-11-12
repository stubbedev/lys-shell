package bar

import (
	"context"
	"fmt"
	"log"
	"math"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-desktop/service/upower"
	"github.com/stubbedev/lys-desktop/settingspage"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/spawn"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

// dashboardPoll paces what has no change signal while the dashboard is
// open: system stats, network rates, and the media position.
const dashboardPoll = time.Second

const dashboardDiskPolls = 30

const dashboardReadTimeout = 2 * time.Second

// dashboardArtInstances numbers the media art's per-instance class
// (media_section's dashboard-media-art-instance-N); the stylesheet
// hangs the cover's background-image off it per section.
var dashboardArtInstances atomic.Uint64

// dashboardView is the dashboard dropdown (dropdowns/dashboard): quick
// actions, the volume control, now playing, battery and network, the
// system rings, and the user session, following their services while
// open.
type dashboardView struct {
	widget.Base
	ctx  ModuleContext
	font render.Font
	px   float64

	root       *widget.Box
	scroll     *widget.Scroll
	quick      *widget.Grid
	refreshers []func(dashboardReadings)
	pollers    []func() func()
	closers    []func()
	*openFollow

	refreshes atomic.Int64
	pollMu    sync.Mutex
	diskIn    int
}

type dashboardReadings struct {
	network   network.Snapshot
	networkOK bool
	profiles  powerprofiles.Snapshot
	battery   upower.Device
	batteryOK bool
}

func (v *dashboardView) read() dashboardReadings {
	ctx, cancel := context.WithTimeout(context.Background(), dashboardReadTimeout)
	defer cancel()
	var r dashboardReadings
	var err error
	if v.ctx.Network != nil {
		if r.network, err = v.ctx.Network.Read(ctx); err != nil {
			log.Printf("dashboard: read network: %v", err)
		}
		r.networkOK = err == nil
	}
	if v.ctx.PowerProfiles != nil {
		if r.profiles, err = v.ctx.PowerProfiles.Read(ctx); err != nil {
			log.Printf("dashboard: read power profile: %v", err)
			r.profiles.Available = false
		}
	}
	if v.ctx.Battery != nil {
		if r.battery, err = v.ctx.Battery.Read(ctx); err != nil {
			log.Printf("dashboard: read battery: %v", err)
		}
		r.batteryOK = err == nil
	}
	return r
}

func dashboardDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &dashboardView{ctx: ctx, font: font, px: px}
	v.openFollow = newOpenFollow(ctx, v.follow)
	v.root = widget.NewBox(widget.Column, 0, 0)
	v.root.AddClass("dropdown", "dashboard-dropdown")
	v.root.Append(v.header(), false)
	// The content's inset sits outside the scroll: .dropdown-content >
	// .dashboard-scroll > .dashboard-content-box (dashboard/mod.rs).
	content := dropdownContent()
	sections := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	sections.AddClass("dashboard-content-box")
	sections.Append(v.quickActions(), false)
	// The volume and media cards are always present; their no-device
	// and empty states live inside (controls/mod.rs, media_section).
	sections.Append(v.controls(), false)
	sections.Append(v.media(), false)
	sections.Append(v.infoRow(), false)
	sections.Append(v.systemStats(), false)
	sections.Append(userSessionSection(ctx), false)
	v.scroll = growingScroll(dropdownScroll(sections, "dashboard-scroll"))
	content.Append(v.scroll, true)
	v.root.Append(content, true)
	v.apply(dashboardReadings{})
	v.startFollowing()
	return v
}

func (v *dashboardView) Measure(axis widget.Axis, forSize int) widget.Sizes {
	widget.SetParents(v, v.root)
	return v.root.Measure(axis, forSize)
}

func (v *dashboardView) Arrange(r render.Rect) {
	v.ArrangeSelf(r)
	widget.SetParents(v, v.root)
	v.root.Arrange(r)
}

func (v *dashboardView) Paint(cv *render.Canvas) { widget.PaintChild(cv, v.root) }

func (v *dashboardView) Children() []widget.Widget { return []widget.Widget{v.root} }

func (v *dashboardView) HitTest(pt widget.Point) widget.Widget { return v.root.HitTest(pt) }

// label builds the panel's labels at the dropdown's text size; their
// classes ink and size them, so no color is set here.
func (v *dashboardView) label(text string) *widget.Label {
	return widget.NewLabel(v.font, v.px, text, 0)
}

// quickActionTilePx is a quick action's square icon tile.
const quickActionTilePx = 40

// quickActionTile is a quick action's column: the icon on its square
// tile over the label, the theme's half spacing apart. The column
// centers in the button, and the tile and label center in the column,
// so the tile stays its square instead of stretching across the cell.
func quickActionTile(icon *widget.Icon, label widget.Widget) *widget.Box {
	col := widget.NewBox(widget.Column, widget.Current().Spacing/2, 0)
	col.SetHAlign(widget.AlignCenter)
	col.SetVAlign(widget.AlignCenter)
	tile := widget.NewBox(widget.Row, 0, 0)
	tile.AddClass("quick-action-icon")
	tile.SetSizeRequest(quickActionTilePx, quickActionTilePx)
	tile.AppendAligned(icon, true, widget.AlignCenter)
	col.AppendAligned(tile, false, widget.AlignCenter)
	col.AppendAligned(label, false, widget.AlignCenter)
	return col
}

// dashboardCard is a dashboard section's card: lys-ui's card, padded
// and spaced by the theme.
func dashboardCard() *widget.Box {
	th := widget.Current()
	card := widget.NewBox(widget.Column, th.Spacing, 2*th.Padding)
	card.AddClass(css.Card, "dashboard-card")
	return card
}

// card is a titled dashboardCard: the icon-led section label, then any
// extra header actions at the end.
func (v *dashboardView) card(iconName, heading string, extra ...widget.Widget) *widget.Box {
	card := dashboardCard()
	header := widget.NewBox(widget.Row, 0, 0)
	header.AddClass("card-header")
	header.AppendAligned(iconSectionLabel(v.font, iconName, heading), true, widget.AlignCenter)
	for _, w := range extra {
		header.AppendAligned(w, false, widget.AlignCenter)
	}
	card.Append(header, false)
	return card
}

// header is the DropdownHeader with the open-settings action.
func (v *dashboardView) header() widget.Widget {
	settings := dropdownButton(widget.NewThemeIcon("ld-settings-symbolic", itemIconPx), func() {
		spawn.Quiet(settingspage.Program)
	}, css.Ghost, css.Icon)
	settings.AddClass("dashboard-settings-btn")
	settings.SetTooltip(i18n.T("dropdown-dashboard-open-settings"))
	return dropdownHeader(v.font, "ld-layout-dashboard-symbolic", i18n.T("dropdown-dashboard-title"), settings)
}

// quickActionsState is what the quick actions cannot read back from a
// service: airplane mode and the radios it switched off.
var quickActionsState struct {
	sync.Mutex
	airplane, preWifi, preBT bool
}

// quickActions is quick_actions: wifi, bluetooth, airplane, do not
// disturb, idle inhibit, and power saver toggles.
func (v *dashboardView) quickActions() widget.Widget {
	card := dashboardCard()
	th := widget.Current()
	grid := widget.NewGrid(th.Spacing, th.Spacing).SetColumnHomogeneous(true).SetRowHomogeneous(true)
	grid.AddClass("quick-actions")
	card.Append(grid, false)
	v.quick = grid

	type toggle struct {
		btn  *widget.Button
		icon *widget.Icon
	}
	make1 := func(col, row int, iconName, labelID string, onClick func()) toggle {
		icon := widget.NewThemeIcon(iconName, itemIconPx)
		lbl := v.label(i18n.T(labelID))
		lbl.AddClass("quick-action-label", css.Caption)
		lbl.SetEllipsize(widget.EllipsizeEnd)
		b := dropdownButton(quickActionTile(icon, lbl), onClick, "quick-action")
		grid.Attach(b, col, row, 1, 1)
		return toggle{b, icon}
	}
	bctx := context.Background()
	var wifi, bt, airplane, dnd, idle, saver toggle
	var state struct {
		hasWifi, wifiOn, hasBT, btOn, dndOn, idleOn, hasPP, saverOn bool
	}
	wifiDev := func() bool { return v.ctx.Networking != nil && v.ctx.Networking.Wifi != nil }
	setWifi := func(on bool) {
		if !wifiDev() {
			return
		}
		v.ctx.Go(func() {
			if err := v.ctx.Networking.Wifi.SetEnabled(bctx, on); err != nil {
				log.Printf("dashboard: wifi toggle failed: %v", err)
			}
		})
	}
	setBT := func(on bool) {
		if v.ctx.Bluetooth == nil {
			return
		}
		v.ctx.Go(func() {
			var err error
			if on {
				err = v.ctx.Bluetooth.Enable(bctx)
			} else {
				err = v.ctx.Bluetooth.Disable(bctx)
			}
			if err != nil {
				log.Printf("dashboard: bluetooth toggle failed: %v", err)
			}
		})
	}
	wifi = make1(0, 0, "ld-wifi-symbolic", "dropdown-dashboard-wifi", func() { setWifi(!state.wifiOn) })
	bt = make1(1, 0, "ld-bluetooth-symbolic", "dropdown-dashboard-bluetooth", func() { setBT(!state.btOn) })
	airplane = make1(2, 0, "ld-plane-symbolic", "dropdown-dashboard-airplane", func() {
		// toggle_airplane: going on remembers and switches the radios
		// off; going off restores the ones that were on.
		s := &quickActionsState
		s.Lock()
		s.airplane = !s.airplane
		if s.airplane {
			s.preWifi, s.preBT = state.wifiOn, state.btOn
			if state.wifiOn {
				setWifi(false)
			}
			if state.btOn {
				setBT(false)
			}
		} else {
			if s.preWifi {
				setWifi(true)
			}
			if s.preBT {
				setBT(true)
			}
		}
		s.Unlock()
		v.refreshAll()
	})
	dnd = make1(0, 1, "ld-bell-symbolic", "dropdown-dashboard-dnd", func() {
		if n := v.ctx.Notifications; n != nil {
			n.SetDND(!n.DND())
		}
	})
	idle = make1(1, 1, "ld-eye-symbolic", "dropdown-dashboard-idle-inhibit", func() {
		if s := v.ctx.IdleInhibit; s != nil {
			if s.Active() {
				s.Disable()
			} else {
				s.Enable(false)
			}
		}
	})
	saver = make1(2, 1, "ld-leaf-symbolic", "dropdown-dashboard-power-saver", func() {
		if v.ctx.PowerProfiles == nil {
			return
		}
		target := powerprofiles.ProfilePowerSaver
		if state.saverOn {
			target = powerprofiles.ProfileBalanced
		}
		v.ctx.Go(func() {
			if err := v.ctx.PowerProfiles.SetActive(bctx, target); err != nil {
				log.Printf("dashboard: power profile toggle failed: %v", err)
			}
		})
	})

	v.refreshers = append(v.refreshers, func(r dashboardReadings) {
		state.hasWifi = wifiDev()
		if r.networkOK {
			state.wifiOn = r.network.WifiEnabled
		}
		if v.ctx.Bluetooth != nil {
			bs := v.ctx.Bluetooth.State()
			state.hasBT, state.btOn = bs.Available, bs.Enabled
		}
		if v.ctx.Notifications != nil {
			state.dndOn = v.ctx.Notifications.DND()
		}
		if v.ctx.IdleInhibit != nil {
			state.idleOn = v.ctx.IdleInhibit.Active()
		}
		state.hasPP = r.profiles.Available
		state.saverOn = r.profiles.Available && r.profiles.Active == powerprofiles.ProfilePowerSaver
		quickActionsState.Lock()
		air := quickActionsState.airplane
		quickActionsState.Unlock()
		apply := func(t toggle, active, sensitive bool) {
			setClass(t.btn, "active", active)
			t.btn.SetEnabled(sensitive)
		}
		apply(wifi, state.wifiOn, state.hasWifi && !air)
		wifi.icon.SetThemeName(dashboardGlyph(state.wifiOn, "ld-wifi-symbolic", "ld-wifi-off-symbolic"))
		apply(bt, state.btOn, state.hasBT && !air)
		bt.icon.SetThemeName(dashboardGlyph(state.btOn, "ld-bluetooth-symbolic", "ld-bluetooth-off-symbolic"))
		apply(airplane, air, state.hasWifi || state.hasBT)
		apply(dnd, state.dndOn, v.ctx.Notifications != nil)
		if n := v.ctx.Notifications; n != nil {
			dnd.btn.SetTooltip(focusText(n.Focus()))
		}
		dnd.icon.SetThemeName(dashboardGlyph(state.dndOn, "ld-bell-off-symbolic", "ld-bell-symbolic"))
		apply(idle, state.idleOn, true)
		apply(saver, state.saverOn, state.hasPP)
	})
	return card
}

// controls is the volume card: mute, the debounced slider, and the
// output device's name, inside the .dashboard-controls box.
func (v *dashboardView) controls() widget.Widget {
	card := v.card("ld-audio-lines-symbolic", i18n.T("dropdown-dashboard-volume"))
	th := widget.Current()
	controls := widget.NewBox(widget.Column, th.Spacing/2, 0)
	controls.AddClass("dashboard-controls")
	row := widget.NewBox(widget.Row, th.Spacing, 0)
	row.AddClass("dashboard-slider-row")
	muteIcon := widget.NewThemeIcon("ld-volume-2-symbolic", itemIconPx)
	var muted bool
	bctx := context.Background()
	mute := dropdownButton(muteIcon, func() {
		target := !muted
		v.ctx.Go(func() {
			if err := v.ctx.Pulse.SetMuted(bctx, target); err != nil {
				log.Printf("dashboard: mute toggle failed: %v", err)
			}
		})
	}, css.Ghost, css.Icon, css.Small)
	mute.AddClass("dashboard-volume-icon")
	row.AppendAligned(mute, false, widget.AlignCenter)
	slider := widgets.NewDebouncedSlider(0, v.font, v.px, 0, v.ctx.Invoke)
	slider.ValueLabel().AddClass("dashboard-slider-value", css.Numeric)
	slider.Knob.AddClass("dashboard-slider")
	slider.Write = func(pct float64) func() {
		return func() {
			if err := v.ctx.Pulse.SetVolume(bctx, pct); err != nil {
				log.Printf("dashboard: volume set failed: %v", err)
			}
		}
	}
	row.Append(slider, true)
	controls.Append(row, false)
	device := v.label(i18n.T("dropdown-dashboard-no-device"))
	device.AddClass("controls-device", css.Caption, css.DimLabel)
	device.SetEllipsize(widget.EllipsizeEnd)
	device.SetAlignment(render.AlignEnd)
	controls.Append(device, false)
	card.Append(controls, false)
	v.refreshers = append(v.refreshers, func(dashboardReadings) {
		// No service: the no-device state, like ControlsSection without
		// an AudioService.
		if v.ctx.Pulse == nil {
			row.SetEnabled(false)
			device.SetText(i18n.T("dropdown-dashboard-no-device"))
			muteIcon.SetThemeName("ld-volume-x-symbolic")
			return
		}
		sink, err := v.ctx.Pulse.DefaultSink(bctx)
		has := err == nil
		row.SetEnabled(has)
		if !has {
			device.SetText(i18n.T("dropdown-dashboard-no-device"))
			muteIcon.SetThemeName("ld-volume-x-symbolic")
			return
		}
		muted = sink.Muted
		device.SetText(sink.Description)
		slider.Set(sink.Volume.AveragePercentage())
		muteIcon.SetThemeName(dashboardGlyph(muted, "ld-volume-x-symbolic", "ld-volume-2-symbolic"))
	})
	return card
}

// media is media_section: the active player's art, track, transport,
// and progress, or the empty state when no player is active.
func (v *dashboardView) media() widget.Widget {
	var current mpris.Player
	var has bool
	bctx := context.Background()
	switchBtn := dropdownButton(widget.NewThemeIcon("ld-arrow-left-right-symbolic", itemIconPx), func() {
		// cycle_player: the next player after the active one.
		src := v.ctx.Media
		if src == nil {
			return
		}
		players := src.Players()
		if len(players) < 2 {
			return
		}
		i := 0
		for j, p := range players {
			if p.BusName == current.BusName {
				i = j
			}
		}
		if err := src.SetActive(players[(i+1)%len(players)].BusName); err != nil {
			log.Printf("dashboard: switch player failed: %v", err)
		}
	}, css.Ghost, css.Icon, css.Small)
	switchBtn.AddClass("media-switch-btn")
	card := v.card("ld-disc-3-symbolic", i18n.T("dropdown-dashboard-now-playing"), switchBtn)
	artPx := int(math.Round(v.px * 3.5))
	art := widget.NewBox(widget.Row, 0, 0)
	widgets.Pin(art, artPx, artPx)
	art.AddClass("dashboard-media-art")
	// The per-instance class the Rust section hangs its cover rule on.
	// The art itself is a cover-scaled image child rather than that
	// rule: Rust's rule names the file wayle-media downloaded, and here
	// the image loads remote art itself.
	art.AddClass("dashboard-media-art-instance-" + strconv.FormatUint(dashboardArtInstances.Add(1), 10))
	artURL := "\x00"
	title := v.label("")
	title.AddClass("media-track", css.Heading)
	artist := v.label("")
	artist.AddClass("media-artist", css.DimLabel)
	for _, l := range []*widget.Label{title, artist} {
		l.SetEllipsize(widget.EllipsizeEnd)
		// xalign 0 under a natural-width cap of one character
		// (media_section's max-width-chars pair).
		l.SetAlignment(render.AlignStart)
		l.SetMaxWidthChars(1)
	}
	th := widget.Current()
	info := widget.NewBox(widget.Column, 0, 0)
	info.AddClass("media-info")
	info.Append(title, false)
	info.Append(artist, false)
	fire := func(what string, cmd func(mpris.Source, context.Context, string) error) {
		src := v.ctx.Media
		if src == nil {
			return
		}
		bus := current.BusName
		v.ctx.Go(func() {
			if err := cmd(src, bctx, bus); err != nil {
				log.Printf("dashboard: %s failed: %v", what, err)
			}
		})
	}
	prev := dropdownButton(widget.NewThemeIcon("ld-skip-back-symbolic", itemIconPx), func() { fire("previous", mpris.Source.Previous) }, css.Ghost, css.Icon, css.Small)
	prev.AddClass("media-btn")
	playIcon := widget.NewThemeIcon("ld-play-symbolic", itemIconPx)
	play := dropdownButton(playIcon, func() { fire("play-pause", mpris.Source.PlayPause) }, css.SuggestedAction, css.Icon, css.Small)
	play.AddClass("media-btn", "play")
	next := dropdownButton(widget.NewThemeIcon("ld-skip-forward-symbolic", itemIconPx), func() { fire("next", mpris.Source.Next) }, css.Ghost, css.Icon, css.Small)
	next.AddClass("media-btn")
	controls := widget.NewBox(widget.Row, th.Spacing/2, 0)
	controls.AddClass("media-controls")
	controls.AppendAligned(prev, false, widget.AlignCenter)
	controls.AppendAligned(play, false, widget.AlignCenter)
	controls.AppendAligned(next, false, widget.AlignCenter)
	compact := widget.NewBox(widget.Row, th.Spacing, 0)
	compact.AddClass("media-compact")
	compact.AppendAligned(art, false, widget.AlignCenter)
	compact.AppendAligned(info, true, widget.AlignCenter)
	compact.AppendAligned(controls, false, widget.AlignCenter)

	seek := widgets.NewDebouncedSlider(0, nil, 0, 0, v.ctx.Invoke)
	seek.Knob.AddClass("media-seek-slider", "dashboard-slider")
	elapsed := v.label("0:00")
	elapsed.AddClass("media-time", css.Caption, css.DimLabel, css.Numeric)
	length := v.label("0:00")
	length.AddClass("media-time", css.Caption, css.DimLabel, css.Numeric)
	seek.OnCommit = func(pct float64) {
		if current.Length <= 0 {
			return
		}
		target := time.Duration(pct / 100 * float64(current.Length))
		bus := current.BusName
		v.ctx.Go(func() {
			if src := v.ctx.Media; src != nil {
				if err := src.SetPosition(bctx, bus, target); err != nil {
					log.Printf("dashboard: seek failed: %v", err)
				}
			}
		})
	}
	times := widget.NewBox(widget.Row, 0, 0)
	times.AddClass("media-progress-times")
	times.Append(elapsed, true)
	times.Append(length, false)
	progress := widget.NewBox(widget.Column, 0, 0)
	progress.AddClass("media-progress")
	progress.Append(seek, false)
	progress.Append(times, false)
	player := widget.NewBox(widget.Column, th.Spacing, 0)
	player.Append(compact, false)
	player.Append(progress, false)

	// The empty state is expand with align Center (the EmptyState
	// template); the page fills the stack and centers it.
	empty := widgets.EmptyState(v.font, v.px, "ld-music-symbolic", i18n.T("dropdown-dashboard-no-media-title"), i18n.T("dropdown-dashboard-no-media-description"))
	emptyPage := vcentered(widget.NewBox(widget.Column, 0, 0), empty)
	body := pageStack()
	body.Add("player", player)
	body.Add("empty", emptyPage)
	card.Append(body, false)

	var polledBus atomic.Value
	setPosition := func(pos time.Duration) {
		elapsed.SetText(formatMediaDuration(pos))
		seek.Set(mediaProgress(pos, current.Length))
	}
	v.refreshers = append(v.refreshers, func(dashboardReadings) {
		src := v.ctx.Media
		if src == nil {
			current, has = mpris.Player{}, false
			polledBus.Store("")
			switchBtn.SetVisible(false)
			body.Show("empty")
			seek.Set(0)
			return
		}
		current, has = src.Active()
		switchBtn.SetVisible(len(src.Players()) > 1)
		if !has {
			polledBus.Store("")
			body.Show("empty")
			seek.Set(0)
			return
		}
		polledBus.Store(current.BusName)
		body.Show("player")
		title.SetText(current.Title)
		artist.SetText(current.Artist)
		if current.ArtURL != artURL {
			artURL = current.ArtURL
			art.Clear()
			art.Append(widgets.Center(mediaArt(artURL, "ld-music-symbolic", "dashboard-media-art-placeholder", "", int(v.px*1.4))), true)
		}
		playIcon.SetThemeName(dashboardGlyph(current.State == mpris.StatePlaying, "ld-pause-symbolic", "ld-play-symbolic"))
		prev.SetEnabled(current.CanGoPrevious)
		next.SetEnabled(current.CanGoNext)
		seek.SetEnabled(current.CanSeek)
		length.SetText(lengthText(current.Length))
	})
	var shownPos time.Duration
	v.pollers = append(v.pollers, func() func() {
		src := v.ctx.Media
		bus, _ := polledBus.Load().(string)
		if src == nil || bus == "" {
			return nil
		}
		ctx, cancel := context.WithTimeout(bctx, time.Second)
		defer cancel()
		pos, err := src.Position(ctx, bus)
		if err != nil || pos == shownPos {
			return nil
		}
		shownPos = pos
		return func() { setPosition(pos) }
	})
	return card
}

// infoRow is the battery and network cards side by side; without a
// battery the network card has the row.
func (v *dashboardView) infoRow() widget.Widget {
	row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	row.AddClass("dashboard-info-row")
	if v.ctx.Battery != nil {
		row.Append(v.battery(), true)
	}
	row.Append(v.network(), true)
	return row
}

// dashboardBatteryIcon is battery_icon.
func dashboardBatteryIcon(percent float64, charging bool) string {
	switch {
	case charging:
		return "ld-battery-charging-symbolic"
	case percent > 75:
		return "ld-battery-full-symbolic"
	case percent > 50:
		return "ld-battery-medium-symbolic"
	case percent > 25:
		return "ld-battery-low-symbolic"
	}
	return "ld-battery-warning-symbolic"
}

// dashboardBatteryTime is time_remaining_label: "~1h 5m" or "~5m",
// empty without an estimate.
func dashboardBatteryTime(secs int64) string {
	if secs <= 0 {
		return ""
	}
	h, m := secs/3600, (secs%3600)/60
	if h > 0 {
		return i18n.T("dropdown-dashboard-battery-time-hm", i18n.Str("hours", strconv.FormatInt(h, 10)), i18n.Str("minutes", strconv.FormatInt(m, 10)))
	}
	return i18n.T("dropdown-dashboard-battery-time-m", i18n.Str("minutes", strconv.FormatInt(m, 10)))
}

// dashboardProfile is power_profile_label and power_profile_icon.
func dashboardProfile(active string) (label, icon string) {
	switch active {
	case powerprofiles.ProfilePowerSaver:
		return i18n.T("dropdown-dashboard-battery-profile-saver"), "ld-leaf-symbolic"
	case powerprofiles.ProfilePerformance:
		return i18n.T("dropdown-dashboard-battery-profile-performance"), "ld-zap-symbolic"
	}
	return i18n.T("dropdown-dashboard-battery-profile-balanced"), "ld-scale-symbolic"
}

// battery is battery_section: icon, percent, gauge, time left, and the
// power profile, tinted by the dashboard's battery thresholds. The card
// shows only while a battery is present (the Rust dashboard builds it
// only for a present device): a desktop's UPower display device, or
// none at all, leaves the network card the row.
func (v *dashboardView) battery() widget.Widget {
	card := v.card("ld-battery-full-symbolic", i18n.T("dropdown-dashboard-battery"))
	card.SetVisible(false)
	cfg := v.ctx.Config.Dashboard
	th := widget.Current()
	status := widget.NewBox(widget.Row, th.Spacing, 0)
	status.AddClass("battery-status")
	icon := widget.NewThemeIcon("ld-battery-full-symbolic", itemIconPx)
	icon.AddClass("battery-icon")
	percent := v.label("")
	percent.AddClass("battery-percent", css.LargeText, css.Numeric)
	status.AppendAligned(icon, false, widget.AlignCenter)
	status.AppendAligned(percent, false, widget.AlignCenter)
	card.Append(status, false)
	gauge := widget.NewProgressBar(0)
	gauge.AddClass("progress-bar")
	card.Append(gauge, false)
	remaining := v.label("")
	remaining.AddClass("battery-detail", css.Caption, css.DimLabel)
	card.Append(remaining, false)
	profileRow := widget.NewBox(widget.Row, th.Spacing/2, 0)
	profileRow.AddClass("battery-profile")
	profileIcon := widget.NewThemeIcon("ld-scale-symbolic", int(math.Round(sectionLabelPx())))
	profileIcon.AddClass("battery-profile-icon")
	profileLabel := v.label("")
	profileLabel.AddClass("battery-profile-label", css.Caption, css.DimLabel)
	profileRow.AppendAligned(profileIcon, false, widget.AlignCenter)
	profileRow.AppendAligned(profileLabel, false, widget.AlignCenter)
	card.Append(profileRow, false)
	v.refreshers = append(v.refreshers, func(r dashboardReadings) {
		dev := r.battery
		card.SetVisible(r.batteryOK && dev.IsPresent)
		if !card.Visible() {
			return
		}
		pct := dev.Percentage
		warning := pct <= float64(cfg.BatteryWarning) && pct > float64(cfg.BatteryCritical)
		critical := pct <= float64(cfg.BatteryCritical)
		// The thresholds are state classes: .battery-icon, .battery-percent,
		// and .battery-detail carry warning/critical, the gauge success,
		// warning, or error.
		icon.SetThemeName(dashboardBatteryIcon(pct, dev.State == upower.StateCharging))
		setClass(icon, "warning", warning)
		setClass(icon, "critical", critical)
		percent.SetText(strconv.FormatFloat(pct, 'f', 0, 64) + "%")
		setClass(percent, "warning", warning)
		setClass(percent, "critical", critical)
		gauge.SetValue(pct / 100)
		setClass(gauge, "success", !warning && !critical)
		setClass(gauge, "warning", warning)
		setClass(gauge, "error", critical)
		var secs int64
		switch dev.State {
		case upower.StateDischarging:
			secs = int64(dev.TimeToEmpty / time.Second)
		case upower.StateCharging:
			secs = int64(dev.TimeToFull / time.Second)
		}
		remaining.SetText(dashboardBatteryTime(secs))
		remaining.SetVisible(secs > 0)
		// battery-detail carries the thresholds like the icon and the
		// percent (battery_section/mod.rs).
		setClass(remaining, "warning", warning)
		setClass(remaining, "critical", critical)
		profileRow.SetVisible(r.profiles.Available)
		if r.profiles.Available {
			label, glyph := dashboardProfile(r.profiles.Active)
			profileLabel.SetText(label)
			profileIcon.SetThemeName(glyph)
		}
	})
	return card
}

// dashboardSpeed is format_speed: KB/s with one decimal under 1024,
// else MB/s.
func dashboardSpeed(bytesPerSec uint64) (value string, mega bool) {
	kbps := float64(bytesPerSec) / 1024
	if kbps < 1024 {
		return strconv.FormatFloat(kbps, 'f', 1, 64), false
	}
	return strconv.FormatFloat(kbps/1024, 'f', 1, 64), true
}

// network is network_section: upload and download across every
// interface, "--" while disconnected.
func (v *dashboardView) network() widget.Widget {
	card := v.card("ld-wifi-symbolic", i18n.T("dropdown-dashboard-network"))
	speeds := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	speeds.AddClass("network-speeds")
	type stat struct {
		box          *widget.Box
		value, units *widget.Label
	}
	mk := func(glyph, dir string) stat {
		s := stat{box: widget.NewBox(widget.Column, 0, 0)}
		s.box.AddClass("speed-stat", dir)
		icon := widget.NewThemeIcon(glyph, itemIconPx)
		icon.AddClass("speed-arrow")
		s.box.AppendAligned(icon, false, widget.AlignStart)
		s.value = v.label("--")
		s.value.AddClass("speed-value", css.LargeText, css.Numeric)
		s.units = v.label(i18n.T("dropdown-dashboard-network-speed-kbs"))
		s.units.AddClass("speed-unit", css.Caption, css.DimLabel)
		s.box.Append(s.value, false)
		s.box.Append(s.units, false)
		speeds.Append(s.box, true)
		return s
	}
	up, down := mk("ld-arrow-up-symbolic", "up"), mk("ld-arrow-down-symbolic", "down")
	card.Append(speeds, false)
	var prev map[string]sysinfo.NetTotals
	var prevAt time.Time
	var connected bool
	var lastRx, lastTx uint64
	unit := func(mega bool) string {
		if mega {
			return i18n.T("dropdown-dashboard-network-speed-mbs")
		}
		return i18n.T("dropdown-dashboard-network-speed-kbs")
	}
	show := func(rx, tx uint64) {
		for _, s := range []struct {
			stat
			rate uint64
		}{{up, tx}, {down, rx}} {
			value, mega := dashboardSpeed(s.rate)
			if !connected {
				value = "--"
			}
			s.value.SetText(value)
			s.units.SetText(unit(mega))
			// .speed-value.muted and .speed-stat.muted image.speed-arrow
			// dim while disconnected.
			setClass(s.value, "muted", !connected)
			setClass(s.box, "muted", !connected)
		}
	}
	v.refreshers = append(v.refreshers, func(r dashboardReadings) {
		connected = r.networkOK && dashboardConnected(r.network)
		show(lastRx, lastTx)
	})
	reader := &sysinfo.NetReader{}
	v.closers = append(v.closers, reader.Close)
	var postedRx, postedTx uint64
	v.pollers = append(v.pollers, func() func() {
		totals, err := reader.Read()
		if err != nil {
			log.Printf("dashboard: read network totals: %v", err)
			return nil
		}
		now := time.Now()
		var rx, tx uint64
		if prev != nil {
			for name, cur := range totals {
				if old, ok := prev[name]; ok {
					if r, err := sysinfo.NetRate(name, old, cur, now.Sub(prevAt).Seconds()); err == nil {
						rx += r.RxPerSec
						tx += r.TxPerSec
					}
				}
			}
		}
		prev, prevAt = totals, now
		if rx == postedRx && tx == postedTx {
			return nil
		}
		postedRx, postedTx = rx, tx
		return func() {
			lastRx, lastTx = rx, tx
			show(rx, tx)
		}
	})
	return card
}

// dashboardThresholdClass is threshold_color: the ring variant class —
// error at or past the error mark, warning at or past the warning mark,
// else success.
func dashboardThresholdClass(value, warning, errorAt float32) string {
	switch {
	case value >= errorAt:
		return "error"
	case value >= warning:
		return "warning"
	}
	return "success"
}

// systemStats is system_stats: CPU, RAM, root disk, and CPU temperature
// rings.
func (v *dashboardView) systemStats() widget.Widget {
	card := v.card("ld-activity-symbolic", i18n.T("dropdown-dashboard-system"))
	cfg := v.ctx.Config.Dashboard
	row := widget.NewBox(widget.Row, widget.Current().Spacing, 0)
	row.AddClass("system-stats-inline")
	ring := func(labelID string) *statRing {
		r := newStatRing(v.label(""))
		col := widget.NewBox(widget.Column, widget.Current().Spacing/2, 0)
		col.AddClass("stat-inline")
		col.AppendAligned(r.ring, false, widget.AlignCenter)
		lbl := v.label(i18n.T(labelID))
		lbl.AddClass("stat-label", css.Caption, css.DimLabel)
		lbl.SetAlignment(render.AlignCenter)
		col.Append(lbl, false)
		row.Append(col, true)
		return r
	}
	cpuRing, memRing, diskRing, tempRing := ring("dropdown-dashboard-cpu"), ring("dropdown-dashboard-ram"), ring("dropdown-dashboard-disk"), ring("dropdown-dashboard-temp")
	card.Append(row, false)
	usage := func(r *statRing, pct float32) {
		r.set(float64(pct/100), strconv.FormatFloat(float64(pct), 'f', 0, 32)+"%", dashboardThresholdClass(pct, cfg.UsageWarning, cfg.UsageError))
	}
	cpuReader := &sysinfo.CPUReader{Sensor: v.ctx.Config.CPU.TempSensor, OmitFrequency: true}
	memReader := &sysinfo.MemoryReader{}
	v.closers = append(v.closers, cpuReader.Close, memReader.Close)
	var posted, sampled dashboardStats
	v.pollers = append(v.pollers, func() func() {
		cpu, err := cpuReader.Read()
		if err != nil {
			log.Printf("dashboard: read cpu: %v", err)
		} else {
			sampled.cpu = roundedPercent(cpu.UsagePercent)
			if cpu.HasTemperature {
				sampled.temp = roundedPercent(cpu.TemperatureC)
			}
		}
		mem, err := memReader.Read()
		if err != nil {
			log.Printf("dashboard: read memory: %v", err)
		} else {
			pct, _ := memoryPercents(mem)
			sampled.mem = roundedPercent(pct)
		}
		if v.diskIn > 0 {
			v.diskIn--
		} else {
			v.diskIn = dashboardDiskPolls - 1
			for _, d := range sysinfo.DisksAt(dashboardRootMount) {
				sampled.disk = roundedPercent(float32(d.UsedBytes) / float32(d.TotalBytes) * 100)
			}
		}
		if sampled == posted {
			return nil
		}
		was, now := posted, sampled
		posted = sampled
		return func() {
			if now.cpu != was.cpu {
				usage(cpuRing, now.cpu.value)
			}
			if now.mem != was.mem {
				usage(memRing, now.mem.value)
			}
			if now.disk != was.disk {
				usage(diskRing, now.disk.value)
			}
			if now.temp != was.temp {
				c := now.temp.value
				tempRing.set(float64(min(max(c/100, 0), 1)), strconv.FormatFloat(float64(c), 'f', 0, 32)+"°", dashboardThresholdClass(c, cfg.TempWarning, cfg.TempError))
			}
		}
	})
	return card
}

func toggleDashboardOnLoop(application *app.Application, rt *barRuntime, monitor string, focused func() (string, bool)) error {
	preferred := ""
	if monitor == "" {
		preferred, _ = focused()
	}
	return answerOnLoop(application, fmt.Sprintf("toggle the dashboard on %q", monitor), func() error {
		return rt.ctx.Dropdowns.toggleDashboard(monitor, preferred)
	})
}

var dashboardRootMount = []string{"/"}

func dashboardGlyph(on bool, onIcon, offIcon string) string {
	if on {
		return onIcon
	}
	return offIcon
}

func (v *dashboardView) refreshAll() {
	if v.ctx.App == nil {
		v.apply(v.read())
		return
	}
	if v.refreshes.Add(1) > 1 {
		return
	}
	v.ctx.Go(func() {
		for {
			asked := v.refreshes.Load()
			readings := v.read()
			v.ctx.Invoke(func() { v.apply(readings) })
			if v.refreshes.CompareAndSwap(asked, 0) {
				return
			}
		}
	})
}

func (v *dashboardView) apply(readings dashboardReadings) {
	for _, refresh := range v.refreshers {
		refresh(readings)
	}
}

func (v *dashboardView) sample() []func() {
	v.pollMu.Lock()
	defer v.pollMu.Unlock()
	var applies []func()
	for _, poll := range v.pollers {
		if apply := poll(); apply != nil {
			applies = append(applies, apply)
		}
	}
	return applies
}

func (v *dashboardView) poll() {
	applies := v.sample()
	if len(applies) == 0 {
		return
	}
	v.ctx.Invoke(func() {
		for _, apply := range applies {
			apply()
		}
	})
}

func (v *dashboardView) startPolling(life context.Context) {
	v.ctx.Go(func() {
		v.pollMu.Lock()
		v.diskIn = 0
		v.pollMu.Unlock()
		ticker := time.NewTicker(dashboardPoll)
		defer ticker.Stop()
		for life.Err() == nil {
			v.poll()
			select {
			case <-life.Done():
			case <-ticker.C:
			}
		}
	})
}

func (v *dashboardView) follow(life context.Context) {
	v.refreshAll()
	if v.ctx.App == nil {
		v.poll()
		return
	}
	v.startPolling(life)
	merged := make(chan struct{}, 1)
	add := func(ch <-chan struct{}, stop func()) {
		v.ctx.Go(func() {
			defer stop()
			for {
				select {
				case <-life.Done():
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
					select {
					case merged <- struct{}{}:
					default:
					}
				}
			}
		})
	}
	if v.ctx.Media != nil {
		add(v.ctx.Media.Subscribe())
	}
	if v.ctx.Bluetooth != nil {
		add(v.ctx.Bluetooth.Subscribe())
	}
	if v.ctx.IdleInhibit != nil {
		add(v.ctx.IdleInhibit.Changes())
	}
	if v.ctx.Notifications != nil {
		events, stop := v.ctx.Notifications.Subscribe()
		ticks := make(chan struct{}, 1)
		v.ctx.Go(func() {
			for range events {
				select {
				case ticks <- struct{}{}:
				default:
				}
			}
		})
		add(ticks, stop)
	}
	subs := []sourceSubscription{subscribeOf(v.ctx.Network), subscribeOf(v.ctx.PowerProfiles), subscribeOf(v.ctx.Battery)}
	if v.ctx.Pulse != nil {
		subs = append(subs, infallibleStop(v.ctx.Pulse.Subscribe))
	}
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		v.ctx.Go(func() {
			ch, stop, err := sub(life)
			if err != nil {
				log.Printf("dashboard: subscribe: %v", err)
				return
			}
			add(ch, func() { stopFollowing("dashboard", stop) })
		})
	}
	v.ctx.Go(func() {
		for {
			select {
			case <-life.Done():
				return
			case <-merged:
				v.refreshAll()
			}
		}
	})
}

func (v *dashboardView) dropdownFocus() widget.Widget { return v.firstControl() }

func (v *dashboardView) firstControl() widget.Widget {
	if quick := focusablesUnder(v.quick, nil); len(quick) > 0 {
		return quick[0]
	}
	if all := focusablesUnder(v.root, nil); len(all) > 0 {
		return all[0]
	}
	return nil
}

func focusablesUnder(w widget.Widget, out []widget.Widget) []widget.Widget {
	if widget.Describe(w).Focusable {
		out = append(out, w)
	}
	if c, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, kid := range c.Children() {
			out = focusablesUnder(kid, out)
		}
	}
	return out
}

func (v *dashboardView) NavigateFocus(target widget.Widget, k widget.KeyAction) widget.Widget {
	if widget.RoleOf(target) == widget.RoleSlider && k != widget.KeyUp && k != widget.KeyDown {
		return nil
	}
	switch k {
	case widget.KeyUp, widget.KeyDown, widget.KeyLeft, widget.KeyRight, widget.KeyHome, widget.KeyEnd:
	default:
		return nil
	}
	order := focusablesUnder(v.root, nil)
	if len(order) == 0 {
		return nil
	}
	var next widget.Widget
	switch k {
	case widget.KeyHome:
		next = order[0]
	case widget.KeyEnd:
		next = order[len(order)-1]
	default:
		next = nearestToward(target, order, k)
	}
	if next == nil {
		if slices.Contains(order, target) {
			return target
		}
		return nil
	}
	v.scrollTo(next)
	return next
}

func nearestToward(target widget.Widget, order []widget.Widget, k widget.KeyAction) widget.Widget {
	at, ok := target.(widget.Boundser)
	if !ok {
		return nil
	}
	from := at.Bounds()
	var best widget.Widget
	bestScore := 0
	for _, w := range order {
		if w == target {
			continue
		}
		other, ok := w.(widget.Boundser)
		if !ok {
			continue
		}
		b := other.Bounds()
		var along, across int
		switch k {
		case widget.KeyDown:
			along, across = b.Y-(from.Y+from.H), spanGap(from.X, from.W, b.X, b.W)
		case widget.KeyUp:
			along, across = from.Y-(b.Y+b.H), spanGap(from.X, from.W, b.X, b.W)
		case widget.KeyRight:
			along, across = b.X-(from.X+from.W), spanGap(from.Y, from.H, b.Y, b.H)
		default:
			along, across = from.X-(b.X+b.W), spanGap(from.Y, from.H, b.Y, b.H)
		}
		sideways := k == widget.KeyLeft || k == widget.KeyRight
		if along < 0 || (sideways && across > 0) {
			continue
		}
		if score := along + 3*across; best == nil || score < bestScore {
			best, bestScore = w, score
		}
	}
	return best
}

func spanGap(a, aLen, b, bLen int) int {
	return max(0, max(b-(a+aLen), a-(b+bLen)))
}

func (v *dashboardView) scrollTo(w widget.Widget) {
	if !slices.Contains(focusablesUnder(v.scroll, nil), w) {
		v.scroll.SetOffset(0, 0)
		return
	}
	control, ok := w.(widget.Boundser)
	if !ok {
		return
	}
	view, b := v.scroll.Bounds(), control.Bounds()
	_, y := v.scroll.Offset()
	switch {
	case b.Y < view.Y:
		v.scroll.SetOffset(0, y-(view.Y-b.Y))
	case b.Y+b.H > view.Y+view.H:
		v.scroll.SetOffset(0, y+(b.Y+b.H)-(view.Y+view.H))
	}
}

// subscriber is the Subscribe seam the network, power-profiles and
// battery sources share.
type subscriber interface {
	Subscribe(ctx context.Context) (<-chan struct{}, func() error, error)
}

func subscribeOf[T subscriber](s T) sourceSubscription {
	var zero T
	if any(s) == any(zero) {
		return nil
	}
	return s.Subscribe
}

// dropdownClosed implements dropdownCloser: stop following, and let go of
// the stat readers once a poll underway is done with them.
func (v *dashboardView) dropdownClosed() {
	v.openFollow.dropdownClosed()
	closers := v.closers
	v.closers = nil
	v.ctx.Go(func() {
		v.pollMu.Lock()
		defer v.pollMu.Unlock()
		for _, closeReader := range closers {
			closeReader()
		}
	})
}

type dashboardStat struct {
	value float32
	known bool
}

func roundedPercent(v float32) dashboardStat {
	return dashboardStat{value: float32(int(float32(v) + 0.5)), known: true}
}

type dashboardStats struct{ cpu, mem, disk, temp dashboardStat }

// dashboardConnected is the network section's resolve_connected: the
// primary connection's device, nothing without one, and either device
// for other primary types. Wifi counts as connected while enabled with
// an SSID.
func dashboardConnected(snap network.Snapshot) bool {
	wired := snap.Wired == network.StatusConnected
	wifi := snap.HasWifi && snap.WifiEnabled && snap.WifiSSID != ""
	switch snap.Primary {
	case network.ConnectionWired:
		return wired
	case network.ConnectionWifi:
		return wifi
	case network.ConnectionNone:
		return false
	}
	return wired || wifi
}
