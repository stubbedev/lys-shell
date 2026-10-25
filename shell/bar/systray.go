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

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
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
	// trayPixmapTarget is the size select_best_pixmap aims for.
	trayPixmapTarget = 24
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

// bestPixmap is select_best_pixmap: the one closest to 24x24 by summed
// width and height distance, the first on ties.
func bestPixmap(pixmaps []sni.Pixmap) (sni.Pixmap, bool) {
	best, bestDist := sni.Pixmap{}, math.MaxInt
	for _, p := range pixmaps {
		d := absInt(int(p.Width)-trayPixmapTarget) + absInt(int(p.Height)-trayPixmapTarget)
		if d < bestDist {
			best, bestDist = p, d
		}
	}
	return best, bestDist != math.MaxInt
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
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

// trayIconSource is the resolved source of an item's glyph, and its
// identity for change detection (item/mod.rs IconSignature).
type trayIconSource struct {
	kind   string // "file", "named", "pixmap", "fallback"
	name   string
	pixmap sni.Pixmap
}

// signature identifies the source: equal sources keep the widget.
func (s trayIconSource) signature() string {
	if s.kind == "pixmap" {
		return "pixmap:" + strconv.Itoa(int(s.pixmap.Width)) + "x" + strconv.Itoa(int(s.pixmap.Height)) + ":" + string(s.pixmap.Data)
	}
	return s.kind + ":" + s.name
}

// resolveTrayIcon is update_icon's source pick: the override icon or
// the item's IconName - as a file in IconThemePath, a file path, or a
// theme name - else the best IconPixmap, else the fallback glyph.
func resolveTrayIcon(cfg config.SystrayConfig, it sni.Item) trayIconSource {
	name := it.IconName
	if o, ok := systrayOverride(cfg.Overrides, it); ok && o.Icon != nil && *o.Icon != "" {
		name = *o.Icon
	}
	if name != "" {
		if file, ok := themePathIcon(it.IconThemePath, name); ok {
			return trayIconSource{kind: "file", name: file}
		}
		if isFile(name) {
			return trayIconSource{kind: "file", name: name}
		}
		return trayIconSource{kind: "named", name: name}
	}
	if p, ok := bestPixmap(it.IconPixmap); ok {
		return trayIconSource{kind: "pixmap", pixmap: p}
	}
	return trayIconSource{kind: "fallback", name: trayFallbackIcon}
}

// buildTrayIcon makes the widget for a source at px logical pixels; a
// pixmap that does not decode falls back to the generic glyph.
func buildTrayIcon(src trayIconSource, px int) *widget.Icon {
	switch src.kind {
	case "file":
		return widget.NewFileIcon(src.name, px)
	case "pixmap":
		p := src.pixmap
		if ic, err := render.IconFromARGB32(int(p.Width), int(p.Height), p.Data, px, px); err == nil {
			return widget.NewIcon(ic)
		}
		return widget.NewThemeIcon(trayFallbackIcon, px)
	}
	return widget.NewThemeIconFallback(src.name, px)
}

// trayEntry is one rendered item.
type trayEntry struct {
	item   sni.Item
	button *widget.Button
	slot   *widget.Box
	icon   *widget.Icon
	sig    string
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
	}
	m.root.AddClass("systray")
	ticks, stop := ctx.SNI.Subscribe()
	follow(ctx, ticks, stop, func(struct{}) { m.refresh() })
	m.refresh()
	return m, nil
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

// updateIcon applies the resolved source and the override tint.
func (m *systrayModule) updateIcon(e *trayEntry) {
	cfg := m.ctx.Config.Systray
	src := resolveTrayIcon(cfg, e.item)
	if sig := src.signature(); sig != e.sig || e.icon == nil {
		e.icon = buildTrayIcon(src, m.iconPx())
		e.slot.Clear()
		e.slot.Append(widgets.Center(e.icon), true)
		e.sig = sig
	}
	var tint render.Color
	if o, ok := systrayOverride(cfg.Overrides, e.item); ok && o.Color != nil {
		tint, _ = styling.ResolveColor(*o.Color, m.ctx.Style.palette)
	}
	e.icon.SetTint(tint)
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
