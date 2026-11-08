package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

func TestStartingTheShellRefreshesASchemaFromAnotherVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{"$id":"lys-shell-config-0.0.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := errors.New("the shell ended")
	if err := refreshingSchema(func(*dbus.Conn) error { return want })(nil); !errors.Is(err, want) {
		t.Fatalf("the shell's own result was replaced: %v", err)
	}
	expected := config.GenerateSchema(version.Version)
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("schema.json still holds %d stale bytes", len(got))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func busPeers(t *testing.T, probe *dbus.Conn) int {
	t.Helper()
	var names []string
	if err := probe.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		t.Fatal(err)
	}
	return len(slices.DeleteFunc(names, func(n string) bool { return !strings.HasPrefix(n, ":") }))
}

func TestStartingTheShellMakesOneBusConnection(t *testing.T) {
	bus := dbustest.Session(t)
	probe := bus.Conn(t)
	before := busPeers(t, probe)
	started := false
	err := runShell(io.Discard, func(session *dbus.Conn) error {
		started = true
		if got := busPeers(t, probe); got != before+1 {
			t.Errorf("the shell started with %d connections of its own, want 1", got-before)
		}
		var owner string
		if err := probe.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, shellipc.AppID).Store(&owner); err != nil || session == nil || owner != session.Names()[0] {
			t.Errorf("the application id is owned by %q (%v), want the shell's connection", owner, err)
		}
		return nil
	})
	if err != nil || !started {
		t.Fatalf("runShell = %v, started %v", err, started)
	}
	other := bus.Conn(t)
	if claimed, err := shellipc.Claim(other); err != nil || !claimed {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	before = busPeers(t, probe)
	var stderr bytes.Buffer
	if err := runShell(&stderr, func(*dbus.Conn) error { t.Error("a second shell started"); return nil }); err != nil || stderr.String() != "lys-shell shell is already running\n" {
		t.Errorf("second shell = %v %q", err, stderr.String())
	}
	if got := busPeers(t, probe); got != before {
		t.Errorf("the refused shell left %d connections behind", got-before)
	}
}
