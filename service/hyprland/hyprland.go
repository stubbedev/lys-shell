// Package hyprland speaks Hyprland's IPC: the command socket for
// queries and dispatches, and the event socket for the live stream.
// The wire formats match crates/wayle-hyprland: one request per
// connection on the command socket, answered up to an EOT byte (0x04),
// and newline-terminated "event>>payload" lines on the event socket.
package hyprland

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/internal/ipcsock"
)

// endOfTransmission terminates every command-socket reply.
const endOfTransmission = '\x04'

// Paths resolves the IPC socket paths for the running Hyprland
// instance. Both HYPRLAND_INSTANCE_SIGNATURE and XDG_RUNTIME_DIR must
// be set; without them Hyprland is not running as far as lys-shell is
// concerned.
func Paths() (command, events string, err error) {
	his := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if his == "" {
		return "", "", errors.New("hyprland: HYPRLAND_INSTANCE_SIGNATURE is not set")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return "", "", errors.New("hyprland: XDG_RUNTIME_DIR is not set")
	}
	base := filepath.Join(runtime, "hypr", his)
	return filepath.Join(base, ".socket.sock"), filepath.Join(base, ".socket2.sock"), nil
}

// Workspace is one entry of j/workspaces.
type Workspace struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Monitor     string `json:"monitor"`
	MonitorID   int    `json:"monitorID"`
	Windows     int    `json:"windows"`
	Fullscreen  bool   `json:"hasfullscreen"`
	Persistent  bool   `json:"ispersistent"`
	LastWindow  string `json:"lastwindow"`
	LastTitle   string `json:"lastwindowtitle"`
	TiledLayout string `json:"tiled_layout"`
}

// Monitor is the subset of j/monitors the shell consumes.
type Monitor struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// ActiveWS is the workspace shown on the monitor.
	ActiveWS struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"activeWorkspace"`
	// SpecialWS is the special workspace shown over it, ID 0 for none.
	SpecialWS struct {
		ID int `json:"id"`
	} `json:"specialWorkspace"`
	// Focused is the compositor's keyboard focus.
	Focused bool `json:"focused"`
}

// Client is the subset of j/clients the shell consumes.
type Client struct {
	Address   string `json:"address"`
	Class     string `json:"class"`
	Title     string `json:"title"`
	Workspace struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
	Monitor  int  `json:"monitor"`
	Floating bool `json:"floating"`
	// At and Size are the window's global logical position and size.
	At   [2]int `json:"at"`
	Size [2]int `json:"size"`
	// Mapped and Hidden say whether the window is on screen at all.
	Mapped bool `json:"mapped"`
	Hidden bool `json:"hidden"`
	// FocusHistoryID is the window's place in the focus history, 0 the
	// most recently focused.
	FocusHistoryID int `json:"focusHistoryID"`
}

// WorkspaceRule is one entry of j/workspacerules.
type WorkspaceRule struct {
	WorkspaceString string  `json:"workspaceString"`
	Monitor         *string `json:"monitor"`
}

// Connection is a live handle to one Hyprland instance.
type Connection struct {
	commandPath string
	eventPath   string
	timeout     ipcsock.Timeout
}

// Connect resolves the sockets for the running instance. No connection
// is opened yet: requests are one-connection-per-request, and events
// subscribe through Events.
func Connect() (*Connection, error) {
	command, events, err := Paths()
	if err != nil {
		return nil, err
	}
	return ConnectTo(command, events), nil
}

// ConnectTo builds a connection to explicit socket paths — the test
// seam and the way for embedders that already resolved the paths.
func ConnectTo(commandPath, eventPath string) *Connection {
	return &Connection{commandPath: commandPath, eventPath: eventPath, timeout: ipcsock.Standard}
}

// Command sends one request and returns the reply up to the EOT byte.
func (c *Connection) Command(request string) (string, error) {
	conn, err := c.timeout.Open(c.commandPath)
	if err != nil {
		return "", fmt.Errorf("hyprland: connect for %q: %w", request, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(request)); err != nil {
		return "", fmt.Errorf("hyprland: write %q: %w", request, err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("hyprland: read the reply to %q: %w", request, err)
	}
	return strings.TrimSuffix(string(reply), string(endOfTransmission)), nil
}

// Workspaces lists the instance's workspaces (j/workspaces).
func (c *Connection) Workspaces() ([]Workspace, error) {
	reply, err := c.Command("j/workspaces")
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Workspace](reply)
}

// Monitors lists the instance's monitors (j/monitors).
func (c *Connection) Monitors() ([]Monitor, error) {
	reply, err := c.Command("j/monitors")
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Monitor](reply)
}

// Clients lists the instance's windows (j/clients), addresses
// normalized.
func (c *Connection) Clients() ([]Client, error) {
	reply, err := c.Command("j/clients")
	if err != nil {
		return nil, err
	}
	clients, err := decodeJSON[[]Client](reply)
	if err != nil {
		return nil, err
	}
	for i := range clients {
		clients[i].Address = NormalizeAddress(clients[i].Address)
	}
	return clients, nil
}

// WorkspaceRules lists the configured workspace rules
// (j/workspacerules).
func (c *Connection) WorkspaceRules() ([]WorkspaceRule, error) {
	reply, err := c.Command("j/workspacerules")
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]WorkspaceRule](reply)
}

// ActiveWorkspace is j/activeworkspace: the focused monitor's
// workspace.
func (c *Connection) ActiveWorkspace() (Workspace, error) {
	reply, err := c.Command("j/activeworkspace")
	if err != nil {
		return Workspace{}, err
	}
	return decodeJSON[Workspace](reply)
}

// ActiveWindow is j/activewindow: the focused window, false when none
// is (Hyprland answers {} while a layer surface or nothing has focus).
func (c *Connection) ActiveWindow() (Client, bool, error) {
	reply, err := c.Command("j/activewindow")
	if err != nil {
		return Client{}, false, err
	}
	client, err := decodeJSON[Client](reply)
	if err != nil {
		return Client{}, false, err
	}
	client.Address = NormalizeAddress(client.Address)
	return client, client.Class != "" || client.Title != "", nil
}

const (
	replyOK                = "ok"
	replyInvalidDispatcher = "Invalid dispatcher"
)

// Action is one dispatcher in both of Hyprland's command languages.
type Action struct{ lua, legacy string }

func luaString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// FocusWindow focuses a window by its address, switching to its
// workspace.
func FocusWindow(address string) Action {
	selector := "address:" + address
	return Action{lua: "hl.dsp.focus({window = " + luaString(selector) + "})", legacy: "focuswindow " + selector}
}

// CloseWindow asks a window to close by its address.
func CloseWindow(address string) Action {
	selector := "address:" + address
	return Action{lua: "hl.dsp.window.close({window = " + luaString(selector) + "})", legacy: "closewindow " + selector}
}

// FocusWorkspace switches to a workspace by number.
func FocusWorkspace(id int) Action {
	n := strconv.Itoa(id)
	return Action{lua: "hl.dsp.focus({workspace = " + n + "})", legacy: "workspace " + n}
}

// FocusNamedWorkspace switches to a workspace by name.
func FocusNamedWorkspace(name string) Action {
	selector := "name:" + name
	return Action{lua: "hl.dsp.focus({workspace = " + luaString(selector) + "})", legacy: "workspace " + selector}
}

// FocusPreviousWorkspace switches back to the workspace focused before.
func FocusPreviousWorkspace() Action {
	return Action{lua: `hl.dsp.focus({workspace = "previous"})`, legacy: "workspace previous"}
}

// NextKeyboardLayout moves every keyboard to its next layout. It is a
// request of its own, not a dispatcher, and fails on any reply but the ack.
func (c *Connection) NextKeyboardLayout() error {
	const request = "switchxkblayout all next"
	reply, err := c.Command(request)
	if err != nil {
		return err
	}
	if reply != "ok" {
		return fmt.Errorf("hyprland: %s: %s", request, reply)
	}
	return nil
}

// Dispatch runs an action, in the legacy language on an instance that
// has no Lua, and fails on any reply but the ack.
func (c *Connection) Dispatch(action Action) error {
	request := "dispatch " + action.lua
	reply, err := c.Command(request)
	if err != nil {
		return err
	}
	if reply == replyInvalidDispatcher {
		request = "dispatch " + action.legacy
		if reply, err = c.Command(request); err != nil {
			return err
		}
	}
	if reply != replyOK {
		return fmt.Errorf("hyprland: %q: %s", request, reply)
	}
	return nil
}
