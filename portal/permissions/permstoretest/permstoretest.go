// Package permstoretest runs the real xdg-permission-store on a private
// test bus.
package permstoretest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/portal/permissions"
)

const ownBound = 10 * time.Second

// Binary is xdg-permission-store, which ships in xdg-desktop-portal's
// libexec beside the frontend $XDG_DESKTOP_PORTAL names (the dev shell
// sets it).
func Binary(t testing.TB) string {
	t.Helper()
	portal := os.Getenv("XDG_DESKTOP_PORTAL")
	if portal == "" {
		t.Fatal("XDG_DESKTOP_PORTAL is unset: the dev shell names the xdg-desktop-portal binary")
	}
	resolved, err := filepath.EvalSymlinks(portal)
	if err != nil {
		t.Fatalf("resolve %s: %v", portal, err)
	}
	return filepath.Join(filepath.Dir(resolved), "xdg-permission-store")
}

// lockedBuffer is the store's log, written by its pipe and read at
// cleanup.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Start runs xdg-permission-store on bus with dataHome as its XDG data
// home, where it keeps its tables, and waits for it to own its name.
// It stops at cleanup; its log is shown when the test fails.
func Start(t testing.TB, bus *dbustest.Bus, dataHome string) {
	t.Helper()
	log := &lockedBuffer{}
	cmd := exec.Command(Binary(t), "--replace") //nolint:gosec // the dev shell's xdg-permission-store
	cmd.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS="+bus.Address, "XDG_DATA_HOME="+dataHome)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xdg-permission-store: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("xdg-permission-store:\n%s", log.String())
		}
	})
	conn := bus.Conn(t)
	for deadline := time.Now().Add(ownBound); ; time.Sleep(10 * time.Millisecond) {
		var owned bool
		if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, permissions.StoreName).Store(&owned); err != nil {
			t.Fatal(err)
		}
		if owned {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("xdg-permission-store never owned %s:\n%s", permissions.StoreName, log.String())
		}
	}
}

// Set writes an entry: each app's permissions for id in table, with
// data, as the frontend stores a grant.
func Set(t testing.TB, conn *dbus.Conn, table permissions.Table, id string, perms map[string][]string, data any) {
	t.Helper()
	err := conn.Object(permissions.StoreName, permissions.StorePath).Call(permissions.StoreIface+".Set", 0,
		string(table), true, id, perms, dbus.MakeVariant(data)).Err
	if err != nil {
		t.Fatalf("set %s %s: %v", table, id, err)
	}
}

// Lookup is an entry's permissions and data; ok is false for none.
func Lookup(t testing.TB, conn *dbus.Conn, table permissions.Table, id string) (perms map[string][]string, data dbus.Variant, ok bool) {
	t.Helper()
	err := conn.Object(permissions.StoreName, permissions.StorePath).Call(permissions.StoreIface+".Lookup", 0, string(table), id).Store(&perms, &data)
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.portal.Error.NotFound" {
		return nil, dbus.Variant{}, false
	}
	if err != nil {
		t.Fatalf("look up %s %s: %v", table, id, err)
	}
	return perms, data, true
}
