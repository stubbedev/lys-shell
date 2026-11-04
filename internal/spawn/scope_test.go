package spawn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type unitProp struct {
	Name  string
	Value dbus.Variant
}

type auxUnit struct {
	Name  string
	Props []unitProp
}

type fakeManager struct {
	mu      sync.Mutex
	scopes  map[string]map[string]dbus.Variant
	senders map[dbus.Sender]bool
	fail    bool
	hold    chan struct{}
	entered chan struct{}
}

func (m *fakeManager) StartTransientUnit(sender dbus.Sender, name, mode string, props []unitProp, _ []auxUnit) (dbus.ObjectPath, *dbus.Error) {
	if m.hold != nil {
		m.entered <- struct{}{}
		<-m.hold
	}
	if m.fail {
		return "", dbus.NewError("org.freedesktop.systemd1.UnitExists", []any{"exists"})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.senders == nil {
		m.senders = map[dbus.Sender]bool{}
	}
	m.senders[sender] = true
	got := map[string]dbus.Variant{"mode": dbus.MakeVariant(mode)}
	for _, p := range props {
		got[p.Name] = p.Value
	}
	m.scopes[name] = got
	return "/org/freedesktop/systemd1/job/1", nil
}

func (m *fakeManager) scope(name string) (map[string]dbus.Variant, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scopes[name]
	return s, ok
}

func onBus(t *testing.T, manager *fakeManager) {
	t.Helper()
	bus := dbustest.Start(t)
	if manager != nil {
		conn := bus.Conn(t)
		if err := conn.Export(manager, systemdPath, systemdIface); err != nil {
			t.Fatal(err)
		}
		if reply, err := conn.RequestName(systemdName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("own %s: %v %v", systemdName, reply, err)
		}
	}
	bus.UseAsSessionBus(t)
}

func running(t *testing.T, name string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := Detached(cmd, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	scopes.moves.Wait()
	return cmd
}

func TestDetachedRunsInItsOwnScope(t *testing.T) {
	m := &fakeManager{scopes: map[string]map[string]dbus.Variant{}}
	onBus(t, m)
	cmd := running(t, "/usr/bin/my-app")
	name := ScopeName("my-app", cmd.Process.Pid)
	scope, ok := m.scope(name)
	if !ok {
		t.Fatalf("no scope %s; asked for %v", name, m.scopes)
	}
	if pids, _ := scope["PIDs"].Value().([]uint32); !slices.Equal(pids, []uint32{uint32(cmd.Process.Pid)}) {
		t.Errorf("scope PIDs %v, want the app's %d", scope["PIDs"], cmd.Process.Pid)
	}
	if slice, _ := scope["Slice"].Value().(string); slice != "app.slice" {
		t.Errorf("scope slice %q, want app.slice", slice)
	}
}

func TestDetachedWithoutSystemdJustStarts(t *testing.T) {
	onBus(t, nil)
	logs := captureLog(t)
	cmd := running(t, "my-app")
	if cmd.Process == nil {
		t.Fatal("the app did not start")
	}
	if logs.String() != "" {
		t.Errorf("starting without systemd logged %q", logs.String())
	}
}

func TestDetachedLogsARefusedScope(t *testing.T) {
	onBus(t, &fakeManager{fail: true})
	logs := captureLog(t)
	cmd := running(t, "my-app")
	if cmd.Process == nil {
		t.Fatal("the app did not start")
	}
	if !strings.Contains(logs.String(), "my-app runs in the shell's cgroup") {
		t.Errorf("a refused scope logged %q", logs.String())
	}
}

func TestQuietReturnsWhileTheBusHolds(t *testing.T) {
	m := &fakeManager{scopes: map[string]map[string]dbus.Variant{}, hold: make(chan struct{}), entered: make(chan struct{}, 8)}
	onBus(t, m)
	release := sync.OnceFunc(func() { close(m.hold) })
	t.Cleanup(release)
	t.Cleanup(scopes.moves.Wait)
	const commands = 3
	for range commands {
		Quiet("true")
	}
	for range commands {
		select {
		case <-m.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the commands' scopes never reached the manager")
		}
	}
	release()
	scopes.moves.Wait()
	if got := len(m.senders); got != 1 {
		t.Errorf("%d session bus connections for %d commands, want 1", got, commands)
	}
	if got := len(m.scopes); got != commands {
		t.Errorf("%d scopes, want %d", got, commands)
	}
}

func TestScopeMoveEndsAtItsDeadline(t *testing.T) {
	m := &fakeManager{scopes: map[string]map[string]dbus.Variant{}, hold: make(chan struct{}), entered: make(chan struct{}, 2)}
	onBus(t, m)
	t.Cleanup(sync.OnceFunc(func() { close(m.hold) }))
	mover := &scopeMover{timeout: 50 * time.Millisecond}
	t.Cleanup(func() { _ = mover.conn.Close() })
	if err := mover.move(os.Getpid(), "held"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("move against a silent manager: %v, want its deadline", err)
	}
	if !mover.conn.Connected() {
		t.Error("the deadline closed the shared connection")
	}
}

func TestScopeMoverReconnectsAfterTheBusCloses(t *testing.T) {
	m := &fakeManager{scopes: map[string]map[string]dbus.Variant{}}
	onBus(t, m)
	mover := &scopeMover{timeout: 5 * time.Second}
	if err := mover.move(os.Getpid(), "first"); err != nil {
		t.Fatal(err)
	}
	first := mover.conn
	_ = first.Close()
	t.Cleanup(func() { _ = mover.conn.Close() })
	if err := mover.move(os.Getpid(), "second"); err != nil {
		t.Fatalf("move after the connection closed: %v", err)
	}
	if mover.conn == first {
		t.Error("the closed connection was kept")
	}
}

func TestScopeName(t *testing.T) {
	for in, want := range map[string]string{
		"org.mozilla.firefox": `app-lys\x2dshell-org.mozilla.firefox-7.scope`,
		"/usr/bin/my-app":     `app-lys\x2dshell-my\x2dapp-7.scope`,
		".hidden":             `app-lys\x2dshell-\x2ehidden-7.scope`,
		"a b":                 `app-lys\x2dshell-a\x20b-7.scope`,
	} {
		if got := ScopeName(in, 7); got != want {
			t.Errorf("ScopeName(%q) = %s, want %s", in, got, want)
		}
	}
}
