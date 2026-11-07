package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/service/sway"
)

// WindowField is a window field fed to the matcher
// (-window-match-fields).
type WindowField uint8

// Window match fields.
const (
	WindowFieldTitle WindowField = iota
	WindowFieldClass
	WindowFieldName
	WindowFieldRole
	WindowFieldDesktop
)

// WindowConfig is the window mode's knobs (rofi's -window-* family).
type WindowConfig struct {
	// Format is the row template: {w} workspace, {c} class, {t} title,
	// {n} name, {r} role.
	Format      string
	MatchFields []WindowField
	// HideActive leaves the focused window out.
	HideActive bool
	// CloseOnDelete makes shift-delete close the window.
	CloseOnDelete bool
	// WindowCommand is the alt accept command, {window} substituted.
	WindowCommand string
	// CurrentDesktopOnly restricts the list to the current workspace
	// (windowcd).
	CurrentDesktopOnly bool
}

// DefaultWindowConfig is WindowConfig::default.
func DefaultWindowConfig() WindowConfig {
	return WindowConfig{
		Format:        "{w}   {c}   {t}",
		MatchFields:   []WindowField{WindowFieldTitle, WindowFieldClass},
		CloseOnDelete: true,
	}
}

// WindowInfo is one open window, compositor-agnostic.
type WindowInfo struct {
	// ID is the compositor's id: lys-wm IPC id, hyprland address, niri
	// id, sway con_id, mango client id.
	ID        string
	Title     string
	Class     string
	Workspace string
	Focused   bool
	// FocusOrder is lower for more recently focused windows.
	FocusOrder         int64
	OnCurrentWorkspace bool
	// HyprlandAddress is the window's Hyprland address, 0 elsewhere: it
	// captures the window through Hyprland's toplevel export.
	HyprlandAddress uint64
	// ToplevelIdentifier is the window's ext-foreign-toplevel-list
	// identifier where the compositor's IPC names it, "" elsewhere, where
	// a capture matches the window by class and title.
	ToplevelIdentifier string
}

// windowPreviewPx is the longer side a window preview is captured at:
// the preview pane's width at a 2x scale.
const windowPreviewPx = 720

// Capture captures the window's contents, scaled to fit the preview
// pane.
func (win WindowInfo) Capture(context.Context) (image.Image, error) {
	return sharepreview.WindowThumb(sharepreview.Toplevel{
		Class: win.Class, Title: win.Title,
		WindowAddress: win.HyprlandAddress, HasAddress: win.HyprlandAddress != 0,
		Identifier: win.ToplevelIdentifier,
	}, windowPreviewPx)
}

// Preview is the window's side pane: its picture, captured once its row
// is selected, over its title, application and workspace.
func (win WindowInfo) Preview() launcher.Preview {
	facts := make([]launcher.PreviewFact, 0, 3)
	if win.Title != "" {
		facts = append(facts, launcher.PreviewFact{Label: i18n.T("launcher-preview-title"), Value: win.Title})
	}
	if win.Class != "" {
		facts = append(facts, launcher.PreviewFact{Label: i18n.T("launcher-preview-app"), Value: win.Class})
	}
	if win.Workspace != "" {
		facts = append(facts, launcher.PreviewFact{Label: i18n.T("launcher-preview-workspace"), Value: win.Workspace})
	}
	return launcher.Preview{Image: win.Capture, Facts: facts}
}

// WindowBackend talks to one compositor. The Rust mode shelled out to
// hyprctl, niri msg, and swaymsg; the Go shell already has native IPC
// clients for every compositor, so the same JSON arrives over the sockets.
type WindowBackend interface {
	List(ctx context.Context) ([]WindowInfo, error)
	Focus(ctx context.Context, id string) error
	Close(ctx context.Context, id string) error
}

// DetectWindows picks the compositor from the environment, nil when
// none it can talk to is running.
func DetectWindows() WindowBackend {
	switch {
	case lyswm.IsRunning():
		return lysWMBackend{}
	case mango.IsRunning():
		return mangoBackend{}
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return hyprlandBackend{}
	case os.Getenv("NIRI_SOCKET") != "":
		return niriBackend{}
	case os.Getenv("SWAYSOCK") != "":
		return swayBackend{}
	}
	return nil
}

// Window switches between open windows (window.rs).
type Window struct {
	cfg     WindowConfig
	backend WindowBackend
	windows []WindowInfo
}

// NewWindow builds the mode; the compositor is detected from the
// environment.
func NewWindow(cfg WindowConfig) *Window { return &Window{cfg: cfg, backend: DetectWindows()} }

// Name is window or windowcd.
func (w *Window) Name() string {
	if w.cfg.CurrentDesktopOnly {
		return "windowcd"
	}
	return "window"
}

// Load lists the windows, most recently focused first; the focused one
// sorts last so plain Enter switches to the previous window, as rofi.
func (w *Window) Load(ctx context.Context) launcher.ModeState {
	var windows []WindowInfo
	if w.backend == nil {
		log.Printf("launcher: window mode: no supported compositor detected")
	} else {
		var err error
		if windows, err = w.backend.List(ctx); err != nil {
			log.Printf("launcher: window mode: %v", err)
		}
	}
	windows = slices.DeleteFunc(windows, func(win WindowInfo) bool {
		return (w.cfg.HideActive && win.Focused) || (w.cfg.CurrentDesktopOnly && !win.OnCurrentWorkspace)
	})
	slices.SortStableFunc(windows, func(a, b WindowInfo) int {
		if a.Focused != b.Focused {
			if a.Focused {
				return 1
			}
			return -1
		}
		switch {
		case a.FocusOrder < b.FocusOrder:
			return -1
		case a.FocusOrder > b.FocusOrder:
			return 1
		}
		return 0
	})
	items := make([]launcher.Item, len(windows))
	for i, win := range windows {
		items[i] = windowItem(win, w.cfg)
	}
	w.windows = windows
	return launcher.ModeState{Items: items, Prompt: w.Name(), NoCustom: true}
}

// Activate focuses the window, or runs -window-command on Alt.
func (w *Window) Activate(ctx context.Context, target launcher.Target, kind launcher.ActivateKind, _ string) launcher.Action {
	i, ok := target.Row()
	if w.backend == nil || !ok || int(i) >= len(w.windows) {
		return launcher.ActionNothing{}
	}
	win := w.windows[i]
	if _, alt := kind.(launcher.ActivateAlt); alt && w.cfg.WindowCommand != "" {
		launcher.RunShell(launcher.Render(w.cfg.WindowCommand, launcher.Values(map[string]string{"window": win.ID})))
		return launcher.ActionClose{}
	}
	if err := w.backend.Focus(ctx, win.ID); err != nil {
		log.Printf("launcher: focus window: %v", err)
	}
	return launcher.ActionClose{}
}

// Delete closes the window (when close-on-delete) and reloads.
func (w *Window) Delete(ctx context.Context, index uint32) launcher.Action {
	if !w.cfg.CloseOnDelete || w.backend == nil || int(index) >= len(w.windows) {
		return launcher.ActionNothing{}
	}
	if err := w.backend.Close(ctx, w.windows[index].ID); err != nil {
		log.Printf("launcher: close window: %v", err)
	}
	// Give the compositor a beat before the reload re-queries.
	time.Sleep(50 * time.Millisecond)
	return launcher.ActionReload{State: w.Load(ctx)}
}

// AllowsCustom is false.
func (*Window) AllowsCustom() bool { return false }

// RowPreview is the row's window.
func (w *Window) RowPreview(index uint32) (launcher.Preview, bool) {
	if int(index) >= len(w.windows) {
		return launcher.Preview{}, false
	}
	return w.windows[index].Preview(), true
}

func windowItem(win WindowInfo, cfg WindowConfig) launcher.Item {
	display := launcher.Render(cfg.Format, launcher.Values(windowVars(win)))
	parts := make([]string, 0, len(cfg.MatchFields))
	for _, f := range cfg.MatchFields {
		switch f {
		case WindowFieldTitle:
			parts = append(parts, win.Title)
		case WindowFieldDesktop:
			parts = append(parts, win.Workspace)
		default:
			parts = append(parts, win.Class)
		}
	}
	id := win.ID
	return launcher.Item{
		Display:   display,
		MatchText: strings.Join(parts, " "),
		Icon:      launcher.IconName(strings.ToLower(win.Class)),
		Info:      &id,
	}
}

const neverFocused = int64(1<<63 - 1)

type hyprlandBackend struct{}

// hyprClient is the part of j/clients the mode reads.
type hyprClient struct {
	Address   string `json:"address"`
	Title     string `json:"title"`
	Class     string `json:"class"`
	Mapped    *bool  `json:"mapped"`
	Workspace struct {
		ID   *int64 `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
	FocusHistoryID *int64 `json:"focusHistoryID"`
}

func (hyprlandBackend) conn() (*hyprland.Connection, error) { return hyprland.Connect() }

func (b hyprlandBackend) List(context.Context) ([]WindowInfo, error) {
	c, err := b.conn()
	if err != nil {
		return nil, err
	}
	raw, err := c.Command("j/clients")
	if err != nil {
		return nil, err
	}
	var active *int64
	if ws, err := c.Command("j/activeworkspace"); err == nil {
		var w struct {
			ID *int64 `json:"id"`
		}
		if json.Unmarshal([]byte(ws), &w) == nil {
			active = w.ID
		}
	}
	return parseHyprlandClients([]byte(raw), active)
}

func parseHyprlandClients(raw []byte, active *int64) ([]WindowInfo, error) {
	var clients []hyprClient
	if err := json.Unmarshal(raw, &clients); err != nil {
		return nil, err
	}
	var out []WindowInfo
	for _, c := range clients {
		if c.Mapped != nil && !*c.Mapped {
			continue
		}
		order := neverFocused
		if c.FocusHistoryID != nil {
			order = *c.FocusHistoryID
		}
		address, _ := strconv.ParseUint(strings.TrimPrefix(c.Address, "0x"), 16, 64)
		out = append(out, WindowInfo{
			ID: c.Address, Title: c.Title, Class: c.Class, Workspace: c.Workspace.Name, HyprlandAddress: address,
			Focused:            c.FocusHistoryID != nil && *c.FocusHistoryID == 0,
			FocusOrder:         order,
			OnCurrentWorkspace: c.Workspace.ID != nil && active != nil && *c.Workspace.ID == *active,
		})
	}
	return out, nil
}

func (b hyprlandBackend) Focus(_ context.Context, id string) error {
	c, err := b.conn()
	if err != nil {
		return err
	}
	return c.Dispatch(hyprland.FocusWindow(id))
}

func (b hyprlandBackend) Close(_ context.Context, id string) error {
	c, err := b.conn()
	if err != nil {
		return err
	}
	return c.Dispatch(hyprland.CloseWindow(id))
}

type niriBackend struct{}

func (niriBackend) List(context.Context) ([]WindowInfo, error) {
	c, err := niri.Connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	windows, err := c.Windows()
	if err != nil {
		return nil, err
	}
	workspaces, err := c.Workspaces()
	if err != nil {
		return nil, err
	}
	return niriWindows(windows, workspaces), nil
}

func niriWorkspaceLabel(w niri.Workspace) string {
	if w.Name != nil {
		return *w.Name
	}
	return strconv.Itoa(int(w.Idx))
}

func niriWindows(windows []niri.Window, workspaces []niri.Workspace) []WindowInfo {
	byID := make(map[uint64]niri.Workspace, len(workspaces))
	for _, w := range workspaces {
		byID[w.ID] = w
	}
	out := make([]WindowInfo, len(windows))
	for i, w := range windows {
		info := WindowInfo{
			ID: strconv.FormatUint(w.ID, 10), Title: deref(w.Title), Class: deref(w.AppID),
			Focused: w.IsFocused, FocusOrder: neverFocused,
		}
		if w.FocusTimestamp != nil {
			info.FocusOrder = -w.FocusTimestamp.Nanoseconds()
		}
		if w.WorkspaceID != nil {
			if ws, ok := byID[*w.WorkspaceID]; ok {
				info.Workspace, info.OnCurrentWorkspace = niriWorkspaceLabel(ws), ws.IsFocused
			}
		}
		out[i] = info
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (niriBackend) action(ctx context.Context, name, id string) error {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return err
	}
	c, err := niri.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.Action(ctx, name, map[string]any{"id": n})
}

func (b niriBackend) Focus(ctx context.Context, id string) error {
	return b.action(ctx, "FocusWindow", id)
}

func (b niriBackend) Close(ctx context.Context, id string) error {
	return b.action(ctx, "CloseWindow", id)
}

type swayBackend struct{}

func (swayBackend) List(context.Context) ([]WindowInfo, error) {
	c, err := sway.Connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Tree()
	if err != nil {
		return nil, err
	}
	return parseSwayTree(raw)
}

// swayNode is the part of a GET_TREE node the walk reads; raw fields
// keep "present and a string" distinct from absent or null, as the
// Rust walk tests them.
type swayNode struct {
	Type             string            `json:"type"`
	ID               int64             `json:"id"`
	Name             json.RawMessage   `json:"name"`
	AppID            json.RawMessage   `json:"app_id"`
	WindowProperties json.RawMessage   `json:"window_properties"`
	Focused          bool              `json:"focused"`
	Nodes            []json.RawMessage `json:"nodes"`
	FloatingNodes    []json.RawMessage `json:"floating_nodes"`
}

func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func parseSwayTree(raw []byte) ([]WindowInfo, error) {
	var out []WindowInfo
	if err := walkSway(raw, "", false, &out); err != nil {
		return nil, err
	}
	focusedWS, found := "", false
	for _, w := range out {
		if w.Focused {
			focusedWS, found = w.Workspace, true
			break
		}
	}
	for i := range out {
		out[i].OnCurrentWorkspace = found && out[i].Workspace == focusedWS
	}
	return out, nil
}

// walkSway collects view nodes, carrying the nearest workspace name. A
// view is a con or floating_con leaf with a string name and either an
// app_id string or window_properties object (window.rs walk_sway).
func walkSway(raw json.RawMessage, workspace string, hasWS bool, out *[]WindowInfo) error {
	var n swayNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	if n.Type == "workspace" {
		if name, ok := jsonString(n.Name); ok {
			workspace, hasWS = name, true
		} else {
			workspace, hasWS = "", false
		}
	}
	appID, hasAppID := jsonString(n.AppID)
	props := len(n.WindowProperties) > 0 && n.WindowProperties[0] == '{'
	name, hasName := jsonString(n.Name)
	if (n.Type == "con" || n.Type == "floating_con") && (hasAppID || props) && hasName && len(n.Nodes) == 0 {
		class := appID
		if !hasAppID && props {
			var wp struct {
				Class *string `json:"class"`
			}
			_ = json.Unmarshal(n.WindowProperties, &wp)
			class = deref(wp.Class)
		}
		ws := ""
		if hasWS {
			ws = workspace
		}
		*out = append(*out, WindowInfo{
			ID: strconv.FormatInt(n.ID, 10), Title: name, Class: class, Workspace: ws,
			Focused: n.Focused, FocusOrder: neverFocused,
		})
	}
	for _, child := range append(n.Nodes, n.FloatingNodes...) {
		if err := walkSway(child, workspace, hasWS, out); err != nil {
			return err
		}
	}
	return nil
}

func (swayBackend) command(ctx context.Context, cmd string) error {
	c, err := sway.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.RunCommand(ctx, cmd)
}

func (b swayBackend) Focus(ctx context.Context, id string) error {
	return b.command(ctx, "[con_id="+id+"] focus")
}

func (b swayBackend) Close(ctx context.Context, id string) error {
	return b.command(ctx, "[con_id="+id+"] kill")
}

type lysWMBackend struct{}

func (lysWMBackend) List(context.Context) ([]WindowInfo, error) {
	windows, err := lyswm.Windows()
	if err != nil {
		return nil, err
	}
	return lysWMWindows(windows), nil
}

// lysWMWindows maps lys-wm's list, oldest first: lys-wm reports no focus
// history, so the newest window ranks as the most recently used.
func lysWMWindows(windows []lyswm.Window) []WindowInfo {
	focusedWS, found := 0, false
	for _, w := range windows {
		if w.Focused {
			focusedWS, found = w.Workspace, true
			break
		}
	}
	out := make([]WindowInfo, len(windows))
	for i, w := range windows {
		out[i] = WindowInfo{
			ID: strconv.FormatUint(w.ID, 10), Title: w.Title, Class: w.AppID, Workspace: strconv.Itoa(w.Workspace),
			Focused: w.Focused, FocusOrder: int64(len(windows) - i), OnCurrentWorkspace: found && w.Workspace == focusedWS,
			ToplevelIdentifier: lyswm.ToplevelIdentifier(w.ID),
		}
	}
	return out
}

func (lysWMBackend) Focus(_ context.Context, id string) error {
	return lyswm.Dispatch("focuswindow " + id)
}

func (lysWMBackend) Close(_ context.Context, id string) error {
	return lyswm.Dispatch("closewindow " + id)
}

// mangoStateWait bounds waiting for mango's first full-state frames.
const mangoStateWait = 2 * time.Second

type mangoBackend struct{}

func (mangoBackend) state(ctx context.Context) ([]mango.Client, error) {
	w, err := mango.Watch()
	if err != nil {
		return nil, err
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(ctx, mangoStateWait)
	defer cancel()
	for !w.Ready() {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("mango: waiting for its client list: %w", ctx.Err())
		case <-w.Ticks():
		}
	}
	_, clients := w.State()
	return clients, nil
}

func (b mangoBackend) List(ctx context.Context) ([]WindowInfo, error) {
	clients, err := b.state(ctx)
	if err != nil {
		return nil, err
	}
	return mangoWindows(clients), nil
}

// mangoWindows maps mango's clients. Mango reports no focus history, so
// list order stands in for it, and a window's workspace is its first tag.
func mangoWindows(clients []mango.Client) []WindowInfo {
	var focused []uint32
	for _, c := range clients {
		if c.IsFocused {
			focused = c.Tags
			break
		}
	}
	out := make([]WindowInfo, len(clients))
	for i, c := range clients {
		ws := ""
		if len(c.Tags) > 0 {
			ws = strconv.FormatUint(uint64(c.Tags[0]), 10)
		}
		out[i] = WindowInfo{
			ID: strconv.FormatUint(uint64(c.ID), 10), Title: c.Title, Class: c.AppID, Workspace: ws,
			Focused: c.IsFocused, FocusOrder: int64(i),
			OnCurrentWorkspace: slices.ContainsFunc(c.Tags, func(t uint32) bool { return slices.Contains(focused, t) }),
		}
	}
	return out
}

func mangoClientID(id string) (uint32, error) {
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("mango: client id %q: %w", id, err)
	}
	return uint32(n), nil
}

func (mangoBackend) Focus(_ context.Context, id string) error {
	n, err := mangoClientID(id)
	if err != nil {
		return err
	}
	return mango.FocusClient(n)
}

func (mangoBackend) Close(_ context.Context, id string) error {
	n, err := mangoClientID(id)
	if err != nil {
		return err
	}
	return mango.KillClient(n)
}

func windowVars(win WindowInfo) map[string]string {
	return map[string]string{"w": win.Workspace, "c": win.Class, "t": win.Title, "n": win.Class, "r": ""}
}
