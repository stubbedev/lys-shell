package spawn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const (
	systemdName  = "org.freedesktop.systemd1"
	systemdPath  = dbus.ObjectPath("/org/freedesktop/systemd1")
	systemdIface = "org.freedesktop.systemd1.Manager"
)

const scopeTimeout = 5 * time.Second

var scopes = &scopeMover{timeout: scopeTimeout}

// Detached starts cmd with no stdio in a session and a systemd scope of its
// own, named for name, and reaps it in the background.
func Detached(cmd *exec.Cmd, name string) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := Scoped(cmd, name); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Scoped starts cmd in a session of its own and asks for its scope in the
// background, for the caller to wait on; the error is a failure to start.
func Scoped(cmd *exec.Cmd, name string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	scopes.moves.Go(func() {
		if err := scopes.move(pid, name); err != nil {
			log.Printf("spawn: %s runs in the shell's cgroup and stops with it: %v", name, err)
		}
	})
	return nil
}

type scopeMover struct {
	timeout time.Duration
	moves   sync.WaitGroup

	mu   sync.Mutex
	conn *dbus.Conn
}

func (s *scopeMover) move(pid int, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	conn, err := s.session(ctx)
	if err != nil {
		return err
	}
	err = intoScope(ctx, conn, pid, name)
	if err == nil || conn.Connected() || ctx.Err() != nil {
		return err
	}
	conn, err = s.session(ctx)
	if err != nil {
		return err
	}
	return intoScope(ctx, conn, pid, name)
}

func (s *scopeMover) session(ctx context.Context) (*dbus.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && s.conn.Connected() {
		return s.conn, nil
	}
	life, closeConn := context.WithCancel(context.Background())
	giveUp := context.AfterFunc(ctx, closeConn)
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(life))
	if !giveUp() || err != nil {
		closeConn()
		return nil, fmt.Errorf("session bus: %w", errors.Join(err, ctx.Err()))
	}
	s.conn = conn
	return conn, nil
}

func intoScope(ctx context.Context, conn *dbus.Conn, pid int, name string) error {
	var running bool
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, systemdName).Store(&running); err != nil {
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
	if err := conn.Object(systemdName, systemdPath).CallWithContext(ctx, systemdIface+".StartTransientUnit", 0, ScopeName(name, pid), "fail", props, aux).Err; err != nil {
		return fmt.Errorf("start its scope: %w", err)
	}
	return nil
}

// ScopeName is the unit a launched process runs in:
// app-lys\x2dshell-<name>-<pid>.scope, name escaped as systemd-escape does.
func ScopeName(name string, pid int) string {
	return `app-lys\x2dshell-` + escapeUnit(filepath.Base(name)) + "-" + strconv.Itoa(pid) + ".scope"
}

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
