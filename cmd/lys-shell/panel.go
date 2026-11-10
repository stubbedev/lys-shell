package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"time"

	"github.com/stubbedev/lys-desktop/rusterr"
	"github.com/stubbedev/lys-desktop/settingspage"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

// panelCommand is lys-shell/src/cli/panel/commands.rs.
func panelCommand() *cli.Command {
	monitor := func(verb string) []*cli.Arg {
		return []*cli.Arg{{ID: "monitor", Help: `Monitor connector name (e.g., "DP-1"). Omit to ` + verb + " all"}}
	}
	focusedMonitor := []*cli.Arg{{ID: "monitor", Help: `Monitor connector name (e.g., "DP-1"). Omit for the focused monitor`}}
	return &cli.Command{
		Name:  "panel",
		About: "Panel management commands",
		Subcommands: []*cli.Command{
			{Name: "start", About: "Start the panel daemon", Run: panelStart},
			{Name: "stop", About: "Stop the panel daemon", Run: panelStop},
			{Name: "restart", About: "Restart the panel daemon", Run: panelRestart},
			{Name: "status", About: "Check panel status", Run: panelStatus},
			{Name: "settings", About: "Open panel settings", Run: panelSettings},
			{Name: "inspect", About: "Open the widget inspector for debugging", Run: panelInspect},
			{Name: "hide", About: "Hide the bar on a monitor", Args: monitor("hide"), Run: barVisibility("hide bar", "BarHide", "All bars hidden", "Bar hidden on ")},
			{Name: "show", About: "Show the bar on a monitor", Args: monitor("show"), Run: barVisibility("show bar", "BarShow", "All bars shown", "Bar shown on ")},
			{Name: "toggle", About: "Toggle bar visibility on a monitor", Args: monitor("toggle"), Run: barVisibility("toggle bar", "BarToggle", "All bars toggled", "Bar toggled on ")},
			{Name: "reveal", About: "Show an auto-hiding bar on a monitor for a few seconds", Args: monitor("reveal"), Run: barVisibility("reveal bar", "BarReveal", "All bars revealed", "Bar revealed on ")},
			{Name: "dashboard", About: "Open or close the dashboard dropdown", Args: focusedMonitor, Run: panelOnMonitor("toggle dashboard", "DashboardToggle")},
			{Name: "focus", About: "Move the keyboard into the bar: arrows walk its modules, Enter opens one, Esc leaves", Args: focusedMonitor, Run: panelOnMonitor("focus bar", "BarFocus")},
		},
	}
}

// panelConnect is panel/proxy.rs's connect.
func panelConnect() (*dbus.Conn, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, cliMessage("D-Bus session unavailable: " + err.Error())
	}
	return conn, nil
}

// panelRunning is proxy.rs's is_running.
func panelRunning() (bool, error) {
	conn, err := panelConnect()
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	running, err := shellipc.IsRunning(conn)
	if err != nil {
		return false, cliMessage("Failed to query D-Bus: " + err.Error())
	}
	return running, nil
}

func panelStart(m *cli.Matches) error {
	if running, _ := panelRunning(); running {
		fmt.Fprintln(m.Stdout(), "Panel is already running")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return cliMessage("Failed to resolve executable: " + err.Error())
	}
	cmd := exec.Command(exe, "shell") //nolint:gosec // our own binary
	if err := cmd.Start(); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return cliMessage("Permission denied when starting panel")
		}
		return cliMessage("Failed to start panel: " + rusterr.IO(err))
	}
	_ = cmd.Process.Release()
	fmt.Fprintln(m.Stdout(), "Panel started")
	return nil
}

// shutdownTimeout is proxy.rs's SHUTDOWN_TIMEOUT.
const shutdownTimeout = 5 * time.Second

func panelStop(m *cli.Matches) error {
	if running, _ := panelRunning(); !running {
		return cliMessage("Panel is not running")
	}
	conn, err := panelConnect()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	released, failed := make(chan struct{}, 1), make(chan error, 1)
	watch, err := watchRelease(conn, released, failed)
	if err != nil {
		return cliMessage("Failed to subscribe to name changes: " + err.Error())
	}
	defer func() { _ = watch.Stop() }()
	if err := activateAction(conn, shellipc.ActionQuit); err != nil {
		return cliMessage("Failed to stop panel: " + rusterr.Zbus(err))
	}
	select {
	case <-released:
	case err := <-failed:
		return cliMessage("Failed to follow the panel stopping: " + err.Error())
	case <-time.After(shutdownTimeout):
		return cliMessage("Timeout waiting for panel to stop")
	}
	fmt.Fprintln(m.Stdout(), "Panel stopped")
	return nil
}

// watchRelease is proxy.rs's wait_for_shutdown subscription: it tells
// released once the application id has no owner, and failed of an
// announcement of the bus's it could not read.
func watchRelease(conn *dbus.Conn, released chan<- struct{}, failed chan<- error) (*dbusx.Watch, error) {
	return dbusx.WatchSignals(context.Background(), conn, dbusx.FromBus(),
		func(err error) {
			select {
			case failed <- err:
			default:
			}
		},
		bussignal.OnNameLost(func(string) error {
			select {
			case released <- struct{}{}:
			default:
			}
			return nil
		}).WithArg0(shellipc.AppID))
}

func activateAction(conn *dbus.Conn, action string) error {
	return conn.Object(shellipc.AppID, shellipc.AppPath).Call(
		shellipc.ActionsIface+".Activate", 0, action, []dbus.Variant{}, map[string]dbus.Variant{}).Err
}

func panelRestart(m *cli.Matches) error {
	if running, _ := panelRunning(); running {
		if err := panelStop(m); err != nil {
			return err
		}
	}
	return panelStart(m)
}

func panelStatus(m *cli.Matches) error {
	running, err := panelRunning()
	if err != nil {
		return cliMessage("Cannot determine panel status: " + err.Error())
	}
	if running {
		fmt.Fprintln(m.Stdout(), "Panel is running")
	} else {
		fmt.Fprintln(m.Stdout(), "Panel is not running")
	}
	return nil
}

// panelSettings starts lys-settings, a package of its own, from the path.
func panelSettings(*cli.Matches) error {
	cmd := exec.Command(settingspage.Program) //nolint:gosec // a constant program name
	if err := cmd.Start(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
			return cliMessage("lys-settings not found. Install it: https://github.com/stubbedev/lys-settings")
		case errors.Is(err, fs.ErrPermission):
			return cliMessage("Permission denied when starting lys-settings")
		default:
			return cliMessage("Failed to launch settings: " + rusterr.IO(err))
		}
	}
	_ = cmd.Process.Release()
	return nil
}

func panelInspect(m *cli.Matches) error {
	if running, _ := panelRunning(); !running {
		return cliMessage("Panel is not running")
	}
	conn, err := panelConnect()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := activateAction(conn, shellipc.ActionInspect); err != nil {
		return cliMessage("Failed to open inspector: " + rusterr.Zbus(err))
	}
	fmt.Fprintln(m.Stdout(), "Inspector opened")
	return nil
}

// panelOnMonitor calls method with the monitor argument, "" for the
// focused monitor.
func panelOnMonitor(op, method string) func(*cli.Matches) error {
	return func(m *cli.Matches) error {
		conn, err := panelConnect()
		if err != nil {
			return err
		}
		p := &daemonProxy{label: "Shell", iface: shellipc.ServiceName, conn: conn, obj: conn.Object(shellipc.ServiceName, shellipc.ServicePath)}
		defer p.Close()
		monitor, _ := cli.Value[string](m, "monitor")
		return p.call(op, method, nil, monitor)
	}
}

// barVisibility is hide.rs / show.rs / toggle.rs.
func barVisibility(op, method, all, one string) func(*cli.Matches) error {
	return func(m *cli.Matches) error {
		conn, err := panelConnect()
		if err != nil {
			return err
		}
		p := &daemonProxy{label: "Shell", iface: shellipc.ServiceName, conn: conn, obj: conn.Object(shellipc.ServiceName, shellipc.ServicePath)}
		defer p.Close()
		monitor, _ := cli.Value[string](m, "monitor")
		if err := p.call(op, method, nil, monitor); err != nil {
			return err
		}
		if monitor == "" {
			fmt.Fprintln(m.Stdout(), all)
		} else {
			fmt.Fprintln(m.Stdout(), one+monitor)
		}
		return nil
	}
}
