package bar

import (
	"errors"
	"slices"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

var errNoWindowList = errors.New("minimized-windows: the compositor offers no wlr foreign-toplevel window list")

// minimizedWindowsModule lists the minimized windows as their apps'
// icons, like a dock:
//
//	box.minimized-windows
//	└ button.task-item...   (image, the window title as tooltip)
//
// A click restores and focuses the window, a middle click closes it and a
// right click offers both. Each window names its entry, or the slot its
// entry will take, as its minimize rectangle, so it minimizes into its
// icon and comes back out of it.
type minimizedWindowsModule struct {
	ctx      ModuleContext
	cfg      config.MinimizedWindowsConfig
	hub      *taskHub
	root     *arrangedBox
	axis     widget.Axis
	px       int
	entries  map[taskWindow]*taskButton
	shown    []taskWindow
	empty    *taskButton
	host     app.Host
	rects    *rectOwner
	unfollow func()
}

func newMinimizedWindows(ctx ModuleContext) (Module, error) {
	if ctx.Tasks == nil {
		return nil, errNoWindowList
	}
	m := &minimizedWindowsModule{
		ctx:     ctx,
		cfg:     ctx.Config.MinimizedWindows,
		hub:     ctx.Tasks,
		axis:    widget.Row,
		entries: map[taskWindow]*taskButton{},
	}
	if ctx.Config.Bar.Location.IsVertical() {
		m.axis = widget.Column
	}
	m.px = taskIconPx(m.cfg.IconSize, ctx.Config)
	m.root = &arrangedBox{Box: widget.NewBox(m.axis, trayItemGap(ctx), 0), arranged: m.claim}
	m.root.AddClass("minimized-windows")
	m.rects = m.hub.windows.owner()
	m.unfollow = m.hub.windows.subscribe(m.render)
	ctx.OnRetire(func() {
		m.unfollow()
		m.rects.release()
	})
	m.render()
	return m, nil
}

// Attach keeps the bar surface the entries' rectangles and menus are on.
func (m *minimizedWindowsModule) Attach(host app.Host) { m.host = host }

// ownsChrome keeps the row out of the module-button wrapper: each entry
// is a button of its own.
func (m *minimizedWindowsModule) ownsChrome() {}

func (m *minimizedWindowsModule) Root() widget.Widget { return m.root }

// render shows an entry per minimized window in scope, in the order
// they were minimized, reusing each window's entry.
func (m *minimizedWindowsModule) render() {
	windows := m.hub.windows.minimized(m.ctx.Connector, m.cfg.Scope == config.WindowScopeAll)
	for w := range m.entries {
		if !slices.Contains(windows, w) {
			delete(m.entries, w)
		}
	}
	m.root.Clear()
	for _, w := range windows {
		e := m.entries[w]
		if e == nil {
			e = m.newEntry(w)
			m.entries[w] = e
		}
		e.setIcon(m.hub.appIcon(w.AppID(), m.cfg.Icon), m.cfg.Icon)
		e.SetTooltip(m.hub.windowTooltip(w))
		m.root.Append(e, false)
	}
	m.shown = windows
	if len(windows) == 0 && m.cfg.ShowWhenEmpty {
		m.root.Append(m.emptyEntry(), false)
	}
	m.root.SetVisible(len(windows) > 0 || m.cfg.ShowWhenEmpty)
}

func (m *minimizedWindowsModule) newEntry(w taskWindow) *taskButton {
	e := newTaskButton(m.ctx, m.px, "task-item")
	e.OnClick = w.Activate
	e.SetOnPointer(func(ev widget.PointerEvent) bool {
		switch ev.Button {
		case widget.BTNMiddle:
			w.Close()
		case widget.BTNRight:
			openTaskMenu(m.ctx, m.host, e, []widget.MenuItem{
				menuItem(i18n.T("bar-task-restore"), w.Activate),
				menuItem(i18n.T("bar-task-close"), w.Close),
			})
		default:
			return false
		}
		return true
	})
	return e
}

// emptyEntry is the module's icon while nothing is minimized.
func (m *minimizedWindowsModule) emptyEntry() *taskButton {
	if m.empty == nil {
		m.empty = newTaskButton(m.ctx, m.px, "task-item")
		m.empty.AddClass("task-empty")
		m.empty.setIcon(m.cfg.Icon, m.cfg.Icon)
		m.empty.SetTooltip(i18n.T("bar-task-none"))
	}
	return m.empty
}

// claim names each shown window's entry as its rectangle, and the slot
// after the last entry for every other window in scope, the one a window
// minimized next takes.
func (m *minimizedWindowsModule) claim() {
	if m.host == nil {
		return
	}
	claims := map[taskWindow]rectClaim{}
	at := func(r render.Rect) rectClaim {
		return rectClaim{priority: rectFromModule, output: m.ctx.Connector, host: m.host, rect: r}
	}
	var last render.Rect
	for _, w := range m.shown {
		last = m.entries[w].Bounds()
		claims[w] = at(last)
	}
	next := last
	switch {
	case len(m.shown) > 0:
		next = nextSlot(last, m.axis, m.root.Spacing())
	case m.empty != nil && m.cfg.ShowWhenEmpty:
		next = m.empty.Bounds()
	}
	if !next.Empty() {
		everywhere := m.cfg.Scope == config.WindowScopeAll
		for _, w := range m.hub.windows.windows() {
			if _, ok := claims[w]; !ok && !w.Minimized() && inScope(w, m.ctx.Connector, everywhere) {
				claims[w] = at(next)
			}
		}
	}
	m.rects.set(claims)
}
