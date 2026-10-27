package screenshot

import (
	"cmp"
	"encoding/json"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sway"
)

// Focus resolves what the output and window modes capture: the focused
// output and the active window, from whichever compositor is running
// (screenshot/mod.rs's focused_output_name and active_window_target).
type Focus interface {
	// FocusedOutput is the focused output's connector name; false lets
	// the capture fall back to the first output.
	FocusedOutput() (string, bool)
	// ActiveWindow identifies the focused window; the zero target
	// matches nothing.
	ActiveWindow() windowTarget
}

// CompositorFocus asks Hyprland (when Hyprland is set), then lys-wm,
// then mango, then niri - the Rust host's order with lys-wm added. The
// focused output also comes from sway's and niri's focused workspace.
type CompositorFocus struct {
	Hyprland *hyprland.Connection
}

// mangoWait bounds the wait for mango's first watch frames.
const mangoWait = 500 * time.Millisecond

// FocusedOutput implements Focus.
func (f CompositorFocus) FocusedOutput() (string, bool) {
	if f.Hyprland != nil {
		if mons, err := f.Hyprland.Monitors(); err == nil {
			for _, m := range mons {
				if m.Focused {
					return m.Name, true
				}
			}
		}
	}
	if lyswm.IsRunning() {
		if outs, err := lyswm.Outputs(); err == nil {
			for _, o := range outs {
				if o.Focused {
					return o.Name, true
				}
			}
		}
	}
	if mons, _, ok := mangoState(); ok {
		for _, m := range mons {
			if m.IsActive {
				return m.Name, true
			}
		}
	}
	if name, ok := swayFocusedOutput(); ok {
		return name, true
	}
	return niriFocusedOutput()
}

func swayFocusedOutput() (string, bool) {
	if !sway.IsRunning() {
		return "", false
	}
	conn, err := sway.Connect()
	if err != nil {
		return "", false
	}
	defer func() { _ = conn.Close() }()
	workspaces, err := conn.Workspaces()
	if err != nil {
		return "", false
	}
	return swayFocusedWorkspaceOutput(workspaces)
}

func swayFocusedWorkspaceOutput(workspaces []sway.Workspace) (string, bool) {
	for _, ws := range workspaces {
		if ws.Focused {
			return ws.Output, ws.Output != ""
		}
	}
	return "", false
}

func niriFocusedOutput() (string, bool) {
	if !niri.IsRunning() {
		return "", false
	}
	conn, err := niri.Connect()
	if err != nil {
		return "", false
	}
	defer func() { _ = conn.Close() }()
	workspaces, err := conn.Workspaces()
	if err != nil {
		return "", false
	}
	return niriFocusedWorkspaceOutput(workspaces)
}

func niriFocusedWorkspaceOutput(workspaces []niri.Workspace) (string, bool) {
	for _, ws := range workspaces {
		if ws.IsFocused && ws.Output != nil {
			return *ws.Output, *ws.Output != ""
		}
	}
	return "", false
}

// ActiveWindow implements Focus.
func (f CompositorFocus) ActiveWindow() windowTarget {
	if f.Hyprland != nil {
		if raw, err := f.Hyprland.Command("j/activewindow"); err == nil {
			if t, ok := parseHyprlandActiveWindow(raw); ok {
				return t
			}
		}
	}
	if lyswm.IsRunning() {
		if w, ok, err := lyswm.ActiveWindow(); err == nil && ok {
			return windowTarget{appID: w.AppID, hasAppID: true, title: w.Title, hasTitle: true}
		}
	}
	if niri.IsRunning() {
		if conn, err := niri.Connect(); err == nil {
			wins, err := conn.Windows()
			_ = conn.Close()
			if err == nil {
				for _, w := range wins {
					if w.IsFocused {
						return targetOf(w.AppID, w.Title)
					}
				}
			}
		}
	}
	if _, clients, ok := mangoState(); ok {
		for _, c := range clients {
			if c.IsFocused {
				t := windowTarget{appID: c.AppID, hasAppID: c.HasAppID, title: c.Title, hasTitle: c.HasTitle}
				return t
			}
		}
	}
	return windowTarget{}
}

// Windows is what the region overlay's click picks from: the visible
// windows' global logical rectangles, topmost first, from Hyprland,
// lys-wm or sway; none from niri and mango, which report no window
// geometry, so a click there picks the output.
func (f CompositorFocus) Windows() []image.Rectangle {
	if f.Hyprland != nil {
		mons, err := f.Hyprland.Monitors()
		if err != nil {
			return nil
		}
		clients, err := f.Hyprland.Clients()
		if err != nil {
			return nil
		}
		return hyprlandWindows(mons, clients)
	}
	if lyswm.IsRunning() {
		workspaces, err := lyswm.Workspaces()
		if err != nil {
			return nil
		}
		wins, err := lyswm.Windows()
		if err != nil {
			return nil
		}
		return lysWMWindows(workspaces, wins)
	}
	if sway.SocketPath() == "" {
		return nil
	}
	conn, err := sway.Connect()
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	wins, err := conn.Windows()
	if err != nil {
		return nil
	}
	return swayWindows(wins)
}

// hyprlandWindows are the clients on screen (mapped, not hidden, on a
// workspace a monitor shows): those on a shown special workspace
// first, then floating ones, each by focus history, most recent first.
func hyprlandWindows(mons []hyprland.Monitor, clients []hyprland.Client) []image.Rectangle {
	shown, special := map[int]bool{}, map[int]bool{}
	for _, m := range mons {
		shown[m.ActiveWS.ID] = true
		if m.SpecialWS.ID != 0 {
			shown[m.SpecialWS.ID], special[m.SpecialWS.ID] = true, true
		}
	}
	visible := slices.DeleteFunc(slices.Clone(clients), func(c hyprland.Client) bool {
		return !c.Mapped || c.Hidden || !shown[c.Workspace.ID] || c.Size[0] <= 0 || c.Size[1] <= 0
	})
	rank := func(c hyprland.Client) int {
		switch {
		case special[c.Workspace.ID]:
			return 0
		case c.Floating:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(visible, func(a, b hyprland.Client) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.FocusHistoryID, b.FocusHistoryID))
	})
	rects := make([]image.Rectangle, len(visible))
	for i, c := range visible {
		rects[i] = image.Rect(c.At[0], c.At[1], c.At[0]+c.Size[0], c.At[1]+c.Size[1])
	}
	return rects
}

func lysWMWindows(workspaces []lyswm.Workspace, wins []lyswm.Window) []image.Rectangle {
	shown := map[int]bool{}
	for _, ws := range workspaces {
		shown[ws.ID] = ws.Visible
	}
	rank := func(w lyswm.Window) int {
		switch {
		case w.Fullscreen:
			return 0
		case w.Floating:
			return 1
		}
		return 2
	}
	visible := slices.DeleteFunc(slices.Clone(wins), func(w lyswm.Window) bool {
		return !shown[w.Workspace] || w.Rect.Width <= 0 || w.Rect.Height <= 0
	})
	slices.Reverse(visible)
	slices.SortStableFunc(visible, func(a, b lyswm.Window) int { return cmp.Compare(rank(a), rank(b)) })
	rects := make([]image.Rectangle, len(visible))
	for i, w := range visible {
		rects[i] = image.Rect(w.Rect.X, w.Rect.Y, w.Rect.X+w.Rect.Width, w.Rect.Y+w.Rect.Height)
	}
	return rects
}

// swayWindows are the visible windows, floating ones first (they sit
// over the tiling), each kind latest in the tree first.
func swayWindows(wins []sway.Window) []image.Rectangle {
	var floating, tiled []image.Rectangle
	for _, w := range slices.Backward(wins) {
		switch {
		case !w.Visible || w.Rect.Empty():
		case w.Floating:
			floating = append(floating, w.Rect)
		default:
			tiled = append(tiled, w.Rect)
		}
	}
	return append(floating, tiled...)
}

// parseHyprlandActiveWindow reads j/activewindow: the address (hex,
// 0x-prefixed) becomes the toplevel-export handle, class and title the
// match keys. An empty object means no active window.
func parseHyprlandActiveWindow(raw string) (windowTarget, bool) {
	var w struct {
		Address *string `json:"address"`
		Class   string  `json:"class"`
		Title   string  `json:"title"`
	}
	if err := json.Unmarshal([]byte(raw), &w); err != nil || w.Address == nil {
		return windowTarget{}, false
	}
	t := windowTarget{appID: w.Class, hasAppID: true, title: w.Title, hasTitle: true}
	if h, err := strconv.ParseUint(strings.TrimPrefix(*w.Address, "0x"), 16, 64); err == nil {
		t.hyprlandHandle, t.hasHandle = h, true
	}
	return t, true
}

// targetOf builds a target from optional app id and title.
func targetOf(appID, title *string) windowTarget {
	var t windowTarget
	if appID != nil {
		t.appID, t.hasAppID = *appID, true
	}
	if title != nil {
		t.title, t.hasTitle = *title, true
	}
	return t
}

// mangoState snapshots mango's monitors and clients through a
// short-lived watch; false when mango is not running or never answers.
func mangoState() ([]mango.Monitor, []mango.Client, bool) {
	if !mango.IsRunning() {
		return nil, nil, false
	}
	w, err := mango.Watch()
	if err != nil {
		return nil, nil, false
	}
	defer w.Close()
	deadline := time.After(mangoWait)
	for !w.Ready() {
		select {
		case _, open := <-w.Ticks():
			if !open {
				return nil, nil, false
			}
		case <-deadline:
			return nil, nil, false
		}
	}
	mons, clients := w.State()
	return mons, clients, true
}
