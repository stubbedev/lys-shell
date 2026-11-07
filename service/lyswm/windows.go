package lyswm

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// windowEvents are the events that change a window the windows result
// reports.
var windowEvents = []EventKind{
	EventOpen, EventClose, EventMove, EventTitle, EventAppID, EventRect,
	EventUrgent, EventMinimized, EventStack, EventFocus,
}

// WindowsWatcher follows every window: seeded from the windows request
// once the stream is open, then kept by the window events alone, ticking
// after each. Each event sets what it reports, so one that the seed
// already holds changes nothing.
type WindowsWatcher struct {
	*Watcher
	mu      sync.Mutex
	windows []Window
}

// WatchWindows opens the event stream and follows the windows.
func WatchWindows() (*WindowsWatcher, error) {
	ww := &WindowsWatcher{}
	seed := func() error {
		windows, err := Windows()
		if err != nil {
			return fmt.Errorf("seed the windows: %w", err)
		}
		ww.windows = windows
		return nil
	}
	w, err := watch(windowEvents, seed, ww.apply, nil, nil)
	if err != nil {
		return nil, err
	}
	ww.Watcher = w
	return ww, nil
}

// Windows is every mapped window, oldest first, as the last event left
// them.
func (w *WindowsWatcher) Windows() []Window {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Window, len(w.windows))
	for i, win := range w.windows {
		out[i] = win
		if win.Stack != nil {
			out[i].Stack = new(*win.Stack)
		}
	}
	return out
}

func (w *WindowsWatcher) apply(e event) {
	if !slices.Contains(windowEvents, e.Event) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch e.Event {
	case EventOpen:
		var opened Window
		if json.Unmarshal(e.Window, &opened) != nil {
			return
		}
		if i := w.index(opened.ID); i >= 0 {
			w.windows[i] = opened
			return
		}
		w.windows = append(w.windows, opened)
	case EventFocus:
		var focused *uint64
		if json.Unmarshal(e.Window, &focused) != nil {
			return
		}
		for i := range w.windows {
			w.windows[i].Focused = focused != nil && w.windows[i].ID == *focused
		}
	default:
		var id uint64
		if json.Unmarshal(e.Window, &id) != nil {
			return
		}
		i := w.index(id)
		if i < 0 {
			return
		}
		if e.Event == EventClose {
			w.windows = slices.Delete(w.windows, i, i+1)
			return
		}
		e.update(&w.windows[i])
	}
}

func (w *WindowsWatcher) index(id uint64) int {
	return slices.IndexFunc(w.windows, func(win Window) bool { return win.ID == id })
}

// update sets the field a per-window event reports.
func (e event) update(win *Window) {
	switch e.Event {
	case EventMove:
		setFrom(&win.Workspace, e.Workspace)
	case EventTitle:
		setFrom(&win.Title, e.Title)
	case EventAppID:
		setFrom(&win.AppID, e.AppID)
	case EventRect:
		var output string
		if json.Unmarshal(e.Output, &output) == nil {
			win.Output = output
		}
		setFrom(&win.Rect, e.Rect)
	case EventUrgent:
		setFrom(&win.Urgent, e.Urgent)
	case EventMinimized:
		setFrom(&win.Minimized, e.Minimized)
	case EventStack:
		var stack *int
		if json.Unmarshal(e.Stack, &stack) == nil {
			win.Stack = stack
		}
	}
}

func setFrom[T any](field *T, value *T) {
	if value != nil {
		*field = *value
	}
}
