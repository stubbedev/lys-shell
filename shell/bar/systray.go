package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/glob"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// errSystrayNoStore guards the wiring: the module needs the shared
// store RunWith owns.
var errSystrayNoStore = errors.New("systray: no SNI store available")

// Tray icon constants (systray/item/helpers.rs).
const (
	// trayFallbackIcon is the glyph for an item with no icon at all.
	trayFallbackIcon = "ld-app-window-symbolic"
	trayCallTimeout  = 5 * time.Second
)

// trayIconExtensions are tried, in order, in an item's IconThemePath.
var trayIconExtensions = []string{"png", "svg", "xpm"}

// TrayService is what the tray module drives: the item controls, the
// DBusMenu calls, and the open-menu change feed. The shell wires the
// SNI host; tests fake it.
type TrayService interface {
	Activate(ctx context.Context, it sni.Item) error
	SecondaryActivate(ctx context.Context, it sni.Item) error
	ContextMenu(ctx context.Context, it sni.Item) error
	// OpenMenu is AboutToShow and the layout, once per showing.
	OpenMenu(ctx context.Context, it sni.Item) (sni.MenuItem, error)
	// MenuLayout re-reads an open menu's layout without AboutToShow.
	MenuLayout(ctx context.Context, it sni.Item) (sni.MenuItem, error)
	// SubmenuOpening is AboutToShow for a submenu as it opens; true asks
	// for the layout to be read again.
	SubmenuOpening(ctx context.Context, it sni.Item, id int32) (bool, error)
	MenuClicked(ctx context.Context, it sni.Item, id int32) error
	// WatchMenu ticks on the item's menu changes until stop, which
	// closes the feed and may be called more than once.
	WatchMenu(key string) (<-chan struct{}, func())
	// WantPixmapSize keeps the items' pixmaps for px device pixels
	// until release.
	WantPixmapSize(px int) (release func())
}

// NewTrayService adapts a running SNI host.
func NewTrayService(h *sni.Host) TrayService { return hostTray{h: h, a: h.Actions()} }

// hostTray is the live TrayService; activations carry (0, 0) as the
// Rust tray sends.
type hostTray struct {
	h *sni.Host
	a *sni.Actions
}

func (t hostTray) Activate(ctx context.Context, it sni.Item) error {
	return t.a.Activate(ctx, it, 0, 0)
}

func (t hostTray) SecondaryActivate(ctx context.Context, it sni.Item) error {
	return t.a.SecondaryActivate(ctx, it, 0, 0)
}

func (t hostTray) ContextMenu(ctx context.Context, it sni.Item) error {
	return t.a.ContextMenu(ctx, it, 0, 0)
}

func (t hostTray) OpenMenu(ctx context.Context, it sni.Item) (sni.MenuItem, error) {
	return t.a.OpenMenu(ctx, it)
}

func (t hostTray) MenuLayout(ctx context.Context, it sni.Item) (sni.MenuItem, error) {
	return t.a.MenuLayout(ctx, it)
}

func (t hostTray) SubmenuOpening(ctx context.Context, it sni.Item, id int32) (bool, error) {
	return t.a.AboutToShow(ctx, it, id)
}

func (t hostTray) MenuClicked(ctx context.Context, it sni.Item, id int32) error {
	return t.a.MenuClicked(ctx, it, id)
}

func (t hostTray) WatchMenu(key string) (<-chan struct{}, func()) { return t.h.WatchMenu(key) }

func (t hostTray) WantPixmapSize(px int) func() { return t.h.WantPixmapSize(px) }

// systrayMatches is helpers.rs's pattern test against an item's Id and
// Title: case-sensitive globs.
func systrayMatches(pattern string, it sni.Item) bool {
	return glob.Wildcard(pattern, it.ID) || glob.Wildcard(pattern, it.Title)
}

// systrayBlacklisted is is_blacklisted.
func systrayBlacklisted(patterns []string, it sni.Item) bool {
	for _, p := range patterns {
		if systrayMatches(p, it) {
			return true
		}
	}
	return false
}

// systrayOverride is find_override: the first entry whose name matches.
func systrayOverride(overrides []config.TrayItemOverride, it sni.Item) (config.TrayItemOverride, bool) {
	for _, o := range overrides {
		if systrayMatches(o.Name, it) {
			return o, true
		}
	}
	return config.TrayItemOverride{}, false
}

// themePathIcon is find_icon_in_theme_path: <path>/<name>.<ext>.
func themePathIcon(themePath, name string) (string, bool) {
	if themePath == "" {
		return "", false
	}
	for _, ext := range trayIconExtensions {
		file := filepath.Join(themePath, name+"."+ext)
		if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() {
			return file, true
		}
	}
	return "", false
}

// isFile reports whether name is a readable regular file path.
func isFile(name string) bool {
	st, err := os.Stat(name)
	return err == nil && st.Mode().IsRegular()
}

// trayIconKind is where a glyph comes from.
type trayIconKind uint8

const (
	trayIconFile trayIconKind = iota
	trayIconNamed
	trayIconPixmap
	trayIconFallback
)

// trayIconSource is the resolved source of a glyph, and its identity
// for change detection (item/mod.rs IconSignature).
type trayIconSource struct {
	kind   trayIconKind
	name   string
	pixmap sni.Pixmap
}

// signature identifies the source: equal sources keep the widget.
func (s trayIconSource) signature() string {
	if s.kind == trayIconPixmap {
		return "pixmap:" + strconv.Itoa(int(s.pixmap.Width)) + "x" + strconv.Itoa(int(s.pixmap.Height)) + ":" + string(s.pixmap.Data)
	}
	return strconv.Itoa(int(s.kind)) + ":" + s.name
}

// iconSource is the source of one SNI name and pixmap pair: the name -
// as a file in IconThemePath, a file path, or a theme name - else the
// pixmap sni.BestPixmap picks for devicePx; false without either.
func iconSource(name, themePath string, pixmaps []sni.Pixmap, devicePx int) (trayIconSource, bool) {
	if name != "" {
		if file, ok := themePathIcon(themePath, name); ok {
			return trayIconSource{kind: trayIconFile, name: file}, true
		}
		if isFile(name) {
			return trayIconSource{kind: trayIconFile, name: name}, true
		}
		return trayIconSource{kind: trayIconNamed, name: name}, true
	}
	if p, ok := sni.BestPixmap(pixmaps, devicePx); ok {
		return trayIconSource{kind: trayIconPixmap, pixmap: p}, true
	}
	return trayIconSource{}, false
}

// trayLook is what an item's button draws: its icon, whether that is
// the item's attention icon, and the overlay composited over it.
type trayLook struct {
	icon       trayIconSource
	attention  bool
	overlay    trayIconSource
	hasOverlay bool
}

// signature identifies the look: equal looks keep the widgets.
func (l trayLook) signature() string {
	if !l.hasOverlay {
		return l.icon.signature()
	}
	return l.icon.signature() + "\x00" + l.overlay.signature()
}

// resolveTrayLook is update_icon's source pick. A NeedsAttention item
// draws its AttentionIconName, else its AttentionIconPixmap, over an
// override's icon. Otherwise, or without either, it draws the override
// icon or its IconName, else its IconPixmap, else the fallback glyph.
// OverlayIconName, else OverlayIconPixmap, composites over the icon
// drawn.
func resolveTrayLook(cfg config.SystrayConfig, it sni.Item, devicePx int) trayLook {
	var l trayLook
	l.overlay, l.hasOverlay = iconSource(it.OverlayIconName, it.IconThemePath, it.OverlayIconPixmap, devicePx)
	if it.Status == sni.StatusNeedsAttention {
		if src, ok := iconSource(it.AttentionName, it.IconThemePath, it.AttentionIconPixmap, devicePx); ok {
			l.icon, l.attention = src, true
			return l
		}
	}
	name := it.IconName
	if o, ok := systrayOverride(cfg.Overrides, it); ok && o.Icon != nil && *o.Icon != "" {
		name = *o.Icon
	}
	src, ok := iconSource(name, it.IconThemePath, it.IconPixmap, devicePx)
	if !ok {
		src = trayIconSource{kind: trayIconFallback, name: trayFallbackIcon}
	}
	l.icon = src
	return l
}

// trayGlyph is an item's icon widget.
type trayGlyph interface {
	widget.Widget
	Bounds() render.Rect
	SetTint(render.Color)
}

// buildTrayIcon makes the widget for a source at px logical pixels; a
// pixmap rasterizes at scale, the device scale the tray last drew at,
// and falls back to the generic glyph when it does not decode.
func buildTrayIcon(src trayIconSource, px int, scale deviceScale, hooks trayPixmapHooks) trayGlyph {
	switch src.kind {
	case trayIconFile:
		return widget.NewFileIcon(src.name, px)
	case trayIconPixmap:
		if g, err := newTrayPixmap(src.pixmap, px, scale, hooks); err == nil {
			return g
		}
		return widget.NewThemeIcon(trayFallbackIcon, px)
	}
	return widget.NewThemeIconFallback(src.name, px)
}

// deviceScale is a canvas's device scale: num device pixels per denom
// logical ones.
type deviceScale struct{ num, denom int }

// box is the device pixels covering px logical ones, rounded up as a
// theme icon's device box is.
func (s deviceScale) box(px int) int { return (px*s.num + s.denom - 1) / s.denom }

// trayPixmapHooks are what a pixmap glyph reports to its tray: the
// device scale it drew at, and the rasters it stops drawing.
type trayPixmapHooks struct {
	scaled func(deviceScale)
	forget func(render.ImageID)
}

// trayPixmap draws an item's pixmap rasterized at the canvas device
// scale, one raster at a time: a scale change re-rasterizes the same
// pixmap before the frame draws, and the raster it replaces, or the
// last one on retire, is forgotten.
type trayPixmap struct {
	*widget.DrawingArea
	pixmap sni.Pixmap
	px     int
	scale  deviceScale
	tint   render.Color
	ic     *render.Icon
	hooks  trayPixmapHooks
}

func newTrayPixmap(p sni.Pixmap, px int, scale deviceScale, hooks trayPixmapHooks) (*trayPixmap, error) {
	g := &trayPixmap{DrawingArea: widget.NewDrawingArea(px, px), pixmap: p, px: px, hooks: hooks}
	if err := g.rasterize(scale); err != nil {
		return nil, err
	}
	g.OnDraw = g.draw
	return g, nil
}

// rasterize replaces the raster with one made for scale and the tint.
func (g *trayPixmap) rasterize(scale deviceScale) error {
	box := scale.box(g.px)
	ic, err := render.IconFromARGB32(int(g.pixmap.Width), int(g.pixmap.Height), g.pixmap.Data, box, box)
	if err != nil {
		return fmt.Errorf("systray: pixmap %dx%d at %d px: %w", g.pixmap.Width, g.pixmap.Height, box, err)
	}
	ic = ic.AtScale(scale.num, scale.denom)
	if g.tint != 0 {
		ic = ic.Tint(g.tint)
	}
	g.retire()
	g.ic, g.scale = ic, scale
	return nil
}

// retire forgets the raster drawn so far.
func (g *trayPixmap) retire() {
	if g.ic != nil {
		g.hooks.forget(g.ic.Still().ID)
		g.ic = nil
	}
}

// SetTint recolors the pixmap as a mask, as a static icon's tint does.
// The pixmap decoded when the glyph was built, so it decodes again.
func (g *trayPixmap) SetTint(tint render.Color) {
	if tint == g.tint {
		return
	}
	g.tint = tint
	if err := g.rasterize(g.scale); err != nil {
		panic(err)
	}
	g.QueueDraw()
}

// draw re-rasterizes for a new device scale, then reports it, before
// drawing centered in the area.
func (g *trayPixmap) draw(cv *render.Canvas, area render.Rect) {
	num, denom := cv.DeviceScale()
	if s := (deviceScale{num, denom}); s != g.scale {
		if err := g.rasterize(s); err != nil {
			panic(err)
		}
		g.hooks.scaled(s)
	}
	w, h := g.ic.LogicalSize()
	g.ic.Draw(cv, area.X+(area.W-w)/2, area.Y+(area.H-h)/2)
}

// trayEntry is one rendered item.
type trayEntry struct {
	item   sni.Item
	button *widget.Button
	slot   *widget.Box
	icon   trayGlyph
	// overlay is the glyph composited over icon's bottom-right quarter,
	// nil without one.
	overlay trayGlyph
	sig     string
}

// retireIcon drops the entry's glyphs, forgetting their pixmap rasters.
func (e *trayEntry) retireIcon() {
	for _, g := range []trayGlyph{e.icon, e.overlay} {
		if p, ok := g.(*trayPixmap); ok {
			p.retire()
		}
	}
	e.icon, e.overlay = nil, nil
}

// systrayModule is the tray: one button per SNI item, following the
// store, with DBusMenu menus in a popover.
type systrayModule struct {
	ctx   ModuleContext
	store *sni.Store
	tray  TrayService
	host  app.Host

	entries map[string]*trayEntry
	root    *widget.Box
	// chrome is the module wrapper, hidden while the tray is empty as
	// mod.rs hides the root's parent; shown mirrors its visibility for
	// a chrome that arrives later.
	chrome moduleChrome
	shown  bool

	// menu is the open menu, nil while none is.
	menu *trayMenu
	// accels binds the open menu's shortcuts (nil: the application);
	// accelKeys are the bindings made, undone on refresh and close.
	accels    accelBinder
	accelKeys []string

	// scale is the device scale the tray last drew at; unwant releases
	// the pixmap size wanted for it.
	scale  deviceScale
	unwant func()
	// forget drops a raster's GPU copy (nil: the application's).
	forget func(render.ImageID)
}

// trayMenu is an open menu: its item key, its popover (nil headless),
// the rows last built, and the release of its change feed.
type trayMenu struct {
	key   string
	pop   *app.MenuPopover
	items []widget.MenuItem
	stop  func()
}

// accelBinder is the application accelerator table the tray menu's
// shortcuts go into (app.Application).
type accelBinder interface {
	AddAccel(keys string, action widget.Activatable) error
	RemoveAccel(keys string) error
}

func newSystray(ctx ModuleContext) (Module, error) {
	if ctx.SNI == nil {
		return nil, errSystrayNoStore
	}
	m := &systrayModule{
		ctx:     ctx,
		store:   ctx.SNI,
		tray:    ctx.Tray,
		entries: make(map[string]*trayEntry),
		root:    widget.NewBox(widget.Row, trayItemGap(ctx), 0),
		scale:   deviceScale{1, 1},
	}
	m.root.AddClass("systray")
	m.want()
	ticks, stop := ctx.SNI.Subscribe()
	follow(ctx, ticks, stop, func(struct{}) { m.refresh() })
	go func() {
		<-ctx.gen.ended()
		m.onLoop(m.retire)
	}()
	m.refresh()
	return m, nil
}

// want asks the host for the pixmaps the tray draws at its scale, then
// releases the size wanted before.
func (m *systrayModule) want() {
	if m.tray == nil {
		return
	}
	prev := m.unwant
	m.unwant = m.tray.WantPixmapSize(m.devicePx())
	if prev != nil {
		prev()
	}
}

// devicePx is the icon box in device pixels at the tray's scale.
func (m *systrayModule) devicePx() int { return m.scale.box(m.iconPx()) }

// scaled follows a pixmap glyph to the device scale it drew at: the
// host keeps the pixmaps for the new size, and the refresh that follows
// picks from them.
func (m *systrayModule) scaled(s deviceScale) {
	if s == m.scale {
		return
	}
	m.scale = s
	m.want()
}

// pixmapHooks wire a pixmap glyph to the tray.
func (m *systrayModule) pixmapHooks() trayPixmapHooks {
	return trayPixmapHooks{scaled: m.scaled, forget: m.forgetImage}
}

func (m *systrayModule) forgetImage(id render.ImageID) {
	switch {
	case m.forget != nil:
		m.forget(id)
	case m.ctx.App != nil:
		m.ctx.App.ForgetImage(id)
	}
}

// onLoop runs fn on the event loop even once the generation retired,
// which ctx.Invoke skips.
func (m *systrayModule) onLoop(fn func()) {
	if m.ctx.App == nil {
		headlessLoop.Lock()
		defer headlessLoop.Unlock()
		fn()
		return
	}
	m.ctx.App.Invoke(fn)
}

// retire releases the wanted pixmap size and forgets every raster as
// the module's generation ends.
func (m *systrayModule) retire() {
	if m.unwant != nil {
		m.unwant()
		m.unwant = nil
	}
	for _, e := range m.entries {
		e.retireIcon()
	}
}

// trayItemGap is item-gap in pixels (resolved at 1 rem).
func trayItemGap(ctx ModuleContext) int {
	return int(math.Round(ctx.Config.Systray.ItemGap.ResolvePx(styling.RemBase, float64(ctx.Config.Bar.Scale))))
}

// iconPx is icon-scale resolved at the 1.25 rem base.
func (m *systrayModule) iconPx() int {
	return int(math.Round(m.ctx.Config.Systray.IconScale.ResolvePx(config.SystrayIconBaseRem*styling.RemBase, float64(m.ctx.Config.Bar.Scale))))
}

// Attach records the layer the menus open on.
func (m *systrayModule) Attach(host app.Host) { m.host = host }

// ownsChrome keeps the tray out of the module-button wrapper: each
// item is its own button.
func (m *systrayModule) ownsChrome() {}

// refresh reconciles the buttons with the store (update_items): the
// blacklist hides items, the rest keep registration order, and each
// icon rebuilds only when its source changed.
func (m *systrayModule) refresh() {
	cfg := m.ctx.Config.Systray
	live := make(map[string]bool)
	var order []*trayEntry
	for _, it := range m.store.Items() {
		if systrayBlacklisted(cfg.Blacklist, it) {
			continue
		}
		key := it.Key()
		live[key] = true
		e, ok := m.entries[key]
		if !ok {
			e = m.newEntry(it)
			m.entries[key] = e
		}
		e.item = it
		m.updateIcon(e)
		order = append(order, e)
	}
	for key := range m.entries {
		if !live[key] {
			m.entries[key].retireIcon()
			delete(m.entries, key)
			if m.menu != nil && key == m.menu.key {
				m.closeMenu()
			}
		}
	}
	m.root.Clear()
	for _, e := range order {
		m.root.Append(e.button, false)
	}
	m.shown = len(order) > 0
	m.showChrome()
}

func (m *systrayModule) showChrome() {
	if m.chrome != nil {
		m.chrome.SetVisible(m.shown)
	}
}

func (m *systrayModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.showChrome()
}

// newEntry builds one item's button and wires its clicks.
func (m *systrayModule) newEntry(it sni.Item) *trayEntry {
	e := &trayEntry{item: it}
	px := m.iconPx()
	e.slot = widget.NewBox(widget.Row, 0, 0)
	e.button = widget.NewButton(widgets.Center(widgets.Pin(e.slot, px, px)), 0, 0)
	e.button.AddClass("systray-item")
	if m.ctx.Style != nil {
		e.button.BgHover = m.ctx.Style.buttonBgHover
		e.button.BgPressed = m.ctx.Style.buttonBgActive
	}
	key := it.Key()
	e.button.OnClick = func() { m.leftClick(key) }
	e.button.SetOnPointer(func(ev widget.PointerEvent) bool {
		switch ev.Button {
		case widget.BTNMiddle:
			m.middleClick(key)
		case widget.BTNRight:
			m.rightClick(key)
		default:
			return false
		}
		return true
	})
	return e
}

// updateIcon applies the resolved look and the override tint, which
// recolors the item's own icon but neither its attention icon nor its
// overlay.
func (m *systrayModule) updateIcon(e *trayEntry) {
	cfg := m.ctx.Config.Systray
	look := resolveTrayLook(cfg, e.item, m.devicePx())
	if sig := look.signature(); sig != e.sig || e.icon == nil {
		e.retireIcon()
		m.buildLook(e, look)
		e.sig = sig
	}
	var tint render.Color
	if o, ok := systrayOverride(cfg.Overrides, e.item); ok && o.Color != nil && !look.attention {
		tint, _ = styling.ResolveColor(*o.Color, m.ctx.Style.palette)
	}
	e.icon.SetTint(tint)
}

// buildLook fills the entry's slot: the icon centered, and the overlay
// at half the icon size in its bottom-right corner.
func (m *systrayModule) buildLook(e *trayEntry, look trayLook) {
	px, hooks := m.iconPx(), m.pixmapHooks()
	e.icon = buildTrayIcon(look.icon, px, m.scale, hooks)
	e.slot.Clear()
	if !look.hasOverlay {
		e.slot.Append(widgets.Center(e.icon), true)
		return
	}
	e.overlay = buildTrayIcon(look.overlay, px/2, m.scale, hooks)
	stack := widget.NewOverlay().
		Append(widgets.Center(e.icon)).
		AppendAligned(e.overlay, widget.AlignEnd, widget.AlignEnd)
	e.slot.Append(stack, true)
}

// call runs one item control off the loop, logging failures as the
// Rust tray's warn! does.
func (m *systrayModule) call(what string, key string, fn func(ctx context.Context, it sni.Item) error) {
	e, ok := m.entries[key]
	if !ok || m.tray == nil {
		return
	}
	it := e.item
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), trayCallTimeout)
		defer cancel()
		if err := fn(ctx, it); err != nil {
			log.Printf("systray: %s %s: %v", it.ID, what, err)
		}
	}()
}

// leftClick activates the item, or asks for its own menu when the item
// is menu-only (ItemIsMenu).
func (m *systrayModule) leftClick(key string) {
	e, ok := m.entries[key]
	if !ok || m.tray == nil {
		return
	}
	if e.item.ItemIsMenu {
		m.call("context menu", key, m.tray.ContextMenu)
		return
	}
	m.call("activate", key, m.tray.Activate)
}

// middleClick is SecondaryActivate.
func (m *systrayModule) middleClick(key string) {
	if m.tray != nil {
		m.call("secondary activate", key, m.tray.SecondaryActivate)
	}
}

// rightClick is request_menu_show: a second right-click closes the
// open menu; otherwise AboutToShow and a fresh layout, then the menu -
// or the item's own ContextMenu when it publishes none. The menu's
// change feed starts before AboutToShow, so a layout the app changes in
// answer to it still reaches the open menu.
func (m *systrayModule) rightClick(key string) {
	if m.menu != nil && m.menu.key == key && m.menu.pop != nil && !m.menu.pop.Closed() {
		m.closeMenu()
		return
	}
	e, ok := m.entries[key]
	if !ok || m.tray == nil {
		return
	}
	it := e.item
	go func() {
		feed, stop := m.tray.WatchMenu(key)
		open := &trayMenu{key: key, stop: stop}
		ctx, cancel := context.WithTimeout(context.Background(), trayCallTimeout)
		menu, err := m.tray.OpenMenu(ctx, it)
		cancel()
		m.ctx.Invoke(func() { m.showMenu(open, menu, err) })
		m.followMenu(open, it, feed)
	}()
}

// showMenu opens the popover over the item, or falls back to the
// item's ContextMenu for an absent or empty menu, releasing the
// menu's change feed.
func (m *systrayModule) showMenu(open *trayMenu, menu sni.MenuItem, err error) {
	e, ok := m.entries[open.key]
	if !ok {
		open.stop()
		return
	}
	if err != nil || len(menu.Children) == 0 {
		open.stop()
		m.call("context menu", open.key, m.tray.ContextMenu)
		return
	}
	m.closeMenu()
	it := e.item
	open.items = m.menuRows(it, menu.Children)
	if m.ctx.App != nil && m.host != nil {
		font, px := dropdownFont(m.ctx)
		// Submenus open as their own popovers beside their rows, as the
		// Rust tray's PopoverMenu with the NESTED flag does.
		pop, err := m.ctx.App.OpenMenuPopover(m.host, trayMenuConfig(e.button, font, px, open.items, func() {
			open.stop()
			m.unbindAccels()
		}))
		if err != nil {
			open.stop()
			log.Printf("systray: %s menu: %v", it.ID, err)
			return
		}
		open.pop = pop
	}
	m.menu = open
	m.bindAccels(it, menu.Children)
}

// followMenu re-reads the open menu's layout on each change until its
// feed closes or the module retires. It never calls AboutToShow: the
// menu is already shown, and an app that answers AboutToShow with
// LayoutUpdated would keep it refreshing forever.
func (m *systrayModule) followMenu(open *trayMenu, it sni.Item, feed <-chan struct{}) {
	for {
		select {
		case <-m.ctx.gen.ended():
			open.stop()
			return
		case _, ok := <-feed:
			if !ok {
				return
			}
		}
		m.reread(open, it)
	}
}

// reread reads the open menu's layout again and shows it, unless
// another menu replaced it meanwhile.
func (m *systrayModule) reread(open *trayMenu, it sni.Item) {
	ctx, cancel := context.WithTimeout(context.Background(), trayCallTimeout)
	fresh, err := m.tray.MenuLayout(ctx, it)
	cancel()
	if err != nil {
		log.Printf("systray: %s menu layout: %v", it.ID, err)
		return
	}
	m.ctx.Invoke(func() {
		if m.menu != open {
			return
		}
		open.items = m.menuRows(it, fresh.Children)
		if open.pop != nil {
			open.pop.SetItems(open.items...)
		}
		m.bindAccels(it, fresh.Children)
	})
}

// submenuOpener is a submenu row's OnOpen: AboutToShow for that
// submenu, and the layout read again when the app asks for it.
func (m *systrayModule) submenuOpener(it sni.Item, id int32) func() {
	return func() {
		open := m.menu
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), trayCallTimeout)
			need, err := m.tray.SubmenuOpening(ctx, it, id)
			cancel()
			if err != nil {
				log.Printf("systray: %s submenu %d: %v", it.ID, id, err)
				return
			}
			if need {
				m.reread(open, it)
			}
		}()
	}
}

// closeMenu dismisses the open menu and releases its change feed.
func (m *systrayModule) closeMenu() {
	if m.menu == nil {
		return
	}
	if m.menu.pop != nil {
		m.menu.pop.Dismiss()
	}
	m.menu.stop()
	m.menu = nil
}

// menuRows is the gtk4 adapter's build_model: invisible rows drop,
// separators split sections (never leading, trailing, or doubled),
// labels lose their leading mnemonic underscores, toggles become
// check/radio rows, disabled rows stay visible but inert, submenus
// nest, and every leaf sends the clicked event.
func (m *systrayModule) menuRows(it sni.Item, nodes []sni.MenuItem) []widget.MenuItem {
	_, px := dropdownFont(m.ctx)
	var rows []widget.MenuItem
	pendingSep := false
	for _, n := range nodes {
		if !n.Visible {
			continue
		}
		if n.Separator {
			pendingSep = len(rows) > 0
			continue
		}
		if pendingSep {
			rows = append(rows, widget.MenuSeparator())
			pendingSep = false
		}
		row := widget.MenuItem{
			Label:    strings.TrimLeft(n.Label, "_"),
			Disabled: !n.Enabled,
			Accel:    trayAccel(n.Shortcut),
			Icon:     trayMenuIcon(n, int(px)),
		}
		if len(n.Children) > 0 {
			row.Items = m.menuRows(it, n.Children)
			row.OnOpen = m.submenuOpener(it, n.ID)
			rows = append(rows, row)
			continue
		}
		switch n.Toggle {
		case sni.ToggleCheckmark:
			row.Kind, row.Checked = widget.ItemCheck, n.ToggleState == sni.ToggleChecked
		case sni.ToggleRadio:
			row.Kind, row.Checked, row.Group = widget.ItemRadio, n.ToggleState == sni.ToggleChecked, "radio"
		}
		row.OnClick = m.clicker(it, n.ID)
		rows = append(rows, row)
	}
	return rows
}

// clicker sends a leaf's clicked event, for its row and its shortcut.
func (m *systrayModule) clicker(it sni.Item, id int32) func() {
	return func() {
		m.call("menu event", it.Key(), func(ctx context.Context, item sni.Item) error {
			return m.tray.MenuClicked(ctx, item, id)
		})
	}
}

// binder is the accelerator table: the injected one, else the app's.
func (m *systrayModule) binder() accelBinder {
	if m.accels != nil {
		return m.accels
	}
	if m.ctx.App != nil {
		return m.ctx.App
	}
	return nil
}

// bindAccels is register_accelerators: every visible, enabled leaf with
// a shortcut fires its clicked event on that key while the menu is
// open. A rebuilt menu drops the previous bindings first; a key already
// taken is skipped.
func (m *systrayModule) bindAccels(it sni.Item, nodes []sni.MenuItem) {
	m.unbindAccels()
	b := m.binder()
	if b == nil {
		return
	}
	var walk func([]sni.MenuItem)
	walk = func(nodes []sni.MenuItem) {
		for _, n := range nodes {
			if !n.Visible || n.Separator {
				continue
			}
			if len(n.Children) > 0 {
				walk(n.Children)
				continue
			}
			spec := trayAccelSpec(n.Shortcut)
			if spec == "" || !n.Enabled {
				continue
			}
			action := fmt.Sprintf("systray.%s.%d", it.Key(), n.ID)
			if err := b.AddAccel(spec, widget.NewAction(action, m.clicker(it, n.ID))); err != nil {
				log.Printf("systray: %s shortcut %s: %v", it.ID, spec, err)
				continue
			}
			m.accelKeys = append(m.accelKeys, spec)
		}
	}
	walk(nodes)
}

// unbindAccels is clear_accelerators.
func (m *systrayModule) unbindAccels() {
	if b := m.binder(); b != nil {
		for _, spec := range m.accelKeys {
			_ = b.RemoveAccel(spec)
		}
	}
	m.accelKeys = nil
}

// trayAccelSpec is to_gtk_accelerator: the first shortcut as a GTK
// accelerator ("<Control><Shift>q"), modifiers outside Control, Shift,
// Alt and Super dropped; empty without one.
func trayAccelSpec(shortcut [][]string) string {
	if len(shortcut) == 0 || len(shortcut[0]) == 0 {
		return ""
	}
	keys := shortcut[0]
	var b strings.Builder
	for _, mod := range keys[:len(keys)-1] {
		switch mod {
		case "Control", "Shift", "Alt", "Super":
			b.WriteString("<" + mod + ">")
		}
	}
	return b.String() + keys[len(keys)-1]
}

// trayMenuIcon is a row's icon: icon-name from the theme, else the PNG
// icon-data; nil without either.
func trayMenuIcon(n sni.MenuItem, px int) *widget.Icon {
	if n.IconName != "" {
		return widget.NewThemeIconFallback(n.IconName, px)
	}
	if len(n.IconData) > 0 {
		if ic, err := render.LoadPNG(n.IconData, px, px); err == nil {
			return widget.NewIcon(ic)
		}
	}
	return nil
}

// trayAccel renders the first shortcut as an accelerator label
// (to_gtk_accelerator's modifier set: Control, Shift, Alt, Super; other
// modifiers drop).
func trayAccel(shortcut [][]string) string {
	if len(shortcut) == 0 || len(shortcut[0]) == 0 {
		return ""
	}
	keys := shortcut[0]
	var parts []string
	for _, mod := range keys[:len(keys)-1] {
		switch mod {
		case "Control":
			parts = append(parts, "Ctrl")
		case "Shift", "Alt", "Super":
			parts = append(parts, mod)
		}
	}
	return strings.Join(append(parts, keys[len(keys)-1]), "+")
}

func (m *systrayModule) Root() widget.Widget { return m.root }

// trayMenuConfig is a tray item's menu: it opens from the item's button
// at lys-ui's popover gap.
func trayMenuConfig(anchor widget.Boundser, font render.Font, px float64, rows []widget.MenuItem, onClosed func()) app.MenuPopoverConfig {
	return app.MenuPopoverConfig{
		Anchor:   anchor,
		Face:     font,
		SizePx:   px,
		Items:    rows,
		Gap:      widget.PopoverGap,
		OnClosed: onClosed,
	}
}
