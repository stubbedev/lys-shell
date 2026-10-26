package toplevels

import (
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/wlr"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/swaytest"
)

// A window is listed from its first done batch, with the batch's app id
// and activated state; a done that changes nothing announces nothing,
// and a closed window leaves the list and frees its handle.
func TestTrackerAppliesBatchesOnDone(t *testing.T) {
	tr := newTracker()
	var changes atomic.Int32
	tr.changed = func() { changes.Add(1) }
	destroyed := false
	h := tr.add(func() { destroyed = true })
	h.HandleZwlrForeignToplevelHandleV1AppId(wlr.ZwlrForeignToplevelHandleV1AppIdEvent{AppId: "org.example.App"})
	h.HandleZwlrForeignToplevelHandleV1State(wlr.ZwlrForeignToplevelHandleV1StateEvent{State: []int32{wlr.ZwlrForeignToplevelHandleV1StateMaximized, wlr.ZwlrForeignToplevelHandleV1StateActivated}})
	if got := tr.windows(); len(got) != 0 || changes.Load() != 0 {
		t.Fatalf("before done: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if got := tr.windows(); !slices.Equal(got, []Window{{AppID: "org.example.App", Activated: true}}) || changes.Load() != 1 {
		t.Errorf("after done: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if changes.Load() != 1 {
		t.Errorf("an unchanged batch announced a change")
	}
	h.HandleZwlrForeignToplevelHandleV1State(wlr.ZwlrForeignToplevelHandleV1StateEvent{State: []int32{}})
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if got := tr.windows(); !slices.Equal(got, []Window{{AppID: "org.example.App"}}) || changes.Load() != 2 {
		t.Errorf("deactivated: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Closed(wlr.ZwlrForeignToplevelHandleV1ClosedEvent{})
	if got := tr.windows(); len(got) != 0 || changes.Load() != 3 || !destroyed {
		t.Errorf("closed: %v, %d changes, destroyed %v", got, changes.Load(), destroyed)
	}
	// A window closing before its first batch was never listed.
	early := tr.add(func() {})
	early.HandleZwlrForeignToplevelHandleV1Closed(wlr.ZwlrForeignToplevelHandleV1ClosedEvent{})
	if changes.Load() != 3 {
		t.Error("a window never listed announced its close")
	}
}

// waitWindows polls the watcher until want holds.
func waitWindows(t *testing.T, w *Watcher, want func([]Window) bool) []Window {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got := w.Windows()
		if want(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("windows = %v", got)
		}
	}
}

// startProbe maps a window under appID on a connection of its own, a
// separate client as a real app is; closing the returned closer closes
// the window and ends its loop.
func startProbe(t *testing.T, appID string) io.Closer {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	a := app.NewApplication(sess)
	win, err := a.NewWindow(app.WindowConfig{Title: "probe", AppID: appID, Width: 200, Height: 100, Root: widget.NewBox(widget.Column, 0, 0)})
	if err != nil {
		sess.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := a.Run(); err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the probe window %s stopped: %v", appID, err)
		}
	}()
	var once sync.Once
	stop := closer(func() {
		once.Do(func() {
			a.Invoke(win.Close)
			<-done
			sess.Close()
		})
	})
	t.Cleanup(func() { _ = stop.Close() })
	return stop
}

// closer is a func as an io.Closer.
type closer func()

func (c closer) Close() error {
	c()
	return nil
}

// Against a real compositor: a window that maps is listed under its app
// id and activated, and leaves the list when it closes; Close ends the
// watcher without an error.
func TestWatcherFollowsAWindow(t *testing.T) {
	swaytest.Start(t)
	var changes atomic.Int32
	w, err := Watch(func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Windows(); len(got) != 0 {
		t.Fatalf("an empty session lists %v", got)
	}
	probe := startProbe(t, "org.example.Probe")
	waitWindows(t, w, func(ws []Window) bool {
		return slices.Equal(ws, []Window{{AppID: "org.example.Probe", Activated: true}})
	})
	if changes.Load() == 0 {
		t.Error("the window mapped without a change")
	}
	_ = probe.Close()
	waitWindows(t, w, func(ws []Window) bool { return len(ws) == 0 })
	w.Close()
	if err := w.Err(); err != nil {
		t.Errorf("Close ended the watcher with %v", err)
	}
}

// A compositor going away ends the watcher with an error.
func TestWatcherEndsWithTheCompositor(t *testing.T) {
	sway := swaytest.Start(t)
	w, err := Watch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sway.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Ended():
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher outlived its compositor")
	}
	if w.Err() == nil {
		t.Error("the compositor went away without an error")
	}
}
