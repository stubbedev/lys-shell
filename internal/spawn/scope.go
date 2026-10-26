package spawn

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/intconv"
)

// systemd's user manager on the session bus.
const (
	systemdName  = "org.freedesktop.systemd1"
	systemdPath  = dbus.ObjectPath("/org/freedesktop/systemd1")
	systemdIface = "org.freedesktop.systemd1.Manager"
)

// Detached starts cmd with no stdio in a session and a systemd scope of
// its own, named for name, and reaps it in the background. The scope is
// what lets it outlive the shell: restarting the shell's unit stops
// every process in its cgroup, so an app the shell launched must not
// stay there.
func Detached(cmd *exec.Cmd, name string) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := start(cmd, name); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// start starts cmd in a session of its own and moves it into its own
// scope; the error is a failure to start. Without a systemd user
// manager there is no unit cgroup to leave, so it stays where it is; a
// manager that refuses the scope is logged, the process already
// running.
func start(cmd *exec.Cmd, name string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := intoScope(cmd.Process.Pid, name); err != nil {
		log.Printf("spawn: %s runs in the shell's cgroup and stops with it: %v", name, err)
	}
	return nil
}

// intoScope moves pid into a new transient scope under app.slice, the
// way desktop launchers hand each app its own unit.
func intoScope(pid int, name string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var running bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, systemdName).Store(&running); err != nil {
		return fmt.Errorf("look up the systemd user manager: %w", err)
	}
	if !running {
		return nil
	}
	unitPID, err := intconv.Exact[uint32](pid)
	if err != nil {
		return fmt.Errorf("pid %d: %w", pid, err)
	}
	props := []struct {
		Name  string
		Value dbus.Variant
	}{
		{"PIDs", dbus.MakeVariant([]uint32{unitPID})},
		{"Slice", dbus.MakeVariant("app.slice")},
		{"CollectMode", dbus.MakeVariant("inactive-or-failed")},
	}
	aux := []struct {
		Name  string
		Props []struct {
			Name  string
			Value dbus.Variant
		}
	}{}
	if err := conn.Object(systemdName, systemdPath).Call(systemdIface+".StartTransientUnit", 0, ScopeName(name, pid), "fail", props, aux).Err; err != nil {
		return fmt.Errorf("start its scope: %w", err)
	}
	return nil
}

// ScopeName is the unit a launched process runs in:
// app-lys\x2dshell-<name>-<pid>.scope, name reduced to its program and
// escaped as systemd-escape does.
func ScopeName(name string, pid int) string {
	return `app-lys\x2dshell-` + escapeUnit(filepath.Base(name)) + "-" + strconv.Itoa(pid) + ".scope"
}

// escapeUnit escapes s for a unit name: ASCII letters, digits, '_' and
// '.' (not leading) stay, every other byte becomes \xHH.
func escapeUnit(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		keep := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' && i > 0
		if keep {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String()
}
