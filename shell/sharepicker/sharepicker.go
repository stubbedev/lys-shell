// Package sharepicker is the screencast source picker
// (crates/lys-shell/src/shell/share_picker) the portal pops over
// dev.stubbe.lys.SharePicker1: a layer surface with Windows, Outputs, and
// Region pages. A card or region confirms at once in single-select
// mode; in multi-select mode cards toggle into a pending set the Share
// button confirms. The reply is the XDPH selection suffix -
// `[r]/window:<id>`, `/screen:<name>`, `/region:<out>@x,y,w,h`, several
// payloads joined by `;` - or empty on cancel.
package sharepicker

import (
	"fmt"
	"image"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/shell/dialogqueue"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/reveal"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// escapeKeycode is the evdev code of Escape.
const escapeKeycode = 1

// spinnerPx sizes the cards' loading spinner.
const spinnerPx = 24

// The page names, which are also the notebook tab labels.
const (
	pageWindows = "Windows"
	pageOutputs = "Outputs"
	pageRegion  = "Region"
)

// Deps are the picker's collaborators.
type Deps struct {
	// Config is the live config: each request reads [share-picker],
	// the styling scale, and the animations afresh.
	Config func() *config.Config
	// Font shapes the labels; Sheet styles the surface.
	Font  render.Font
	Sheet *widget.Stylesheet
	// Describe names the app asking to share (desktopentry.Describe);
	// nil for a request no portal app makes (the XDPH plugin).
	Describe func(appID string) (name, icon string)
}

// Picker is the share picker component. Pick is its entry point; the
// rest runs on the lys-ui loop goroutine.
type Picker struct {
	app    *app.Application
	d      Deps
	region interface {
		Request(frames map[string]render.StillImage) (regionoverlay.Selection, bool)
		Cancel()
	}

	// cfg, anims, and scale are the request's config snapshot
	// (PickerConfig::from_config).
	cfg   config.SharePickerConfig
	anims config.AnimationsConfig
	scale float64

	reply      chan string
	queue      dialogqueue.Queue
	requester  string
	selecting  bool
	allowToken bool
	multiple   bool
	pending    []string
	root       widget.Widget
	rev        *widget.Revealer
	layer      *app.LayerWindow
	confirm    *widget.Button
	confirmLbl *widget.Label

	// invoke marshals onto the loop (Application.Invoke).
	invoke func(func())
	// windows and outputs stand in for the compositor queries in
	// tests.
	windows func() []sharepreview.Toplevel
	outputs func() []outputInfo
}

// New builds the picker on the application; region is the live region
// overlay the Region page opens.
func New(a *app.Application, d Deps, region *regionoverlay.Overlay) *Picker {
	return &Picker{app: a, d: d, region: region, invoke: a.Invoke}
}

// Pick shows the picker for one portal request and blocks until the
// user selects or cancels. The request's window list is the raw
// XDPH_WINDOW_SHARING_LIST; its sources are the pages shown; its token
// flag seeds the restore-token box; multiple enables multi-select. A
// request arriving while one is open waits its turn. Call it from any
// goroutine but the loop's.
func (p *Picker) Pick(c portaldialogs.Caller, r Request) string {
	ch := make(chan string, 1)
	toplevels := sharepreview.ParseList(r.WindowList)
	p.invoke(func() {
		p.queue.Enqueue(c.Handle, func() { p.show(c, ch, toplevels, r) }, func() { ch <- "" })
	})
	return <-ch
}

// Dismiss cancels the request at handle: the picker showing for it,
// with the region overlay it opened, is taken down; a waiting one never
// shows. Another request's picker stays.
func (p *Picker) Dismiss(handle string) {
	p.invoke(func() {
		if !p.queue.Dismiss(handle) {
			return
		}
		if p.selecting {
			p.region.Cancel()
		}
		p.answer("")
	})
}

// show re-reads the config and maps the surface for one request with
// its enter transition.
func (p *Picker) show(c portaldialogs.Caller, ch chan string, toplevels []sharepreview.Toplevel, r Request) {
	p.snapshot()
	p.reply, p.allowToken, p.multiple, p.pending = ch, r.AllowToken, r.Multiple, nil
	p.requester = p.describe(c)
	p.root = p.build(toplevels, r.Sources)
	if err := p.mapSurface(); err != nil {
		log.Printf("share picker: %v", err)
		p.answer("")
		return
	}
	reveal.Show(p.rev, p.anims, config.AnimSharePicker)
}

// describe is the line naming the app a request comes from, "" without
// a describer.
func (p *Picker) describe(c portaldialogs.Caller) string {
	if p.d.Describe == nil {
		return ""
	}
	name, _ := p.d.Describe(c.AppID)
	return name + " wants to share your screen"
}

// snapshot reads the request's config.
func (p *Picker) snapshot() {
	cfg := p.d.Config()
	p.cfg, p.anims, p.scale = cfg.SharePicker, cfg.Animations, float64(cfg.Styling.Scale)
}

// px resolves a share-picker size against its rem base at the styling
// scale, truncated as the Rust `as i32`.
func (p *Picker) px(s config.Size, baseRem float64) int {
	return int(s.ResolvePx(baseRem*styling.RemBase, p.scale))
}

func (p *Picker) width() int  { return p.px(p.cfg.Width, config.SharePickerWidthBaseRem) }
func (p *Picker) height() int { return p.px(p.cfg.Height, config.SharePickerHeightBaseRem) }
func (p *Picker) widgetSize() int {
	return p.px(p.cfg.WidgetSize, config.SharePickerWidgetBaseRem)
}

// mapSurface maps the overlay: unanchored (centered by the compositor)
// and sized by the surface's size request.
func (p *Picker) mapSurface() error {
	layer, err := p.app.NewLayer(app.LayerConfig{
		Layer:         app.LayerOverlay,
		ExclusiveZone: -1,
		Keyboard:      app.KeyboardOnDemand,
		Namespace:     "lys-shell-share-picker",
		Root:          p.root,
		OnKey: func(_ *widget.Router, keycode uint32, _ app.Mods) {
			if keycode == escapeKeycode {
				p.answer("")
			}
		},
	})
	if err != nil {
		return err
	}
	p.layer = layer
	return nil
}

// hide unmaps the surface at once (the Region page steps aside while
// the overlay is up).
func (p *Picker) hide() {
	if p.layer != nil {
		p.layer.Close()
		p.layer = nil
	}
}

// dismiss plays the exit transition, then unmaps (hide_animated).
func (p *Picker) dismiss() {
	layer := p.layer
	if layer == nil || p.rev == nil {
		p.hide()
		return
	}
	p.layer = nil
	reveal.Hide(p.rev, p.anims, config.AnimSharePicker, layer.Close)
}

// answer replies to the open request (once), dismisses the surface,
// and shows the next request waiting.
func (p *Picker) answer(s string) {
	if p.reply == nil {
		p.dismiss()
		return
	}
	p.reply <- s
	p.reply = nil
	p.dismiss()
	p.queue.Done()
}

// prefix is the reply's flag segment: `r` asks the portal for a
// restore token.
func (p *Picker) prefix() string {
	if p.allowToken {
		return "r"
	}
	return ""
}

// selectPayload handles a card or region: in single-select mode it
// confirms at once; in multi-select mode it toggles the payload in the
// pending set.
func (p *Picker) selectPayload(payload string) {
	if !p.multiple {
		p.answer(p.prefix() + "/" + payload)
		return
	}
	if i := slices.Index(p.pending, payload); i >= 0 {
		p.pending = slices.Delete(p.pending, i, i+1)
	} else {
		p.pending = append(p.pending, payload)
	}
	if p.confirm != nil {
		p.confirm.SetEnabled(len(p.pending) > 0)
		p.confirmLbl.SetText(confirmLabel(len(p.pending)))
	}
}

// confirmPending answers the multi-select set, joined by the `;` the
// portal's multi-payload parser expects. An empty set does nothing.
func (p *Picker) confirmPending() {
	if len(p.pending) == 0 {
		return
	}
	p.answer(p.prefix() + "/" + strings.Join(p.pending, ";"))
}

// confirmLabel is the Share button's label for the pending count.
func confirmLabel(n int) string {
	if n <= 1 {
		return "Share"
	}
	return fmt.Sprintf("Share %d sources", n)
}

// windowPayload names a window card's selection: the stable ext
// identifier when the entry has one (a consumer owning its capture can
// re-resolve it), else the XDPH id only XDPH resolves.
func windowPayload(tl sharepreview.Toplevel) string {
	id := tl.Identifier
	if id == "" {
		id = strconv.FormatUint(tl.ID, 10)
	}
	return Target{Kind: TargetWindow, Name: id}.Payload()
}

// regionPayload is a region selection's payload.
func regionPayload(sel regionoverlay.Selection) string {
	return Target{Kind: TargetRegion, Name: sel.Output, X: intconv.Clamp[int32](sel.X), Y: intconv.Clamp[int32](sel.Y), Width: intconv.Clamp[int32](sel.Width), Height: intconv.Clamp[int32](sel.Height)}.Payload()
}

// build assembles the surface (the view!):
//
//	window.background.share-picker-window
//	└ revealer
//	  └ box.share-picker-surface (vertical, the configured size request)
//	    ├ notebook.share-picker-notebook (the pages of the sources
//	    │   offered: Windows, Outputs, Region)
//	    ├ checkbutton.share-picker-restore-button > label
//	    └ button.share-picker-confirm-button (multi-select only)
//
// The configured default page opens when it is offered, else the first
// page offered.
func (p *Picker) build(toplevels []sharepreview.Toplevel, sources Sources) widget.Widget {
	nb := widget.NewNotebook(p.d.Font)
	nb.AddClass("share-picker-notebook")
	nb.SetVExpand(true)
	var pages []string
	for _, page := range []struct {
		name   string
		source Sources
		build  func() widget.Widget
	}{
		{pageWindows, SourceWindows, func() widget.Widget { return p.windowsPage(toplevels) }},
		{pageOutputs, SourceOutputs, p.outputsPage},
		{pageRegion, SourceRegions, p.regionPage},
	} {
		if sources&page.source != 0 {
			p.appendPage(nb, page.name, page.build())
			pages = append(pages, page.name)
		}
	}
	wanted := map[config.SharePickerPage]string{
		config.SharePickerWindows: pageWindows, config.SharePickerOutputs: pageOutputs, config.SharePickerRegion: pageRegion,
	}[p.cfg.DefaultPage]
	if !slices.Contains(pages, wanted) {
		wanted = pages[0]
	}
	nb.SelectTab(wanted)
	th := widget.Current()
	surface := widget.NewBox(widget.Column, 2*th.Spacing, 3*th.Padding)
	surface.AddClass(css.Card, "share-picker-surface")
	surface.SetSizeRequest(p.width(), p.height())
	if p.requester != "" {
		requester := p.label(p.requester)
		requester.AddClass("share-picker-requester", css.Heading)
		surface.Append(requester, false)
	}
	surface.Append(nb, true)
	if !p.cfg.HideTokenRestore {
		check := widget.NewCheckButton(p.allowToken)
		check.AddClass("share-picker-restore-button")
		hint := p.label("Allow a restore token")
		hint.AddClass(css.DimLabel)
		check.SetChild(hint)
		check.OnChanged = func(on bool) { p.allowToken = on }
		surface.Append(check, false)
	}
	p.confirm, p.confirmLbl = nil, nil
	if p.multiple {
		p.confirmLbl = p.label(confirmLabel(0))
		p.confirmLbl.SetAlignment(render.AlignCenter)
		p.confirm = widget.NewButton(p.confirmLbl, 0, 0)
		p.confirm.AddClass(css.SuggestedAction, "share-picker-confirm-button")
		p.confirm.SetEnabled(false)
		p.confirm.OnClick = p.confirmPending
		surface.Append(p.confirm, false)
	}
	p.rev = widget.NewRevealer(surface)
	root := widget.NewBox(widget.Column, 0, 0)
	root.SetElement("window")
	root.AddClass("background", "share-picker-window")
	if p.d.Sheet != nil {
		root.AttachStylesheet(p.d.Sheet)
	}
	root.Append(p.rev, true)
	return root
}

// label is a line at the body size, scaled.
func (p *Picker) label(text string) *widget.Label {
	return widget.NewLabel(p.d.Font, widgets.TextPx(styling.Body, p.scale), text, 0)
}

// page is a notebook page: the content in a .share-picker-page scroll
// that scrolls vertically only, so its content takes the page's width.
func page(content widget.Widget) *widget.Scroll {
	sc := widget.NewScroll(content)
	sc.AddClass("share-picker-page")
	sc.FillX, sc.FillY = true, true
	sc.VerticalOnly = true
	return sc
}

// placeholder is an empty page's lys-ui empty state, centered in the
// page.
func (p *Picker) placeholder(icon, text string) widget.Widget {
	empty := widgets.EmptyState(p.d.Font, widgets.TextPx(styling.Body, p.scale), icon, text, "")
	empty.AddClass("share-picker-placeholder")
	empty.SetHExpand(true)
	empty.SetVExpand(true)
	return page(widgets.Center(empty))
}

// appendPage adds a page under its page_label: a centered
// .share-picker-tab-label filling the tab, the pointer over all of it.
func (p *Picker) appendPage(nb *widget.Notebook, name string, page widget.Widget) {
	l := p.label(name)
	l.AddClass("share-picker-tab-label")
	l.SetHExpand(true)
	l.SetAlignment(render.AlignCenter)
	l.SetCursorName("pointer")
	nb.AppendTab(name, page)
	nb.SetTabLabel(name, l)
}

// card is a source card: lys-ui's card at the theme's padding and half
// its spacing, loading until its preview lands.
func (p *Picker) card() *widget.Box {
	th := widget.Current()
	card := widget.NewBox(widget.Column, th.Spacing/2, th.Padding)
	card.AddClass(css.Card, "share-picker-card", "share-picker-card-loading")
	return card
}

// cardLabel is a card's one-line caption (max-width-chars 1: it takes
// the card's width and ellipsizes).
func (p *Picker) cardLabel(text string, classes ...string) *widget.Label {
	l := p.label(text)
	l.AddClass(classes...)
	l.SetEllipsize(widget.EllipsizeEnd)
	l.SetMaxWidthChars(1)
	return l
}

// windowsPage lays the window cards out in rows, as the homogeneous
// FlowBox does. The XDPH list feeds it; without one, the generic ext
// enumeration does. Cards run in reverse list order, as the Rust
// FlowBox inserted each at the front.
func (p *Picker) windowsPage(toplevels []sharepreview.Toplevel) widget.Widget {
	if len(toplevels) == 0 {
		enumerate := p.windows
		if enumerate == nil {
			enumerate = fallbackToplevels
		}
		toplevels = enumerate()
	}
	if len(toplevels) == 0 {
		return p.placeholder("ld-app-window-symbolic", "No windows available")
	}
	spacing := p.px(p.cfg.WindowsSpacing, config.SharePickerWindowsSpacingBaseRem)
	cols := gridColumns(len(toplevels), int(p.cfg.WindowsMinPerRow), int(p.cfg.WindowsMaxPerRow))
	grid := widget.NewBox(widget.Column, spacing, 0)
	grid.SetElement("flowbox")
	var row *widget.Box
	for i := range toplevels {
		tl := toplevels[len(toplevels)-1-i]
		if i%cols == 0 {
			row = widget.NewBox(widget.Row, spacing, 0)
			row.SetHomogeneous(true)
			grid.Append(row, false)
		}
		row.Append(p.windowCard(tl), true)
	}
	return page(grid)
}

// windowCard is build_window_card: lys-ui's card around the preview (a
// spinner over it until the capture lands), the title (the class when
// the title is blank), and the class as a caption, the whole card
// clickable.
func (p *Picker) windowCard(tl sharepreview.Toplevel) widget.Widget {
	preview := widget.NewImage(nil)
	preview.AddClass("share-picker-image")
	preview.SetScale(widget.ImageFit)
	preview.SetSizeRequest(0, p.widgetSize())
	preview.SetVExpand(true)
	preview.SetVAlign(widget.AlignCenter)
	spinner := widget.NewSpinner(spinnerPx)
	spinner.SetSpinning(true)
	shot := widget.NewOverlay().Append(preview).Append(widgets.Center(spinner))
	card := p.card()
	card.SetVAlign(widget.AlignStart)
	card.Append(shot, false)
	card.Append(p.cardLabel(cardTitle(tl), "share-picker-image-label"), false)
	card.Append(p.cardLabel(tl.Class, "share-picker-image-class-label", css.Caption, css.DimLabel), false)
	child := widget.NewBox(widget.Column, 0, 0)
	child.SetElement("flowboxchild")
	child.Append(card, false)
	child.SetTooltip(tl.Title + "\n" + tl.Class)
	child.SetCursorName("pointer")
	payload := windowPayload(tl)
	child.SetOnClickWithin(func() { p.selectPayload(payload) })
	p.loadPreview(preview, card, spinner, func(size int) (*image.RGBA, error) { return windowThumb(tl, size) })
	return child
}

// cardTitle is a window card's title line: the title, or the class
// when the title is blank.
func cardTitle(tl sharepreview.Toplevel) string {
	if strings.TrimSpace(tl.Title) == "" {
		return tl.Class
	}
	return tl.Title
}

// loadPreview is update_frame_lazily: the capture runs off the loop and
// lands back on it; the spinner goes and the card drops its loading
// class either way. grab gets the request's resize size, read on the
// loop since the next request re-snapshots the config; the loop's
// invoke is read here too, not from the capture goroutine.
func (p *Picker) loadPreview(preview *widget.Image, card *widget.Box, spinner *widget.Spinner, grab func(size int) (*image.RGBA, error)) {
	size, invoke := int(p.cfg.ResizeSize), p.invoke
	go func() {
		img, err := grab(size)
		invoke(func() {
			if spinner != nil {
				spinner.SetVisible(false)
			}
			card.RemoveClass("share-picker-card-loading")
			if err != nil {
				log.Printf("share picker: preview: %v", err)
				return
			}
			preview.SetImage(img)
		})
	}()
}

// outputsPage maps the outputs as the compositor lays them out.
func (p *Picker) outputsPage() widget.Widget {
	list := p.outputs
	if list == nil {
		list = func() []outputInfo {
			c, err := capture.Connect()
			if err != nil {
				log.Printf("share picker: outputs: %v", err)
				return nil
			}
			defer func() { _ = c.Close() }()
			return outputInfos(c.Outputs())
		}
	}
	infos := list()
	if len(infos) == 0 {
		return p.placeholder("ld-monitor-symbolic", "No outputs available")
	}
	if p.cfg.OutputsRespectScaling {
		applyScaling(infos)
	}
	slots := make([]outputSlot, len(infos))
	for i, o := range infos {
		slots[i] = outputSlot{x: o.x, y: o.y, width: o.width, height: o.height}
	}
	m := &outputMap{
		area:    newMonitorArea(slots),
		slots:   slots,
		spacing: p.px(p.cfg.OutputsSpacing, config.SharePickerOutputsSpacingBaseRem),
	}
	for _, o := range infos {
		m.cards = append(m.cards, p.outputCard(o))
	}
	return page(m)
}

// outputCard is build_output_card: a button around the card with the
// output's frame filling it, and the connector name when configured.
func (p *Picker) outputCard(o outputInfo) widget.Widget {
	preview := widget.NewImage(nil)
	preview.AddClass("share-picker-image")
	preview.SetScale(widget.ImageStretch)
	preview.SetVExpand(true)
	card := p.card()
	card.Append(preview, true)
	if p.cfg.OutputsShowLabel {
		card.Append(p.cardLabel(o.name, "share-picker-image-label"), false)
	}
	button := widget.NewButton(card, 0, 0)
	button.AddClass(css.Flat, "share-picker-output-button")
	button.SetCursorName("pointer")
	payload := Target{Kind: TargetOutput, Name: o.name}.Payload()
	button.OnClick = func() { p.selectPayload(payload) }
	p.loadPreview(preview, card, nil, func(size int) (*image.RGBA, error) { return outputThumb(o.name, size) })
	return button
}

// regionPage is build_region_page: the Select region button. The picker
// steps aside while the live overlay is up, then comes back to take the
// selection (or to stay open after a cancel).
func (p *Picker) regionPage() widget.Widget {
	lbl := p.label("Select region")
	lbl.SetAlignment(render.AlignCenter)
	btn := widget.NewButton(lbl, 0, 0)
	btn.AddClass(css.SuggestedAction, "share-picker-region-button")
	btn.SetCursorName("pointer")
	btn.OnClick = func() {
		p.hide()
		p.selecting = true
		go func() {
			sel, ok := p.region.Request(nil)
			p.invoke(func() {
				p.selecting = false
				if p.reply == nil {
					return // the request was answered meanwhile
				}
				if err := p.mapSurface(); err != nil {
					log.Printf("share picker: %v", err)
				}
				if ok {
					p.selectPayload(regionPayload(sel))
				}
			})
		}()
	}
	box := widget.NewBox(widget.Column, 0, 0)
	box.AddClass("share-picker-page")
	box.Append(btn, false)
	return page(widgets.Center(box))
}
