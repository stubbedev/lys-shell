package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

var shellSessionNames = []string{
	"dev.stubbe.lys.shell", "dev.stubbe.lys.Shell1", "dev.stubbe.lys.Wallpaper1",
	"dev.stubbe.lys.Audio1", "dev.stubbe.lys.Brightness1", "dev.stubbe.lys.Recorder1",
	"dev.stubbe.lys.Notifications1", "org.freedesktop.Notifications", "dev.stubbe.lys.IdleInhibit1",
	"dev.stubbe.lys.Media1", "dev.stubbe.lys.SystemTray1", "dev.stubbe.lys.Screenshot1",
	"dev.stubbe.lys.SharePicker1", "dev.stubbe.lys.PortalDialogs1", "dev.stubbe.lys.FileChooser1",
	"dev.stubbe.lys.Print1",
}

func TestTheShellServesItsSessionNamesOnOneConnection(t *testing.T) {
	startBareSession(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	awaitBar(t, s)
	conn := dbustest.SessionConn(t)
	owners := map[string]string{}
	await(t, s, "every session name", func() bool {
		for _, name := range shellSessionNames {
			var owner string
			if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, name).Store(&owner); err != nil {
				return false
			}
			owners[name] = owner
		}
		return true
	})
	shared := owners["dev.stubbe.lys.shell"]
	for _, name := range shellSessionNames {
		if owners[name] != shared {
			t.Errorf("%s is served on %s, the application id on %s", name, owners[name], shared)
		}
	}
	var pid uint32
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetConnectionUnixProcessID", 0, shared).Store(&pid); err != nil || int(pid) != s.cmd.Process.Pid {
		t.Errorf("the shared connection belongs to pid %d (%v), want the shell's %d", pid, err, s.cmd.Process.Pid)
	}
}

func connectionSerial(t *testing.T, connect func(...dbus.ConnOption) (*dbus.Conn, error)) int {
	t.Helper()
	conn, err := connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	name := conn.Names()[0]
	_, serial, ok := strings.Cut(name, ".")
	if !ok {
		t.Fatalf("unique name %q has no serial", name)
	}
	n, err := strconv.Atoi(serial)
	if err != nil {
		t.Fatalf("unique name %q: %v", name, err)
	}
	return n
}

func TestTheShellOpensOneConnectionPerBusBeforeItsBarMaps(t *testing.T) {
	startBareSession(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	session, system := connectionSerial(t, dbus.ConnectSessionBus), connectionSerial(t, dbus.ConnectSystemBus)
	s := startShellProcess(t, image)
	awaitBar(t, s)
	sessionOpened := connectionSerial(t, dbus.ConnectSessionBus) - session - 1
	systemOpened := connectionSerial(t, dbus.ConnectSystemBus) - system - 1
	if sessionOpened != 1 || systemOpened != 1 {
		t.Errorf("by the time the bar mapped the shell had opened %d session and %d system bus connections, want 1 and 1\nshell log:\n%s", sessionOpened, systemOpened, s.logs(t))
	}
}
