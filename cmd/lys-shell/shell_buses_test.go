package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
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
