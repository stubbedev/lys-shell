package bar

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/wmipc"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/layering"
)

// dockMinimizedAlpha is the running indicator's opacity while every
// window of its app is minimized.
const dockMinimizedAlpha = 0.35

// dockApp is one dock entry: a pinned app, or an app with windows open
// on the dock's output, with those windows in the order they appeared.
type dockApp struct {
	appID   string
	pinned  bool
	windows []taskWindow
}

// dockApps is the dock's entries: the pinned apps in their order, then
// every other app with a window on output, in the order its first window
// appeared.
func dockApps(pinned []string, windows []taskWindow, output string) []dockApp {
	apps := make([]dockApp, 0, len(pinned))
	for _, id := range pinned {
		if !slices.ContainsFunc(apps, func(a dockApp) bool { return a.appID == id }) {
			apps = append(apps, dockApp{appID: id, pinned: true})
		}
	}
	for _, w := range windows {
		if !w.OnOutput(output) {
			continue
		}
		i := slices.IndexFunc(apps, func(a dockApp) bool { return a.appID == w.AppID() })
		if i < 0 {
			apps = append(apps, dockApp{appID: w.AppID()})
			i = len(apps) - 1
		}
		apps[i].windows = append(apps[i].windows, w)
	}
	return apps
}

// running reports whether the app has a window open.
func (a dockApp) running() bool { return len(a.windows) > 0 }

// minimized reports whether every window of the app is minimized.
func (a dockApp) minimized() bool {
	return a.running() && !slices.ContainsFunc(a.windows, func(w taskWindow) bool { return !w.Minimized() })
}

// target is the window a click on the app brings up: after the focused
// one, so clicks cycle through the app's windows; else the first one
// shown, else the first, which a click brings back from minimized.
func (a dockApp) target() taskWindow {
	if i := slices.IndexFunc(a.windows, func(w taskWindow) bool { return w.Activated() && !w.Minimized() }); i >= 0 {
		return a.windows[(i+1)%len(a.windows)]
	}
	if i := slices.IndexFunc(a.windows, func(w taskWindow) bool { return !w.Minimized() }); i >= 0 {
		return a.windows[i]
	}
	return a.windows[0]
}

// dockAnchor is the layer anchor of a dock on edge: that edge alone, so
// the compositor centers it along the edge.
func dockAnchor(edge config.DockEdge) app.Anchor {
	switch edge {
	case config.DockEdgeBottom:
		return app.AnchorBottom
	case config.DockEdgeLeft:
		return app.AnchorLeft
	case config.DockEdgeRight:
		return app.AnchorRight
	}
	panic(fmt.Sprintf("dock: edge %q has no anchor", edge))
}

// dockArea is where a dock of size sits on an output at out, in the
// compositor's global logical pixels: centered along its edge.
func dockArea(edge config.DockEdge, out wmipc.Rect, size widget.Size) wmipc.Rect {
	r := wmipc.Rect{Width: size.W, Height: size.H}
	switch edge {
	case config.DockEdgeBottom:
		r.X, r.Y = out.X+(out.Width-size.W)/2, out.Y+out.Height-size.H
	case config.DockEdgeLeft:
		r.X, r.Y = out.X, out.Y+(out.Height-size.H)/2
	case config.DockEdgeRight:
		r.X, r.Y = out.X+out.Width-size.W, out.Y+(out.Height-size.H)/2
	}
	return r
}

// covered reports whether a window shown on output overlaps area.
func covered(windows []wmipc.Window, output string, area wmipc.Rect) bool {
	return slices.ContainsFunc(windows, func(w wmipc.Window) bool {
		return w.Output == output && w.Stack != nil && !w.Minimized && overlap(w.Rect, area)
	})
}

func overlap(a, b wmipc.Rect) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

// dock is one output's dock:
//
//	box.dock-surface
//	├ box.dock.<edge>        (the entries; hidden while the dock hides)
//	│ └ button.task-item.dock-item[.running][.minimized]...
//	│   (the app icon and its running indicator, the app name as tooltip)
//	└ drawingarea.dock-reveal (the strip a hidden dock keeps)
//
// A pinned app launches on click; an open app's click brings up its next
// window, restoring a minimized one, and a right click lists its windows.
// Every window names its app's entry as its minimize rectangle, over the
// bar module's.
type dock struct {
	ctx     ModuleContext
	cfg     config.DockConfig
	hub     *taskHub
	output  *app.Output
	surface *dockSurface
	panel   *arrangedBox
	// slot holds the panel and nothing else: its bounds are the panel's
	// margin box, the room the resting dock takes from its edge.
	slot   *widget.Box
	reveal *widget.DrawingArea
	axis   widget.Axis
	px     int
	zoom   dockZoom
	// hovered is the app whose entry is under the pointer, empty for none.
	hovered string
	entries map[string]*dockEntry
	shown   []dockApp
	host    app.Host
	rects   *rectOwner
	launch  func(taskApp)
	// hide is the dock's reveal controller, the bar's too.
	hide *autoHide
	// preview shows an app's windows on a hover rest over its entry.
	preview *windowPreview
	// size is the panel's size as last laid out shown and unzoomed, which
	// a hidden dock keeps for what would cover it and for its reveal strip.
	size widget.Size
	// rested hears each change of size.
	rested func()
	stops  []func()
}

type dockEntry struct {
	*taskButton
	indicator *widget.DrawingArea
	app       dockApp
}

// newDock builds output's dock; after runs its delayed hides on the loop.
func newDock(ctx ModuleContext, cfg config.DockConfig, output *app.Output, after func(time.Duration, func()) func()) *dock {
	d := &dock{
		ctx:     ctx,
		cfg:     cfg,
		hub:     ctx.Tasks,
		output:  output,
		axis:    widget.Row,
		entries: map[string]*dockEntry{},
		launch:  launchApp,
	}
	if cfg.Edge.IsVertical() {
		d.axis = widget.Column
	}
	d.px = taskIconPx(cfg.IconSize, ctx.Config)
	d.zoom = dockZoom{base: d.px}
	if cfg.HoverZoom {
		d.zoom.peak = taskIconPx(cfg.HoverZoomSize, ctx.Config)
	}
	d.panel = &arrangedBox{Box: widget.NewBox(d.axis, trayItemGap(ctx), 0), arranged: d.claim}
	d.panel.AddClass("dock", string(cfg.Edge))
	d.reveal = widget.NewDrawingArea(revealPx, revealPx)
	d.reveal.AddClass("dock-reveal")
	d.reveal.SetVisible(false)
	across := widget.Column
	if d.axis == widget.Column {
		across = widget.Row
	}
	d.surface = &dockSurface{Box: widget.NewBox(across, 0, 0), d: d}
	d.slot = widget.NewBox(d.axis, 0, 0)
	d.slot.Append(d.panel, true)
	edgeAlign(d.slot, cfg.Edge)
	d.surface.AddClass("dock-surface")
	d.surface.Append(d.slot, true)
	d.surface.Append(d.reveal, false)
	d.hide = &autoHide{
		mode:       dockMode(cfg),
		delay:      autoHideDelay(cfg.AutoHideDelayMs),
		after:      after,
		covered:    func() (bool, bool) { on, _, known := d.windows(); return on, known },
		fullscreen: func() (bool, bool) { _, on, known := d.windows(); return on, known },
		apply:      d.applyHidden,
	}
	d.surface.SetOnHoverWithin(d.hide.setHovered)
	d.preview = newWindowPreview(ctx, d.hide.hold)
	d.rects = d.hub.windows.owner()
	d.stops = append(d.stops, d.hub.windows.subscribe(d.render), d.hub.subscribeGeometry(d.syncHidden))
	d.render()
	return d
}

// close stops following the windows and withdraws the dock's
// rectangles.
func (d *dock) close() {
	for _, stop := range d.stops {
		stop()
	}
	d.hide.close()
	d.preview.stop()
	d.rects.release()
}

// attach keeps the dock's surface, which its rectangles, menus and
// previews are on.
func (d *dock) attach(host app.Host) {
	d.host = host
	d.preview.Attach(host)
}

// render shows an entry per app, reusing each app's entry.
func (d *dock) render() {
	apps := dockApps(d.cfg.Pinned, d.hub.windows.windows(), d.output.Name)
	ids := make([]string, len(apps))
	for i, a := range apps {
		ids[i] = a.appID
	}
	d.hub.apps.want(ids)
	for id := range d.entries {
		if !slices.Contains(ids, id) {
			delete(d.entries, id)
		}
	}
	d.panel.Clear()
	for _, a := range apps {
		e := d.entries[a.appID]
		if e == nil {
			e = d.newEntry()
			d.entries[a.appID] = e
		}
		e.app = a
		e.setIcon(d.hub.appIcon(a.appID, d.cfg.Icon), d.cfg.Icon)
		e.SetTooltip(d.hub.appName(a.appID))
		e.SetClass("running", a.running())
		e.SetClass("minimized", a.minimized())
		e.indicator.SetVisible(a.running())
		d.panel.Append(e, false)
	}
	d.shown = apps
	if !slices.Contains(ids, d.hovered) {
		d.hovered = ""
	}
	d.applyZoom()
	d.syncHidden()
}

func (d *dock) newEntry() *dockEntry {
	e := &dockEntry{indicator: widget.NewDrawingArea(max(d.px/6, 3), max(d.px/6, 3))}
	e.indicator.AddClass("dock-indicator")
	e.indicator.OnDraw = func(cv *render.Canvas, r render.Rect) {
		if e.app.minimized() {
			defer cv.PopAlpha(cv.PushAlpha(dockMinimizedAlpha))
		}
		cv.RoundedRect(r, min(r.W, r.H)/2, widget.Current().Accent)
	}
	e.taskButton = newTaskButton(d.ctx, d.px, "task-item", e.indicator)
	e.AddClass("dock-item")
	e.OnClick = func() { d.activate(e.app) }
	e.SetOnPointer(func(ev widget.PointerEvent) bool {
		if ev.Button != widget.BTNRight {
			return false
		}
		d.menu(e)
		return true
	})
	e.SetOnHoverWithin(func(on bool) {
		d.hoverEntry(e.app.appID, on)
		d.preview.hover(on && len(e.app.windows) > 1, e, func() ([]previewWindow, previewActions) { return dockPreview(e.app) })
	})
	return e
}

// dockPreview is an app's windows as its entry's preview shows them.
func dockPreview(a dockApp) ([]previewWindow, previewActions) {
	actions := taskActions{windows: make(map[string]taskWindow, len(a.windows))}
	windows := make([]previewWindow, len(a.windows))
	for i, w := range a.windows {
		id := strconv.Itoa(i)
		actions.windows[id] = w
		windows[i] = previewWindow{id: id, title: w.Title(), appID: w.AppID()}
	}
	return windows, actions
}

// activate launches a closed app and brings up an open one's next
// window.
func (d *dock) activate(a dockApp) {
	if a.running() {
		a.target().Activate()
		return
	}
	app, ok := d.hub.apps.get(a.appID)
	if !ok || !app.found {
		log.Printf("dock: %s has no desktop entry to launch", a.appID)
		return
	}
	d.launch(app)
}

// menu lists the app's windows, and offers to open another.
func (d *dock) menu(e *dockEntry) {
	items := make([]widget.MenuItem, 0, len(e.app.windows)+1)
	for _, w := range e.app.windows {
		items = append(items, menuItem(d.hub.windowTooltip(w), w.Activate))
	}
	if app, ok := d.hub.apps.get(e.app.appID); ok && app.found {
		items = append(items, menuItem(i18n.T("bar-task-launch"), func() { d.launch(app) }))
	}
	openTaskMenu(d.ctx, d.host, e, items)
}

// launchApp starts an app from its desktop entry off the loop.
func launchApp(app taskApp) {
	go func() {
		if err := app.entry.Launch(); err != nil {
			log.Printf("dock: launch %s: %v", app.entry.ID, err)
		}
	}()
}

// claim names each app's entry as the minimize rectangle of its windows.
func (d *dock) claim() {
	if d.hide.hidden {
		return
	}
	if d.hovered == "" {
		d.measured(widget.Size{W: d.slot.Bounds().W, H: d.slot.Bounds().H})
	}
	if d.host == nil {
		return
	}
	claims := map[taskWindow]rectClaim{}
	for _, a := range d.shown {
		r := d.entries[a.appID].Bounds()
		for _, w := range a.windows {
			claims[w] = rectClaim{priority: rectFromDock, output: d.output.Name, host: d.host, rect: r}
		}
	}
	d.rects.set(claims)
}

// dockMode is the reveal mode an auto-hiding dock has: it hides while a
// window covers it.
func dockMode(cfg config.DockConfig) config.BarAutoHide {
	if cfg.AutoHide {
		return config.AutoHideWindowsOverlap
	}
	return config.AutoHideNever
}

// windows reports a window covering the dock where it shows and one
// filling its output; known is false without lys-wm's window geometry,
// so an auto-hiding dock shows only under the pointer.
func (d *dock) windows() (covering, fullscreen, known bool) {
	out := wmipc.Rect{X: int(d.output.LogicalX), Y: int(d.output.LogicalY), Width: int(d.output.LogicalW), Height: int(d.output.LogicalH)}
	return windowsOn(d.hub, d.output.Name, dockArea(d.cfg.Edge, out, d.size))
}

// syncHidden lets the reveal controller decide again.
func (d *dock) syncHidden() { d.hide.sync() }

// applyHidden swaps the entries for the reveal strip, or back.
func (d *dock) applyHidden(hide bool) {
	if hide {
		d.rects.release()
	}
	if d.axis == widget.Row {
		d.reveal.SetContentSize(d.size.W, revealPx)
	} else {
		d.reveal.SetContentSize(revealPx, d.size.H)
	}
	d.slot.SetVisible(!hide)
	d.reveal.SetVisible(hide)
	d.fitHeadroom()
}

// dockZoom is how entries grow under the pointer: the hovered one to
// peak, its neighbors halfway; a peak no larger than base is no zoom.
type dockZoom struct{ base, peak int }

func (z dockZoom) on() bool { return z.peak > z.base }

// px is the icon size of an entry steps entries from the hovered one.
func (z dockZoom) px(steps int) int {
	if !z.on() {
		return z.base
	}
	switch steps {
	case 0:
		return z.peak
	case 1:
		return (z.base + z.peak) / 2
	}
	return z.base
}

// growth is how much a zoomed dock outgrows its resting size along its
// edge and across it.
func (z dockZoom) growth() (along, across int) {
	if !z.on() {
		return 0, 0
	}
	return z.px(0) - z.base + 2*(z.px(1)-z.base), z.peak - z.base
}

// hoverEntry zooms around the app whose entry the pointer entered, and
// back once it left without entering another.
func (d *dock) hoverEntry(appID string, on bool) {
	switch {
	case on:
		d.hovered = appID
	case d.hovered == appID:
		d.hovered = ""
	default:
		return
	}
	d.applyZoom()
}

// applyZoom sizes every entry's icon for its distance from the hovered
// one.
func (d *dock) applyZoom() {
	at := slices.IndexFunc(d.shown, func(a dockApp) bool { return a.appID == d.hovered })
	for i, a := range d.shown {
		steps := -1
		if at >= 0 {
			steps = max(i-at, at-i)
		}
		d.entries[a.appID].resize(d.zoom.px(steps))
	}
}

// measure sizes the resting panel as its styles lay it out, before the
// first frame, so the surface reserves its edge from the start.
func (d *dock) measure() {
	d.measured(widget.SizeWithin(d.slot, widget.Size{W: int(d.output.LogicalW), H: int(d.output.LogicalH)}))
}

// measured takes the resting size of the laid-out panel, settling the
// headroom and the reveal decision after the frame when it changed.
func (d *dock) measured(size widget.Size) {
	if size == d.size {
		return
	}
	d.size = size
	d.ctx.Invoke(func() {
		d.fitHeadroom()
		d.hide.sync()
		if d.rested != nil {
			d.rested()
		}
	})
}

// fitHeadroom keeps the shown surface large enough for the zoomed
// entries, so hovering never resizes it; a hidden dock is its strip.
func (d *dock) fitHeadroom() {
	along, across := d.zoom.growth()
	if d.hide.hidden || along == 0 {
		d.surface.SetSizeRequest(0, 0)
		return
	}
	if d.axis == widget.Row {
		d.surface.SetSizeRequest(d.size.W+along, d.size.H+across)
	} else {
		d.surface.SetSizeRequest(d.size.W+across, d.size.H+along)
	}
}

// thickness is how far the resting dock reaches out from its edge.
func (d *dock) thickness() int {
	if d.axis == widget.Row {
		return d.size.H
	}
	return d.size.W
}

// edgeAlign seats w against edge, centered along it.
func edgeAlign(w *widget.Box, edge config.DockEdge) {
	switch edge {
	case config.DockEdgeBottom:
		w.SetHAlign(widget.AlignCenter)
		w.SetVAlign(widget.AlignEnd)
	case config.DockEdgeLeft:
		w.SetHAlign(widget.AlignStart)
		w.SetVAlign(widget.AlignCenter)
	case config.DockEdgeRight:
		w.SetHAlign(widget.AlignEnd)
		w.SetVAlign(widget.AlignCenter)
	}
}

// dockSurface is the dock's root: it takes pointer input on the panel
// alone, so the headroom kept for zoom passes clicks to what is beneath.
type dockSurface struct {
	*widget.Box
	d *dock
}

// InputRegion is the shown panel with its margins, which reach the
// screen edge so a pointer pushed against it is on the dock, or all of
// a hidden dock's strip.
func (s *dockSurface) InputRegion() []render.Rect {
	if s.d.hide.hidden {
		return nil
	}
	return []render.Rect{s.d.slot.Bounds()}
}

// dockHost opens the docks of the shell's mount generation.
type dockHost struct {
	app *app.Application
	rt  *barRuntime
}

// open shows one output's dock.
func (h *dockHost) open(output *app.Output, cfg config.DockConfig) (surfaceLayer[config.DockConfig], error) {
	ctx := h.rt.ctx
	if ctx.Tasks == nil {
		return nil, errNoWindowList
	}
	ctx.Connector = output.Name
	w := &dockWindow{host: h, output: output}
	w.build(ctx, cfg)
	layer, err := h.app.NewLayer(app.LayerConfig{
		Output:    output,
		Layer:     layering.For(ctx.Config.General, config.LayerTop),
		Anchor:    dockAnchor(cfg.Edge),
		Keyboard:  app.KeyboardNone,
		Namespace: "lys-shell-dock-" + output.Name,
		Root:      w.root,
		OnResize:  func(int, int) { w.reserve() },
	})
	if err != nil {
		w.dock.close()
		return nil, err
	}
	w.layer = layer
	w.dock.attach(layer)
	w.reserve()
	return w, nil
}

// dockWindow is an open dock: its layer and the dock it shows.
type dockWindow struct {
	host   *dockHost
	output *app.Output
	layer  *app.LayerWindow
	root   *widget.Box
	dock   *dock
	cfg    config.DockConfig
}

// build fills the window's root with a dock for cfg.
func (w *dockWindow) build(ctx ModuleContext, cfg config.DockConfig) {
	if w.root == nil {
		w.root = widget.NewBox(widget.Row, 0, 0)
		if ctx.Theme != nil {
			ctx.Theme.Attach(w.root)
		}
	}
	w.cfg = cfg
	w.dock = newDock(ctx, cfg, w.output, loopAfter(w.host.app))
	w.dock.rested = w.reserve
	w.root.Clear()
	w.root.Append(w.dock.surface, false)
	w.dock.measure()
}

// reserve keeps the resting dock's thickness clear of windows while it
// does not auto-hide, never the headroom kept for zoom.
func (w *dockWindow) reserve() {
	if w.layer == nil {
		return
	}
	zone := int32(0)
	if !w.cfg.AutoHide {
		zone = intconv.Clamp[int32](w.dock.thickness())
	}
	if err := w.layer.SetExclusiveZone(zone); err != nil {
		log.Printf("dock %s: exclusive zone %d: %v", w.output.Name, zone, err)
	}
}

// Rebuild shows the dock for cfg on the same surface, moving it to the
// new edge.
func (w *dockWindow) Rebuild(cfg config.DockConfig) error {
	if cfg.Edge != w.cfg.Edge {
		if err := w.layer.SetAnchor(dockAnchor(cfg.Edge)); err != nil {
			return fmt.Errorf("move to %s: %w", cfg.Edge, err)
		}
	}
	w.dock.close()
	ctx := w.host.rt.ctx
	ctx.Connector = w.output.Name
	w.build(ctx, cfg)
	w.dock.attach(w.layer)
	w.reserve()
	return nil
}

func (w *dockWindow) Close() {
	w.dock.close()
	w.layer.Close()
}

// dockFor is the dock config an output shows, and whether it shows one.
func dockFor(cfg *config.Config, connector string) (config.DockConfig, bool) {
	return cfg.Dock, cfg.Dock.ShowsOn(connector)
}
