// Package toplevels follows the compositor's windows over
// zwlr_foreign_toplevel_management_v1 on a private Wayland connection:
// each window's app id and whether it is the activated one. lys-ui
// keeps its own foreign-toplevel list internal, so this binds the
// protocol directly.
package toplevels

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-lib/third_party/wayland/wlclient"
	"github.com/stubbedev/lys-lib/wlr"
)

const managerIface = "zwlr_foreign_toplevel_manager_v1"

// maxManagerVersion is the version bound: v3 is the newest the binding
// knows, and only app_id, state, done and closed are read.
const maxManagerVersion = 3

// ErrUnsupported is a compositor without the foreign-toplevel manager.
var ErrUnsupported = errors.New("toplevels: the compositor offers no " + managerIface)

// Window is one window as its latest done batch left it.
type Window struct {
	AppID     string
	Activated bool
}

// Watcher follows the windows until Close or until the compositor goes
// away, which closes Ended. One goroutine reads the connection and
// alone closes it: Close asks the compositor to stop, and the read
// loop ends on its finished event.
type Watcher struct {
	ctx      *wl.Context
	reg      *wl.Registry
	mgr      *wlr.ZwlrForeignToplevelManagerV1
	tracker  *tracker
	ended    chan struct{}
	stopping atomic.Bool
	// finished is the manager's finished event, on the read loop.
	finished bool
	err      error
}

// Watch connects to the compositor named by WAYLAND_DISPLAY and
// follows its windows; changed runs on the watcher's goroutine after
// every batch that changes what Windows reports.
func Watch(changed func()) (*Watcher, error) {
	display, err := wl.Connect("")
	if err != nil {
		return nil, fmt.Errorf("toplevels: connect: %w", err)
	}
	w := &Watcher{ctx: display.Context(), tracker: newTracker(), ended: make(chan struct{})}
	display.AddDeleteIdHandler(w)
	if w.reg, err = display.GetRegistry(); err != nil {
		_ = w.ctx.Close()
		return nil, fmt.Errorf("toplevels: registry: %w", err)
	}
	wlclient.RegistryAddListener(w.reg, w)
	// The first roundtrip binds the manager, the second brings every
	// window's first batch.
	for range 2 {
		if err := roundtrip(display); err != nil {
			_ = w.ctx.Close()
			return nil, fmt.Errorf("toplevels: initial roundtrip: %w", err)
		}
	}
	if w.mgr == nil {
		_ = w.ctx.Close()
		return nil, ErrUnsupported
	}
	w.tracker.changed = changed
	go w.dispatch()
	return w, nil
}

// Windows is every open window, in the order they appeared.
func (w *Watcher) Windows() []Window { return w.tracker.windows() }

// Ended closes once the connection is gone; Err then says why.
func (w *Watcher) Ended() <-chan struct{} { return w.ended }

// Err is why the connection ended, nil before it has or after Close.
func (w *Watcher) Err() error {
	select {
	case <-w.ended:
		return w.err
	default:
		return nil
	}
}

// Close stops following the windows and waits for the connection to
// close. When the stop cannot be sent the connection is already
// broken, and the read loop ends on that.
func (w *Watcher) Close() {
	w.stopping.Store(true)
	_ = w.mgr.Stop()
	<-w.ended
}

func (w *Watcher) dispatch() {
	defer close(w.ended)
	defer func() { _ = w.ctx.Close() }()
	for !w.finished {
		err := w.ctx.Run()
		if errors.Is(err, wl.ErrContextRunProxyNil) {
			continue
		}
		if err != nil {
			if !w.stopping.Load() {
				w.err = fmt.Errorf("toplevels: the compositor connection ended: %w", err)
			}
			return
		}
	}
}

// HandleRegistryGlobal binds the manager.
func (w *Watcher) HandleRegistryGlobal(ev wl.RegistryGlobalEvent) {
	if ev.Interface != managerIface || w.mgr != nil {
		return
	}
	mgr := wlr.NewZwlrForeignToplevelManagerV1(w.ctx)
	if err := w.reg.Bind(ev.Name, ev.Interface, min(ev.Version, maxManagerVersion), mgr); err != nil {
		return
	}
	mgr.AddToplevelHandler(w)
	mgr.AddFinishedHandler(w)
	w.mgr = mgr
}

// HandleRegistryGlobalRemove ignores globals going away; the manager's
// windows close on their own.
func (w *Watcher) HandleRegistryGlobalRemove(wl.RegistryGlobalRemoveEvent) {}

// HandleDisplayDeleteId frees a destroyed handle's id.
func (w *Watcher) HandleDisplayDeleteId(ev wl.DisplayDeleteIdEvent) {
	w.ctx.Unregister(wl.ProxyId(ev.Id))
}

// HandleZwlrForeignToplevelManagerV1Toplevel follows a new window.
func (w *Watcher) HandleZwlrForeignToplevelManagerV1Toplevel(ev wlr.ZwlrForeignToplevelManagerV1ToplevelEvent) {
	if ev.Toplevel == nil {
		return
	}
	h := w.tracker.add(func() { _ = ev.Toplevel.Destroy() })
	ev.Toplevel.AddAppIdHandler(h)
	ev.Toplevel.AddStateHandler(h)
	ev.Toplevel.AddDoneHandler(h)
	ev.Toplevel.AddClosedHandler(h)
}

// HandleZwlrForeignToplevelManagerV1Finished ends the read loop: the
// compositor sends no more after it.
func (w *Watcher) HandleZwlrForeignToplevelManagerV1Finished(wlr.ZwlrForeignToplevelManagerV1FinishedEvent) {
	w.finished = true
}

func roundtrip(display *wl.Display) error {
	cb, err := display.Sync()
	if err != nil {
		return err
	}
	return display.Context().RunTill(cb)
}

// tracker is the window list: each handle's pending batch, applied on
// its done, and removed on its closed. Its handlers run on the watcher
// goroutine; windows may be read from any.
type tracker struct {
	mu      sync.Mutex
	handles []*handle
	changed func()
}

func newTracker() *tracker { return &tracker{} }

// add starts following one window; destroy frees its handle once it
// closes.
func (t *tracker) add(destroy func()) *handle {
	h := &handle{t: t, destroy: destroy}
	t.mu.Lock()
	t.handles = append(t.handles, h)
	t.mu.Unlock()
	return h
}

func (t *tracker) windows() []Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Window, 0, len(t.handles))
	for _, h := range t.handles {
		if h.shown {
			out = append(out, h.applied)
		}
	}
	return out
}

// update applies fn under the lock and runs changed when it reports a
// change.
func (t *tracker) update(fn func() bool) {
	t.mu.Lock()
	changed := fn()
	t.mu.Unlock()
	if changed && t.changed != nil {
		t.changed()
	}
}

// handle is one window: the batch arriving and the one applied. A
// window is listed from its first done.
type handle struct {
	t       *tracker
	destroy func()
	pending Window
	applied Window
	shown   bool
}

// HandleZwlrForeignToplevelHandleV1AppId takes the app id into the
// batch.
func (h *handle) HandleZwlrForeignToplevelHandleV1AppId(ev wlr.ZwlrForeignToplevelHandleV1AppIdEvent) {
	h.t.mu.Lock()
	h.pending.AppID = ev.AppId
	h.t.mu.Unlock()
}

// HandleZwlrForeignToplevelHandleV1State takes the activated flag into
// the batch.
func (h *handle) HandleZwlrForeignToplevelHandleV1State(ev wlr.ZwlrForeignToplevelHandleV1StateEvent) {
	h.t.mu.Lock()
	h.pending.Activated = slices.Contains(ev.State, wlr.ZwlrForeignToplevelHandleV1StateActivated)
	h.t.mu.Unlock()
}

// HandleZwlrForeignToplevelHandleV1Done applies the batch.
func (h *handle) HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent) {
	h.t.update(func() bool {
		changed := !h.shown || h.pending != h.applied
		h.applied, h.shown = h.pending, true
		return changed
	})
}

// HandleZwlrForeignToplevelHandleV1Closed drops the window.
func (h *handle) HandleZwlrForeignToplevelHandleV1Closed(wlr.ZwlrForeignToplevelHandleV1ClosedEvent) {
	h.t.update(func() bool {
		i := slices.Index(h.t.handles, h)
		if i < 0 {
			return false
		}
		h.t.handles = slices.Delete(h.t.handles, i, i+1)
		return h.shown
	})
	h.destroy()
}
