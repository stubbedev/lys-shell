package shellipc

import (
	"fmt"
	"sort"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"github.com/stubbedev/lys-shell/internal/dbusx"
)

// The shell's application identity: the name and the org.gtk.Actions
// object a GtkApplication exports through GIO, so `lys-shell panel` and
// gapplication(1) reach the shell the same way.
const (
	AppID         = "dev.stubbe.lys.shell"
	AppPath       = dbus.ObjectPath("/dev/stubbe/lys/shell")
	ActionsIface  = "org.gtk.Actions"
	ActionQuit    = "quit"
	ActionInspect = "inspector"
)

// IsRunning is bootstrap.rs's is_already_running / the CLI's
// is_running: whether a shell owns the application id.
func IsRunning(conn *dbus.Conn) (bool, error) {
	var owned bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, AppID).Store(&owned)
	return owned, err
}

// Actions is what the two stateless actions run: Quit ends the shell,
// Inspect opens lys-ui's inspector.
type Actions struct {
	Quit    func()
	Inspect func()
}

// actions is GIO's action-group exporter for Actions.
type actions struct {
	run Actions
}

// actionDescription is org.gtk.Actions' (bgav): enabled, parameter
// type, state.
type actionDescription struct {
	Enabled   bool
	ParamType dbus.Signature
	State     []dbus.Variant
}

func (a *actions) names() []string {
	names := []string{ActionInspect, ActionQuit}
	sort.Strings(names)
	return names
}

// List names the actions.
func (a *actions) List() ([]string, *dbus.Error) { return a.names(), nil }

// Describe describes one action.
func (a *actions) Describe(name string) (actionDescription, *dbus.Error) {
	if name != ActionQuit && name != ActionInspect {
		return actionDescription{}, dbusx.InvalidArgs("action '" + name + "' does not exist")
	}
	return actionDescription{Enabled: true, State: []dbus.Variant{}}, nil
}

// DescribeAll describes every action.
func (a *actions) DescribeAll() (map[string]actionDescription, *dbus.Error) {
	out := map[string]actionDescription{}
	for _, name := range a.names() {
		out[name] = actionDescription{Enabled: true, State: []dbus.Variant{}}
	}
	return out, nil
}

// Activate runs an action.
func (a *actions) Activate(name string, _ []dbus.Variant, _ map[string]dbus.Variant) *dbus.Error {
	switch name {
	case ActionQuit:
		a.run.Quit()
		return nil
	case ActionInspect:
		a.run.Inspect()
		return nil
	default:
		return dbusx.InvalidArgs("action '" + name + "' does not exist")
	}
}

// SetState refuses: both actions are stateless.
func (a *actions) SetState(name string, _ dbus.Variant, _ map[string]dbus.Variant) *dbus.Error {
	return dbusx.InvalidArgs("action '" + name + "' is stateless")
}

// ServeApplication takes the application id and exports run as its
// actions. A shell already holding the id is an error.
func ServeApplication(conn *dbus.Conn, run Actions) (func(), error) {
	a := &actions{run: run}
	if err := conn.Export(a, AppPath, ActionsIface); err != nil {
		return nil, fmt.Errorf("export %s: %w", ActionsIface, err)
	}
	node := &introspect.Node{
		Name: string(AppPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{Name: ActionsIface, Methods: introspect.Methods(a)},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), AppPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return nil, fmt.Errorf("export introspection: %w", err)
	}
	reply, err := conn.RequestName(AppID, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", AppID, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("%s is already owned", AppID)
	}
	return func() { _, _ = conn.ReleaseName(AppID) }, nil
}
