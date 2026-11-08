package bar

import (
	"log"
	"math"
	"path/filepath"
	"slices"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/wmipc"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// taskHub is the shell's window state the minimized-windows modules and
// the docks share: the one window list and its apps' desktop entries.
// It runs on the loop.
type taskHub struct {
	windows      *taskWindows
	apps         *taskApps
	geom         windowGeometry
	geomWatchers []*func()
}

func newTaskHub(src toplevelSource, post func(func())) *taskHub {
	h := &taskHub{windows: newTaskWindows(src)}
	h.apps = newTaskApps(post, h.windows.notify)
	return h
}

func (h *taskHub) close() { h.windows.close() }

// appIcon is the icon an app shows: its desktop entry's, or fallback
// while the entry is unknown or names none.
func (h *taskHub) appIcon(appID, fallback string) string {
	if app, ok := h.apps.get(appID); ok && app.found && app.icon != "" {
		return app.icon
	}
	return fallback
}

// appName is the app's desktop entry name, or its app id.
func (h *taskHub) appName(appID string) string {
	if app, ok := h.apps.get(appID); ok && app.found {
		return app.name
	}
	return appID
}

// windowTooltip is a window's title, or its app's name when untitled.
func (h *taskHub) windowTooltip(w taskWindow) string {
	if w.Title() != "" {
		return w.Title()
	}
	return h.appName(w.AppID())
}

// taskIconPx resolves an icon-size against TaskIconBaseRem at the bar
// scale.
func taskIconPx(size config.Size, cfg *config.Config) int {
	return int(math.Round(size.ResolvePx(config.TaskIconBaseRem*styling.RemBase, float64(cfg.Bar.Scale))))
}

// taskButton is one entry of the module or the dock: a button around an
// app icon, which it rebuilds only when the icon changes.
type taskButton struct {
	*widget.Button
	slot     *widget.Box
	icon     string
	fallback string
	px       int
	invoke   func(func())
}

// newTaskButton builds an entry of px icons; marks, when given, sit over
// the icon at its foot, the dock's running indicator.
func newTaskButton(ctx ModuleContext, px int, class string, marks ...widget.Widget) *taskButton {
	b := &taskButton{slot: widget.NewBox(widget.Row, 0, 0), px: px, invoke: ctx.Invoke}
	content := widget.NewOverlay().Append(widgets.Center(widgets.Pin(b.slot, px, px)))
	for _, m := range marks {
		content.AppendAligned(m, widget.AlignCenter, widget.AlignEnd)
	}
	b.Button = widget.NewButton(content, 0, 0)
	b.AddClass(class)
	if ctx.Style != nil {
		b.BgHover = ctx.Style.buttonBgHover
		b.BgPressed = ctx.Style.buttonBgActive
	}
	return b
}

// setIcon shows name, a theme icon or an icon file, with fallback for a
// file that does not load; the same name keeps the icon it has.
func (b *taskButton) setIcon(name, fallback string) {
	if name == b.icon {
		return
	}
	b.icon, b.fallback = name, fallback
	b.showIcon()
}

// resize shows the icon at px, the dock's hover zoom.
func (b *taskButton) resize(px int) {
	if px == b.px {
		return
	}
	b.px = px
	widgets.Pin(b.slot, px, px)
	if b.icon != "" {
		b.showIcon()
	}
}

func (b *taskButton) showIcon() {
	b.slot.Clear()
	b.slot.Append(widgets.Center(taskIcon(b.icon, b.fallback, b.px, b.invoke)), true)
}

func taskIcon(name, fallback string, px int, invoke func(func())) *widget.Icon {
	if filepath.IsAbs(name) {
		return widgets.NewAsyncFileIcon(name, fallback, px, invoke, nil)
	}
	return widget.NewThemeIconFallback(name, px)
}

// arrangedBox is a box that hears each layout once its children have
// their bounds, where an entry list names its windows' rectangles.
type arrangedBox struct {
	*widget.Box
	arranged func()
}

func (b *arrangedBox) Arrange(r render.Rect) {
	b.Box.Arrange(r)
	b.arranged()
}

// nextSlot is where an entry after last would sit along axis, gap past
// it, for a window that has no entry yet.
func nextSlot(last render.Rect, axis widget.Axis, gap int) render.Rect {
	if axis == widget.Column {
		last.Y += last.H + gap
		return last
	}
	last.X += last.W + gap
	return last
}

// menuItem is one context menu row.
func menuItem(label string, run func()) widget.MenuItem {
	return widget.MenuItem{Label: label, OnClick: run}
}

// openTaskMenu opens a context menu beside anchor on host.
func openTaskMenu(ctx ModuleContext, host app.Host, anchor widget.Boundser, items []widget.MenuItem) {
	if ctx.App == nil || host == nil {
		return
	}
	font, px := dropdownFont(ctx)
	if _, err := ctx.App.OpenMenuPopover(host, app.MenuPopoverConfig{Anchor: anchor, Face: font, SizePx: px, Items: items, Gap: widget.PopoverGap}); err != nil {
		log.Printf("task menu: %v", err)
	}
}

// windowGeometry is where lys-wm's windows are: the watcher that keeps
// them, read on the loop.
type windowGeometry interface {
	Windows() []wmipc.Window
}

// followGeometry keeps lys-wm's window geometry in the hub: the docks
// re-check whether a window covers them after each change. It runs on
// the loop; ticks closes when the stream ends, which drops the geometry.
func (h *taskHub) followGeometry(g windowGeometry, ticks <-chan struct{}, post func(func())) {
	h.geom = g
	go func() {
		for range ticks {
			post(h.geometryChanged)
		}
		post(func() {
			h.geom = nil
			h.geometryChanged()
		})
	}()
}

func (h *taskHub) geometryChanged() {
	for _, fn := range slices.Clone(h.geomWatchers) {
		(*fn)()
	}
}

// geometry is every lys-wm window, false without lys-wm's geometry.
func (h *taskHub) geometry() ([]wmipc.Window, bool) {
	if h.geom == nil {
		return nil, false
	}
	return h.geom.Windows(), true
}

// subscribeGeometry calls fn after each geometry change; stop ends it.
func (h *taskHub) subscribeGeometry(fn func()) (stop func()) {
	w := &fn
	h.geomWatchers = append(h.geomWatchers, w)
	return func() {
		h.geomWatchers = slices.DeleteFunc(h.geomWatchers, func(o *func()) bool { return o == w })
	}
}
