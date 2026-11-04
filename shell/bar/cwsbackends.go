package bar

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/stubbedev/lys-lib/intconv"

	"github.com/stubbedev/lys-shell/internal/appicons"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sway"
)

// newSwayWorkspaces is the sway-workspaces factory.
func newSwayWorkspaces(ctx ModuleContext) (Module, error) {
	if err := requireCompositor("sway-workspaces", compositorSway); err != nil {
		return nil, err
	}
	cfg := ctx.Config.SwayWorkspaces.View()
	return newCwsModule(ctx, "sway", cwsWorkspaces, cfg, &connectingBackend{connect: func() (cwsBackend, error) {
		conn, err := sway.Connect()
		if err != nil {
			return nil, err
		}
		return &swayBackend{conn: conn, titles: cwsFollowsTitles(cfg)}, nil
	}}), nil
}

// newNiriWorkspaces is the niri-workspaces factory.
func newNiriWorkspaces(ctx ModuleContext) (Module, error) {
	if err := requireCompositor("niri-workspaces", compositorNiri); err != nil {
		return nil, err
	}
	return newCwsModule(ctx, "niri", cwsWorkspaces, ctx.Config.NiriWorkspaces.View(), &connectingBackend{connect: func() (cwsBackend, error) {
		conn, err := niri.Connect()
		if err != nil {
			return nil, err
		}
		return &niriBackend{conn: conn}, nil
	}}), nil
}

var errBackendClosed = errors.New("the workspaces module was stopped")

type connectingBackend struct {
	connect func() (cwsBackend, error)

	mu     sync.Mutex
	conn   cwsBackend
	closed bool
}

func (b *connectingBackend) connected() (cwsBackend, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errBackendClosed
	}
	if b.conn != nil {
		return b.conn, nil
	}
	conn, err := b.connect()
	if err != nil {
		return nil, err
	}
	b.conn = conn
	return conn, nil
}

func (b *connectingBackend) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	conn, err := b.connected()
	if err != nil {
		return nil, nil, err
	}
	return conn.snapshot()
}

func (b *connectingBackend) focus(ctx context.Context, ws cwsWorkspace) error {
	conn, err := b.connected()
	if err != nil {
		return err
	}
	return conn.focus(ctx, ws)
}

func (b *connectingBackend) focusNext(ctx context.Context) error {
	conn, err := b.connected()
	if err != nil {
		return err
	}
	return conn.focusNext(ctx)
}

func (b *connectingBackend) focusPrevious(ctx context.Context) error {
	conn, err := b.connected()
	if err != nil {
		return err
	}
	return conn.focusPrevious(ctx)
}

func (b *connectingBackend) focusLast(ctx context.Context) error {
	conn, err := b.connected()
	if err != nil {
		return err
	}
	return conn.focusLast(ctx)
}

func (b *connectingBackend) subscribe() (<-chan struct{}, func(), error) {
	conn, err := b.connected()
	if err != nil {
		return nil, nil, err
	}
	return conn.subscribe()
}

func (b *connectingBackend) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.conn != nil {
		b.conn.close()
	}
}

// swayBackend maps sway's GET_WORKSPACES/GET_TREE onto the shared
// model: occupied means any window's enclosing workspace, active is
// sway's visible flag, and every workspace carries its name and
// output.
type swayBackend struct {
	conn   *sway.Conn
	titles bool
	mu     sync.Mutex
	byID   map[uint64]sway.Workspace
}

func (b *swayBackend) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	replies, err := b.conn.Workspaces()
	if err != nil {
		return nil, nil, err
	}
	tree, err := b.conn.Windows()
	if err != nil {
		return nil, nil, err
	}
	windows := make([]cwsWindow, 0, len(tree))
	occupied := map[uint64]bool{}
	for _, w := range tree {
		cw := cwsWindow{
			id:           uint64(w.ID),
			workspace:    uint64(w.WorkspaceID),
			hasWorkspace: w.HasWorkspace,
			urgent:       w.Urgent,
			app:          appicons.Window{AppID: w.AppID, HasAppID: w.HasAppID, Title: w.Title, HasTitle: w.HasTitle},
		}
		if w.HasWorkspace {
			occupied[cw.workspace] = true
		}
		windows = append(windows, cw)
	}
	byID := make(map[uint64]sway.Workspace, len(replies))
	workspaces := make([]cwsWorkspace, 0, len(replies))
	for _, r := range replies {
		id := uint64(r.ID)
		byID[id] = r
		workspaces = append(workspaces, cwsWorkspace{
			id: id, num: r.Num, name: r.Name, hasName: true,
			output: r.Output, hasOutput: true,
			urgent: r.Urgent, active: r.Visible, focused: r.Focused,
			hasWindows: occupied[id],
		})
	}
	b.mu.Lock()
	b.byID = byID
	b.mu.Unlock()
	return workspaces, windows, nil
}

// focus is focus_workspace(WorkspaceRef::Id): resolved against the
// snapshot, or a con_id focus for an id the snapshot lost.
func (b *swayBackend) focus(ctx context.Context, ws cwsWorkspace) error {
	b.mu.Lock()
	reply, ok := b.byID[ws.id]
	b.mu.Unlock()
	if !ok {
		return b.conn.RunCommand(ctx, "[con_id="+strconv.FormatUint(ws.id, 10)+"] focus")
	}
	return b.conn.FocusWorkspace(ctx, reply)
}

func (b *swayBackend) focusNext(ctx context.Context) error { return b.conn.FocusNextOnOutput(ctx) }

func (b *swayBackend) focusPrevious(ctx context.Context) error { return b.conn.FocusPrevOnOutput(ctx) }

func (b *swayBackend) focusLast(ctx context.Context) error { return b.conn.FocusBackAndForth(ctx) }

func (b *swayBackend) subscribe() (<-chan struct{}, func(), error) {
	if b.titles {
		return sway.Subscribe()
	}
	return sway.SubscribeWithoutTitles()
}
func (b *swayBackend) close() { _ = b.conn.Close() }

// niriBackend maps niri's Workspaces/Windows: occupied is an active
// window id, the idx is the number, and windows order by their
// scrolling-layout position.
type niriBackend struct {
	conn *niri.Conn
}

func (b *niriBackend) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	replies, err := b.conn.Workspaces()
	if err != nil {
		return nil, nil, err
	}
	all, err := b.conn.Windows()
	if err != nil {
		return nil, nil, err
	}
	workspaces := make([]cwsWorkspace, 0, len(replies))
	for _, r := range replies {
		ws := cwsWorkspace{
			id: r.ID, num: int(r.Idx),
			urgent: r.IsUrgent, active: r.IsActive, focused: r.IsFocused,
			hasWindows: r.ActiveWindowID != nil,
		}
		if r.Name != nil {
			ws.name, ws.hasName = *r.Name, true
		}
		if r.Output != nil {
			ws.output, ws.hasOutput = *r.Output, true
		}
		workspaces = append(workspaces, ws)
	}
	windows := make([]cwsWindow, 0, len(all))
	for _, w := range all {
		cw := cwsWindow{id: w.ID, urgent: w.IsUrgent}
		if w.WorkspaceID != nil {
			cw.workspace, cw.hasWorkspace = *w.WorkspaceID, true
		}
		if w.AppID != nil {
			cw.app.AppID, cw.app.HasAppID = *w.AppID, true
		}
		if w.Title != nil {
			cw.app.Title, cw.app.HasTitle = *w.Title, true
		}
		// (column, tile), floating windows (no position) last.
		cw.order = [2]int{int(^uint(0) >> 1), 0}
		if pos := w.Layout.PosInScrollingLayout; pos != nil {
			cw.order = *pos
		}
		windows = append(windows, cw)
	}
	return workspaces, windows, nil
}

func (b *niriBackend) focus(ctx context.Context, ws cwsWorkspace) error {
	return b.conn.FocusWorkspaceID(ctx, ws.id)
}

func (b *niriBackend) focusNext(ctx context.Context) error { return b.conn.FocusWorkspaceDown(ctx) }

func (b *niriBackend) focusPrevious(ctx context.Context) error { return b.conn.FocusWorkspaceUp(ctx) }

func (b *niriBackend) focusLast(ctx context.Context) error {
	return b.conn.FocusWorkspacePrevious(ctx)
}
func (b *niriBackend) subscribe() (<-chan struct{}, func(), error) { return niri.Subscribe() }
func (b *niriBackend) close()                                      { _ = b.conn.Close() }

// newMangoWorkspaces is the mango-workspaces factory: the watcher runs
// from construction, like MangoService::new's start_monitoring.
func newMangoWorkspaces(ctx ModuleContext) (Module, error) {
	if err := requireCompositor("mango-workspaces", compositorMango); err != nil {
		return nil, err
	}
	cfg := ctx.Config.MangoWorkspaces
	return newCwsModule(ctx, "mango", cwsTags, cfg.View(), &connectingBackend{connect: func() (cwsBackend, error) {
		w, err := mango.Watch()
		if err != nil {
			return nil, err
		}
		return &mangoBackend{watch: w, connector: ctx.Connector, hideEmpty: cfg.HideEmpty, minTagCount: int(cfg.MinTagCount)}, nil
	}}), nil
}

// mangoBackend selects one monitor's tags (rebuild_tags): the bar's
// own monitor, else the active one, else the first; hide-empty keeps
// occupied, active, and the first min-tag-count tags. Clients map to
// one window per tag they sit on, in mango's list order.
type mangoBackend struct {
	watch       *mango.Watcher
	connector   string
	hideEmpty   bool
	minTagCount int
}

// chooseMonitor is chosen_monitor.
func chooseMonitor(monitors []mango.Monitor, connector string) (mango.Monitor, bool) {
	if connector != "" {
		for _, m := range monitors {
			if m.Name == connector {
				return m, true
			}
		}
	}
	for _, m := range monitors {
		if m.IsActive {
			return m, true
		}
	}
	if len(monitors) > 0 {
		return monitors[0], true
	}
	return mango.Monitor{}, false
}

func (b *mangoBackend) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	monitors, clients := b.watch.State()
	monitor, ok := chooseMonitor(monitors, b.connector)
	if !ok {
		return nil, nil, nil
	}
	tags := make([]cwsWorkspace, 0, len(monitor.Tags))
	for _, tag := range monitor.Tags {
		// displayed_tags
		if b.hideEmpty && tag.ClientCount == 0 && !tag.IsActive && int(tag.Index) > b.minTagCount {
			continue
		}
		tags = append(tags, cwsWorkspace{
			id: uint64(tag.Index), num: int(tag.Index),
			output: monitor.Name, hasOutput: true,
			urgent: tag.IsUrgent, active: tag.IsActive,
			hasWindows: tag.ClientCount > 0,
		})
	}
	windows := make([]cwsWindow, 0, len(clients))
	for pos, c := range clients {
		if c.Monitor != monitor.Name {
			continue
		}
		for _, tag := range c.Tags {
			windows = append(windows, cwsWindow{
				id: uint64(c.ID), workspace: uint64(tag), hasWorkspace: true,
				urgent: c.IsUrgent, order: [2]int{pos, 0},
				app: appicons.Window{AppID: c.AppID, HasAppID: c.HasAppID, Title: c.Title, HasTitle: c.HasTitle},
			})
		}
	}
	return tags, windows, nil
}

func (b *mangoBackend) focus(_ context.Context, ws cwsWorkspace) error {
	tag, err := intconv.Exact[uint32](ws.id)
	if err != nil {
		return fmt.Errorf("mango: tag %d: %w", ws.id, err)
	}
	return mango.ViewTag(tag)
}
func (b *mangoBackend) focusNext(context.Context) error     { return mango.ViewRight() }
func (b *mangoBackend) focusPrevious(context.Context) error { return mango.ViewLeft() }

// focusLast is a no-op: mango has no last-tag action.
func (b *mangoBackend) focusLast(context.Context) error { return nil }

func (b *mangoBackend) subscribe() (<-chan struct{}, func(), error) {
	return b.watch.Ticks(), b.watch.Close, nil
}
func (b *mangoBackend) close() { b.watch.Close() }

func newLysWMWorkspaces(ctx ModuleContext) (Module, error) {
	if err := requireCompositor("lys-wm-workspaces", compositorLysWM); err != nil {
		return nil, err
	}
	cfg := ctx.Config.LysWMWorkspaces.View()
	var titles *lysWMTitles
	if cwsFollowsTitles(cfg) {
		titles = &lysWMTitles{}
	}
	return newCwsModule(ctx, "lys-wm", cwsWorkspaces, cfg, lysWMBackend{titles: titles}), nil
}

var lysWMWorkspaceEvents = []lyswm.EventKind{
	lyswm.EventOutput, lyswm.EventWorkspace, lyswm.EventFocusedOutput,
	lyswm.EventOpen, lyswm.EventClose, lyswm.EventMove, lyswm.EventUrgent,
}

var errNoPreviousWorkspace = errors.New("lys-wm has no action that returns to the previous workspace")

type lysWMBackend struct{ titles *lysWMTitles }

type lysWMTitles struct {
	hidden atomic.Pointer[map[uint64]bool]
}

func (t *lysWMTitles) hides(window uint64) bool {
	hidden := t.hidden.Load()
	return hidden != nil && (*hidden)[window]
}

func (b lysWMBackend) hideTitles(shown []cwsButtonModel, windows []cwsWindow) {
	if b.titles == nil {
		return
	}
	hidden := make(map[uint64]bool, len(windows))
	for _, w := range windows {
		hidden[w.id] = !w.hasWorkspace || !slices.ContainsFunc(shown, func(m cwsButtonModel) bool { return m.ws.id == w.workspace })
	}
	b.titles.hidden.Store(&hidden)
}

func (lysWMBackend) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	replies, err := lyswm.Workspaces()
	if err != nil {
		return nil, nil, err
	}
	all, err := lyswm.Windows()
	if err != nil {
		return nil, nil, err
	}
	workspaces, windows := lysWMModels(replies, all)
	return workspaces, windows, nil
}

func lysWMModels(replies []lyswm.Workspace, all []lyswm.Window) ([]cwsWorkspace, []cwsWindow) {
	urgent := map[int]bool{}
	windows := make([]cwsWindow, 0, len(all))
	for _, w := range all {
		urgent[w.Workspace] = urgent[w.Workspace] || w.Urgent
		windows = append(windows, cwsWindow{
			id: w.ID, workspace: uint64(w.Workspace), hasWorkspace: true, urgent: w.Urgent,
			app: appicons.Window{AppID: w.AppID, HasAppID: w.AppID != "", Title: w.Title, HasTitle: w.Title != ""},
		})
	}
	workspaces := make([]cwsWorkspace, 0, len(replies))
	for _, r := range replies {
		workspaces = append(workspaces, cwsWorkspace{
			id: uint64(r.ID), num: r.ID,
			output: r.Output, hasOutput: true,
			urgent: urgent[r.ID], active: r.Visible, focused: r.Focused,
			hasWindows: r.Windows > 0,
		})
	}
	return workspaces, windows
}

func (lysWMBackend) focus(_ context.Context, ws cwsWorkspace) error {
	return lyswm.Dispatch("workspace " + strconv.Itoa(ws.num))
}

func (lysWMBackend) focusNext(context.Context) error { return lyswm.Dispatch("workspace e+1") }

func (lysWMBackend) focusPrevious(context.Context) error { return lyswm.Dispatch("workspace e-1") }

func (lysWMBackend) focusLast(context.Context) error { return errNoPreviousWorkspace }

func (b lysWMBackend) subscribe() (<-chan struct{}, func(), error) {
	if b.titles == nil {
		w, err := lyswm.Watch(lysWMWorkspaceEvents...)
		if err != nil {
			return nil, nil, err
		}
		return w.Ticks(), w.Close, nil
	}
	w, err := lyswm.WatchTitles(b.titles.hides, func() { b.titles.hidden.Store(nil) }, lysWMWorkspaceEvents...)
	if err != nil {
		return nil, nil, err
	}
	return w.Ticks(), w.Close, nil
}

func (lysWMBackend) close() {}
