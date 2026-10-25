package bar

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/appicons"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

// Media dropdown sizing the code owns; the strings come from locales
// dropdowns/_media.ftl (dropdown-media-*).
const (
	mediaPositionPollEvery = time.Second
)

// formatMediaDuration is helpers.rs format_duration: m:ss, or h:mm:ss
// from an hour up.
func formatMediaDuration(d time.Duration) string {
	total := int(d / time.Second)
	h, m, s := total/3600, total%3600/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// mediaProgress is helpers.rs progress_fraction as a 0..100 percent.
func mediaProgress(pos, length time.Duration) float64 {
	if length <= 0 {
		return 0
	}
	return min(max(float64(pos)/float64(length), 0), 1) * 100
}

// mediaSourceIcon is helpers.rs resolve_source_icon: the app table on
// the identity, then the bus name, then the desktop entry; else the
// entry's -symbolic name, or the music glyph without one.
func mediaSourceIcon(p mpris.Player) string {
	for _, name := range []string{p.Identity, p.BusName, p.DesktopEntry} {
		if name == "" {
			continue
		}
		if icon, ok := appicons.Lookup(name); ok {
			return icon
		}
	}
	if p.DesktopEntry != "" {
		return p.DesktopEntry + "-symbolic"
	}
	return "ld-music-symbolic"
}

// mediaPlayPauseIcon and mediaLoopIcon are player_view methods.rs's
// glyph picks.
func mediaPlayPauseIcon(state mpris.PlaybackState) string {
	if state == mpris.StatePlaying {
		return "ld-pause-symbolic"
	}
	return "ld-play-symbolic"
}

func mediaLoopIcon(mode mpris.LoopMode) string {
	if mode == mpris.LoopTrack {
		return "ld-repeat-1-symbolic"
	}
	return "ld-repeat-symbolic"
}

// orUnknown substitutes the placeholder for an empty field.
func orUnknown(value, placeholder string) string {
	if value == "" {
		return placeholder
	}
	return value
}

// mediaView is the media dropdown (dropdowns/media): the player view
// with transport controls, or the source picker, or the no-player
// empty state. It follows the service while open and stops on Close.
type mediaView struct {
	ctx ModuleContext
	src mpris.Source

	*widget.Box
	// pages slides between the main page (the player or the empty
	// state, in main) and the source picker (sources).
	pages *widget.Stack
	// main holds the player and the empty state as pages of one
	// homogeneous stack (the relm4 if/else's gtk::Stack), so the card
	// keeps the player's height with no player.
	main    *widget.Stack
	sources *widget.Box
	player  *widget.Box
	empty   *widget.StatusPage
	mode    string

	identity   *widget.Label
	sourceIcon *widget.Icon
	art        *widget.Box
	artURL     string
	title      *widget.Label
	artist     *widget.Label
	album      *widget.Label
	seek       *widgets.DebouncedSlider
	position   *widget.Label
	length     *widget.Label
	shuffle    *widget.Button
	previous   *widget.Button
	playPause  *widget.Button
	playIcon   *widget.Icon
	next       *widget.Button
	loop       *widget.Button
	loopIcon   *widget.Icon
	// check is the active row's tb-check glyph in the source picker
	// (buttons hide their subtrees from the tree walks, so the view
	// carries it, like every other stateful widget here).
	check *widget.Icon

	current mpris.Player
	has     bool
	// seekBus is the player the seek knob last followed: a switch or a
	// clear resets it, the way methods.rs refresh_from_player and
	// clear_fields re-set the slider from the (new) player's progress.
	seekBus string

	once sync.Once
	stop chan struct{}
}

// mediaDropdown builds the live media card.
func mediaDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	if ctx.Media == nil {
		return mediaEmptyState(ctx)
	}
	v := &mediaView{ctx: ctx, src: ctx.Media, stop: make(chan struct{})}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "media-dropdown")
	v.main = widget.NewStack()
	v.sources = widget.NewBox(widget.Column, 0, 0)
	v.pages = widget.NewStack()
	pageSlide(v.pages, ctx.Config)
	v.pages.Add("main", v.main)
	v.pages.Add("sources", v.sources)
	v.Append(v.pages, true)
	v.empty = mediaEmptyState(ctx)
	v.buildPlayer(font, px)
	v.main.Add("player", v.player)
	v.main.Add("empty", v.empty)
	v.refresh()
	v.follow()
	return v
}

// mediaEmptyState is the EmptyState template with the media strings.
func mediaEmptyState(ctx ModuleContext) *widget.StatusPage {
	font, px := dropdownFont(ctx)
	return widgets.EmptyState(font, px, "ld-play-symbolic",
		i18n.T("dropdown-media-no-player-title"), i18n.T("dropdown-media-no-player-description"))
}

// mediaControlPx and mediaPlayPx are the transport glyphs' sizes:
// the minor and skip buttons', and play/pause's on its accent button.
const (
	mediaControlPx = 16
	mediaPlayPx    = 20
)

// controlButton builds one transport button on lys-ui's icon buttons:
// a ghost one, or with classes beyond media-control (the accent
// play/pause) those.
func (v *mediaView) controlButton(icon *widget.Icon, onClick func(), classes ...string) *widget.Button {
	if len(classes) == 0 {
		classes = []string{css.Ghost}
	}
	b := widget.NewButton(icon, 0, 0)
	b.AddClass(append([]string{"media-control", css.Icon}, classes...)...)
	b.OnClick = onClick
	return b
}

// buildPlayer assembles the player view once; refresh fills it in: the
// dropdown header with the source button as its action, over the
// artwork, the track, the seek bar and the transport, the theme
// spacing them.
func (v *mediaView) buildPlayer(font render.Font, px float64) {
	th := widget.Current()
	v.player = widget.NewBox(widget.Column, 0, 0)

	v.sourceIcon = widget.NewThemeIcon("ld-music-symbolic", itemIconPx)
	v.sourceIcon.AddClass("media-source-icon")
	v.identity = widget.NewLabel(font, px, "", 0)
	v.identity.AddClass("media-source-name")
	v.identity.SetEllipsize(widget.EllipsizeEnd)
	source := widget.NewBox(widget.Row, th.Spacing/2, 0)
	source.AppendAligned(v.sourceIcon, false, widget.AlignCenter)
	source.AppendAligned(v.identity, false, widget.AlignCenter)
	source.AppendAligned(widget.NewThemeIcon("ld-chevron-right-symbolic", itemIconPx), false, widget.AlignCenter)
	sourceButton := dropdownButton(source, v.showPicker, "media-source-button", css.Ghost, css.Small)
	v.player.Append(dropdownHeader(font, "ld-disc-3-symbolic", i18n.T("dropdown-media-title"), sourceButton), false)

	body := dropdownContent("media-player")
	// The artwork takes the panel's leftover height (player_view
	// mod.rs:123-127).
	v.art = widget.NewBox(widget.Row, 0, 0)
	v.art.AddClass("media-artwork")
	body.Append(v.art, true)

	info := widget.NewBox(widget.Column, th.Spacing/2, 0)
	info.AddClass("media-info")
	v.title = widget.NewLabel(font, px, "", 0)
	v.title.AddClass("media-title", css.LargeText)
	v.artist = widget.NewLabel(font, px, "", 0)
	v.artist.AddClass("media-artist")
	v.album = widget.NewLabel(font, px, "", 0)
	v.album.AddClass("media-album", css.DimLabel)
	// The info rows fill the panel and ellipsize at its edge, centered
	// text (xalign 0.5) like player_view mod.rs:158-184.
	for _, l := range []*widget.Label{v.title, v.artist, v.album} {
		l.SetEllipsize(widget.EllipsizeEnd)
		l.SetAlignment(render.AlignCenter)
		info.Append(l, false)
	}
	body.Append(info, false)

	// The seek bar is a DebouncedSlider without its value label.
	v.seek = widgets.NewDebouncedSlider(0, nil, 0, 0, v.ctx.Invoke)
	v.seek.Knob.AddClass("media-seek-slider")
	v.seek.OnCommit = v.seekTo
	times := widget.NewBox(widget.Row, 0, 0)
	times.AddClass("media-progress-times")
	v.position = widget.NewLabel(font, px, "0:00", 0)
	v.length = widget.NewLabel(font, px, "0:00", 0)
	for _, l := range []*widget.Label{v.position, v.length} {
		l.AddClass("media-time", css.Caption, css.DimLabel, css.Numeric)
	}
	times.Append(v.position, true)
	times.Append(v.length, false)
	progress := widget.NewBox(widget.Column, th.Spacing/2, 0)
	progress.AddClass("media-progress")
	progress.Append(v.seek, false)
	progress.Append(times, false)
	body.Append(progress, false)

	controls := widget.NewBox(widget.Row, th.Spacing, 0)
	controls.AddClass("media-controls")
	v.shuffle = v.controlButton(widget.NewThemeIcon("ld-shuffle-symbolic", mediaControlPx), func() { v.fire("toggle shuffle", v.src.ToggleShuffle) })
	v.shuffle.AddClass("minor")
	v.previous = v.controlButton(widget.NewThemeIcon("ld-skip-back-symbolic", mediaControlPx), func() { v.fire("previous track", v.src.Previous) })
	v.playIcon = widget.NewThemeIcon("ld-play-symbolic", mediaPlayPx)
	v.playPause = v.controlButton(v.playIcon, func() { v.fire("play/pause", v.src.PlayPause) }, css.SuggestedAction, css.Large, "main")
	v.next = v.controlButton(widget.NewThemeIcon("ld-skip-forward-symbolic", mediaControlPx), func() { v.fire("next track", v.src.Next) })
	v.loopIcon = widget.NewThemeIcon("ld-repeat-symbolic", mediaControlPx)
	v.loop = v.controlButton(v.loopIcon, func() { v.fire("toggle loop", v.src.ToggleLoop) })
	v.loop.AddClass("minor")
	for _, b := range []*widget.Button{v.shuffle, v.previous, v.playPause, v.next, v.loop} {
		controls.AppendAligned(b, false, widget.AlignCenter)
	}
	body.AppendAligned(controls, false, widget.AlignCenter)
	v.player.Append(body, true)
}

// fire runs one transport command off the loop; failures log, as the
// Rust fire_player_command does.
func (v *mediaView) fire(what string, cmd func(context.Context, string) error) {
	if !v.has {
		return
	}
	bus := v.current.BusName
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cmd(ctx, bus); err != nil {
			log.Printf("media: %s failed: %v", what, err)
		}
	}()
}

// seekTo is SeekCommitted: the percent of the track length; nothing
// without a known length.
func (v *mediaView) seekTo(percent float64) {
	if !v.has || v.current.Length <= 0 {
		return
	}
	target := time.Duration(float64(v.current.Length) * percent / 100)
	v.fire("seek", func(ctx context.Context, bus string) error {
		return v.src.SetPosition(ctx, bus, target)
	})
}

// show swaps the visible main page, "player" or "empty".
func (v *mediaView) show(mode string) {
	if v.mode == mode {
		return
	}
	v.mode = mode
	v.main.Show(mode)
	v.pages.Show("main")
}

// refresh repaints from the active player (PlayerChanged plus the
// metadata, capability, state, loop, and shuffle watchers at once).
func (v *mediaView) refresh() {
	p, ok := v.src.Active()
	v.current, v.has = p, ok
	// A player switch (or clearing) retires the old knob position: the
	// slider follows the new player's progress from zero until the next
	// position read (methods.rs:164,200).
	bus := ""
	if ok {
		bus = p.BusName
	}
	if bus != v.seekBus {
		v.seekBus = bus
		v.seek.Set(0)
	}
	if v.mode == "picker" {
		v.buildPicker()
		return
	}
	if !ok {
		v.show("empty")
		return
	}
	v.show("player")
	v.identity.SetText(p.Identity)
	v.sourceIcon.SetThemeName(mediaSourceIcon(p))
	v.title.SetText(orUnknown(p.Title, i18n.T("dropdown-media-unknown-title")))
	setClass(v.title, "placeholder", p.Title == "")
	v.artist.SetText(orUnknown(p.Artist, i18n.T("dropdown-media-unknown-artist")))
	setClass(v.artist, "placeholder", p.Artist == "")
	v.album.SetText(orUnknown(p.Album, i18n.T("dropdown-media-unknown-album")))
	setClass(v.album, "placeholder", p.Album == "")
	v.setArt(p.ArtURL)
	v.length.SetText(lengthText(p.Length))
	v.seek.SetEnabled(p.CanSeek)
	v.shuffle.SetEnabled(p.CanShuffle)
	setClass(v.shuffle, "active", p.Shuffle)
	v.previous.SetEnabled(p.CanGoPrevious)
	v.next.SetEnabled(p.CanGoNext)
	v.loop.SetEnabled(p.CanLoop)
	setClass(v.loop, "active", p.Loop != mpris.LoopNone && p.Loop != mpris.LoopUnsupported)
	v.loopIcon.SetThemeName(mediaLoopIcon(p.Loop))
	v.playIcon.SetThemeName(mediaPlayPauseIcon(p.State))
}

// lengthText is the length label: 0:00 without a known length.
func lengthText(length time.Duration) string {
	if length <= 0 {
		return "0:00"
	}
	return formatMediaDuration(length)
}

// setArt swaps the cover when the art URL changes: a file:// path or
// an http(s) URL fills the panel, anything else is the disc
// placeholder centered in it (the art resolver's
// Ready/NeedsDownload/Unresolvable).
func (v *mediaView) setArt(url string) {
	if url == v.artURL && len(v.art.Children()) > 0 {
		return
	}
	v.artURL = url
	v.art.Clear()
	art := mediaArt(url, "ld-disc-3-symbolic", "media-artwork-placeholder", "media-artwork-placeholder-icon", mediaArtGlyphPx)
	if _, isImage := art.(*widget.Image); isImage {
		v.art.Append(art, true)
		return
	}
	// The placeholder tile centers in the panel (Rust halign/valign
	// center on the placeholder box).
	row := widget.NewBox(widget.Row, 0, 0)
	row.Append(widget.NewSpacer(0, 0), true)
	row.Append(art, false)
	row.Append(widget.NewSpacer(0, 0), true)
	v.art.AppendAligned(row, true, widget.AlignCenter)
}

// mediaArtGlyphPx is the placeholder disc's size on its tile.
const mediaArtGlyphPx = 48

// mediaArt is a player's cover: the art at a file or http(s) URL scaled
// to cover, else the placeholder glyph in its tile box (boxClass, whose
// rules paint the tile, with imgClass on the glyph).
func mediaArt(url, placeholder, boxClass, imgClass string, px int) widget.Widget {
	var img *widget.Image
	switch {
	case strings.HasPrefix(url, "file://"):
		img = widget.NewFileImage(strings.TrimPrefix(url, "file://"))
	case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
		img = widget.NewURLImage(url)
	}
	if img == nil {
		return iconTile(widget.NewThemeIcon(placeholder, px), imgClass, boxClass)
	}
	img.SetScale(widget.ImageCover)
	return img
}

// setPosition applies a position read.
func (v *mediaView) setPosition(pos time.Duration) {
	if !v.has {
		return
	}
	v.position.SetText(formatMediaDuration(pos))
	v.seek.Set(mediaProgress(pos, v.current.Length))
}

// showPicker is ShowSourcePicker: the list of players.
func (v *mediaView) showPicker() {
	v.mode = "picker"
	v.buildPicker()
}

// buildPicker lists every player as an Item in an item card under the
// back and title row; picking one makes it active and returns to the
// player view (source_picker/mod.rs).
func (v *mediaView) buildPicker() {
	font, px := dropdownFont(v.ctx)
	th := widget.Current()
	col := dropdownContent("media-source-picker")
	header := widget.NewBox(widget.Row, th.Spacing, 0)
	header.AddClass("picker-header")
	back := dropdownButton(widget.NewThemeIcon("ld-arrow-left-symbolic", itemIconPx), func() { v.mode = ""; v.refresh() }, "picker-back", css.Ghost, css.Icon, css.Small)
	header.AppendAligned(back, false, widget.AlignCenter)
	headerLabel := widget.NewLabel(font, px, i18n.T("dropdown-media-sources"), 0)
	headerLabel.AddClass("picker-title", css.Heading)
	header.AppendAligned(headerLabel, true, widget.AlignCenter)
	col.Append(header, false)
	card, list := itemCard()
	card.AddClass("media-source-list")
	col.Append(scrolledCard(card, "picker-body"), true)
	active, _ := v.src.Active()
	v.check = nil
	for _, p := range v.src.Players() {
		// The row shows the identity as-is (methods.rs:39).
		row := widget.NewItem(font, p.Identity, "")
		row.SetSmall(true)
		row.SetEllipsize(true)
		row.AddClass("media-source-option")
		row.SetMedia(widget.NewThemeIcon(mediaSourceIcon(p), itemIconPx), false)
		setClass(row, "selected", p.BusName == active.BusName)
		if p.BusName == active.BusName {
			v.check = widget.NewThemeIcon("tb-check-symbolic", itemIconPx)
			v.check.AddClass("media-source-option-check")
			row.AddAction(v.check)
		}
		bus := p.BusName
		row.SetOnActivate(func() {
			if err := v.src.SetActive(bus); err != nil {
				log.Printf("media: select %s: %v", bus, err)
			}
			v.mode = ""
			v.refresh()
		})
		list.Append(row)
	}
	v.sources.Clear()
	v.sources.Append(col, true)
	v.pages.Show("sources")
}

// follow keeps the view current while open: service ticks refresh,
// and the position polls once a second (MPRIS signals no position
// changes; the Rust player polls at position_poll_interval).
func (v *mediaView) follow() {
	ticks, unsubscribe := v.src.Subscribe()
	v.pollPosition()
	go func() {
		defer unsubscribe()
		ticker := time.NewTicker(mediaPositionPollEvery)
		defer ticker.Stop()
		for {
			select {
			case <-v.stop:
				return
			case <-ticks:
				v.ctx.Invoke(v.refresh)
			case <-ticker.C:
				v.pollPosition()
			}
		}
	}()
}

// pollPosition reads the position off the loop and applies it on it.
func (v *mediaView) pollPosition() {
	if v.ctx.App == nil || !v.has {
		return
	}
	bus := v.current.BusName
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		pos, err := v.src.Position(ctx, bus)
		if err != nil {
			return
		}
		v.ctx.Invoke(func() { v.setPosition(pos) })
	}()
}

// dropdownClosed implements dropdownCloser: stop following the
// service once the popover went away.
func (v *mediaView) dropdownClosed() { v.once.Do(func() { close(v.stop) }) }
