// Package lyswm is the lys-wm IPC client: one JSON request line per
// connection on $LYS_WM_SOCKET, answered by one response line, and the
// watch stream of event lines (lys-wm's docs/ipc.md).
package lyswm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"
)

// SocketEnv is the variable lys-wm exports its socket path in.
const SocketEnv = "LYS_WM_SOCKET"

const protocolVersion = 1

const (
	dialTimeout    = 2 * time.Second
	requestTimeout = 5 * time.Second
)

// ErrNotRunning is a request made while no lys-wm socket is advertised.
var ErrNotRunning = errors.New("lys-wm: " + SocketEnv + " is not set")

// SocketPath reads the advertised socket; empty when lys-wm is not
// running.
func SocketPath() string { return os.Getenv(SocketEnv) }

// IsRunning reports whether a lys-wm socket is advertised.
func IsRunning() bool { return SocketPath() != "" }

// Rect is a rectangle in logical pixels of the output layout.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Output is one entry of the outputs result.
type Output struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Rect        Rect    `json:"rect"`
	Scale       float64 `json:"scale"`
	Workspace   int     `json:"workspace"`
	Focused     bool    `json:"focused"`
	Powered     bool    `json:"powered"`
}

// Workspace is one open workspace.
type Workspace struct {
	ID      int    `json:"id"`
	Output  string `json:"output"`
	Windows int    `json:"windows"`
	Visible bool   `json:"visible"`
	Focused bool   `json:"focused"`
}

// Window is one mapped window.
type Window struct {
	ID         uint64 `json:"id"`
	AppID      string `json:"app_id"`
	Title      string `json:"title"`
	Workspace  int    `json:"workspace"`
	Output     string `json:"output"`
	Rect       Rect   `json:"rect"`
	Floating   bool   `json:"floating"`
	Fullscreen bool   `json:"fullscreen"`
	Urgent     bool   `json:"urgent"`
	Focused    bool   `json:"focused"`
}

// Layout is the active keyboard layout group.
type Layout struct {
	Index uint32 `json:"index"`
	Name  string `json:"name"`
}

// Locks is the lock keys' state as the keymap's LEDs show it.
type Locks struct {
	Caps   bool `json:"caps"`
	Num    bool `json:"num"`
	Scroll bool `json:"scroll"`
}

// RejectedError is a request lys-wm answered with an error.
type RejectedError struct {
	Request string
	Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("lys-wm rejected %s: %s", e.Request, e.Message)
}

type request struct {
	Request string `json:"request"`
	Action  string `json:"action,omitempty"`
}

type response struct {
	Version int             `json:"version"`
	Error   string          `json:"error"`
	Result  json.RawMessage `json:"result"`
}

func open(req request) (net.Conn, *bufio.Reader, error) {
	path := SocketPath()
	if path == "" {
		return nil, nil, ErrNotRunning
	}
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("lys-wm: %s: %w", req.Request, err)
	}
	line, err := json.Marshal(req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("lys-wm: encode %s: %w", req.Request, err)
	}
	if err := conn.SetDeadline(time.Now().Add(requestTimeout)); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("lys-wm: %s: %w", req.Request, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("lys-wm: send %s: %w", req.Request, err)
	}
	return conn, bufio.NewReader(conn), nil
}

func readResponse(reader *bufio.Reader, req request) (response, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return response{}, fmt.Errorf("lys-wm: read the %s answer: %w", req.Request, err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return response{}, fmt.Errorf("lys-wm: parse the %s answer %q: %w", req.Request, line, err)
	}
	if resp.Version != protocolVersion {
		return response{}, fmt.Errorf("lys-wm: %s answered IPC version %d, this client speaks %d", req.Request, resp.Version, protocolVersion)
	}
	if resp.Error != "" {
		return response{}, &RejectedError{Request: req.Request, Message: resp.Error}
	}
	return resp, nil
}

func call(req request, out any) error {
	conn, reader, err := open(req)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	resp, err := readResponse(reader, req)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("lys-wm: parse the %s result: %w", req.Request, err)
	}
	return nil
}

// Outputs lists the outputs.
func Outputs() ([]Output, error) {
	var out []Output
	return out, call(request{Request: "outputs"}, &out)
}

// Workspaces lists the open workspaces in ID order.
func Workspaces() ([]Workspace, error) {
	var out []Workspace
	return out, call(request{Request: "workspaces"}, &out)
}

// Windows lists the mapped windows, oldest first.
func Windows() ([]Window, error) {
	var out []Window
	return out, call(request{Request: "windows"}, &out)
}

// ActiveWindow is the focused window; false when none has focus.
func ActiveWindow() (Window, bool, error) {
	var out *Window
	if err := call(request{Request: "activewindow"}, &out); err != nil {
		return Window{}, false, err
	}
	if out == nil {
		return Window{}, false, nil
	}
	return *out, true, nil
}

func activeSubmap() (string, error) {
	var out *string
	if err := call(request{Request: "submap"}, &out); err != nil {
		return "", err
	}
	if out == nil {
		return "", errors.New("lys-wm: submap answered no result")
	}
	return *out, nil
}

func activeLayout() (Layout, error) {
	var out *Layout
	if err := call(request{Request: "layout"}, &out); err != nil {
		return Layout{}, err
	}
	if out == nil {
		return Layout{}, errors.New("lys-wm: layout answered no result")
	}
	return *out, nil
}

func activeLocks() (Locks, error) {
	var out *Locks
	if err := call(request{Request: "locks"}, &out); err != nil {
		return Locks{}, err
	}
	if out == nil {
		return Locks{}, errors.New("lys-wm: locks answered no result")
	}
	return *out, nil
}

// Dispatch runs one action, as a bind would.
func Dispatch(action string) error {
	return call(request{Request: "dispatch", Action: action}, nil)
}

// EventKind names one watch event.
type EventKind string

// The watch events.
const (
	EventDispatch      EventKind = "dispatch"
	EventOutput        EventKind = "output"
	EventWorkspace     EventKind = "workspace"
	EventFocusedOutput EventKind = "focusedoutput"
	EventOpen          EventKind = "open"
	EventClose         EventKind = "close"
	EventMove          EventKind = "move"
	EventTitle         EventKind = "title"
	EventIcon          EventKind = "icon"
	EventUrgent        EventKind = "urgent"
	EventUnresponsive  EventKind = "unresponsive"
	EventFocus         EventKind = "focus"
	EventSubmap        EventKind = "submap"
	EventLayout        EventKind = "layout"
	EventLocks         EventKind = "locks"
)

type event struct {
	Event  EventKind `json:"event"`
	Submap *string   `json:"submap"`
	Layout *Layout   `json:"layout"`
	Locks  *Locks    `json:"locks"`
}

// Watcher follows the watch stream: it keeps the submap, keyboard
// layout and lock keys, seeded from the submap, layout and locks
// requests and then updated by the stream, and ticks after each event of the kinds it was asked
// for.
type Watcher struct {
	conn  net.Conn
	kinds []EventKind
	ticks chan struct{}
	once  sync.Once

	mu     sync.Mutex
	submap string
	layout Layout
	locks  Locks
}

// Watch opens the event stream. It ticks on the given kinds, or on
// every event when none is given; Ticks closes when the stream ends.
func Watch(kinds ...EventKind) (*Watcher, error) {
	req := request{Request: "watch"}
	conn, reader, err := open(req)
	if err != nil {
		return nil, err
	}
	if _, err := readResponse(reader, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: watch: %w", err)
	}
	submap, err := activeSubmap()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: watch: seed the submap: %w", err)
	}
	layout, err := activeLayout()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: watch: seed the layout: %w", err)
	}
	locks, err := activeLocks()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: watch: seed the locks: %w", err)
	}
	w := &Watcher{conn: conn, kinds: kinds, ticks: make(chan struct{}, 1), submap: submap, layout: layout, locks: locks}
	go w.pump(reader)
	return w, nil
}

func (w *Watcher) pump(reader *bufio.Reader) {
	defer close(w.ticks)
	defer w.Close()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil || e.Event == "" {
			return
		}
		w.apply(e)
		if len(w.kinds) > 0 && !slices.Contains(w.kinds, e.Event) {
			continue
		}
		select {
		case w.ticks <- struct{}{}:
		default:
		}
	}
}

func (w *Watcher) apply(e event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case e.Event == EventSubmap && e.Submap != nil:
		w.submap = *e.Submap
	case e.Event == EventLayout && e.Layout != nil:
		w.layout = *e.Layout
	case e.Event == EventLocks && e.Locks != nil:
		w.locks = *e.Locks
	}
}

// Submap is the active bind submap; "" for the default binds.
func (w *Watcher) Submap() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.submap
}

// Layout is the active keyboard layout.
func (w *Watcher) Layout() Layout {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.layout
}

// Locks is the lock keys' state.
func (w *Watcher) Locks() Locks {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.locks
}

// Ticks fires after each followed event; it closes when the stream
// ends.
func (w *Watcher) Ticks() <-chan struct{} { return w.ticks }

// Close ends the stream.
func (w *Watcher) Close() {
	w.once.Do(func() { _ = w.conn.Close() })
}
