package spawn

import (
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

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

// fakeManager is systemd's user manager, recording the scopes asked of
// it, or refusing them when fail is set.
type fakeManager struct {
	mu     sync.Mutex
	scopes map[string]map[string]dbus.Variant
	fail   bool
}

func (m *fakeManager) StartTransientUnit(name, mode string, props []unitProp, _ []auxUnit) (dbus.ObjectPath, *dbus.Error) {
	if m.fail {
		return "", dbus.NewError("org.freedesktop.systemd1.UnitExists", []any{"exists"})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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

// onBus points the package at a private session bus, with a user
// manager on it when manager is set.
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

// running starts a process that lives until the test ends.
func running(t *testing.T, name string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := Detached(cmd, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// A launched app runs in a scope of its own under app.slice, so
// restarting the shell's unit leaves it running.
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

// Without a systemd user manager there is no unit to leave: the app
// starts and nothing is asked of anyone.
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

// A manager refusing the scope leaves the app running in the shell's
// cgroup and says so; it is not a failure to start.
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

// Unit names escape what systemd-escape escapes, the program's base
// name only.
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
