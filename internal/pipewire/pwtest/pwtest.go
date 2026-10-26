// Package pwtest starts a private PipeWire daemon for tests, so
// producers and consumers link without touching the user's session, and
// runs libpipewire consumers against it as the reference a producer is
// checked by.
package pwtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/sockdir"
)

// daemonConf is a private PipeWire daemon: the native protocol, the
// client-node and adapter modules a pw_stream needs, link creation,
// and a dummy driver so a linked graph runs with no devices.
const daemonConf = `
context.properties = {
    core.daemon = true
    core.name   = pipewire-0
    support.dbus = false
}
context.spa-libs = {
    support.* = support/libspa-support
    video.convert.* = videoconvert/libspa-videoconvert
}
context.modules = [
    { name = libpipewire-module-protocol-native }
    { name = libpipewire-module-metadata }
    { name = libpipewire-module-spa-node-factory }
    { name = libpipewire-module-client-node }
    { name = libpipewire-module-access }
    { name = libpipewire-module-adapter }
    { name = libpipewire-module-link-factory }
]
context.objects = [
    { factory = spa-node-factory
        args = {
            factory.name    = support.node.driver
            node.name       = Dummy-Driver
            node.group      = pipewire.dummy
            priority.driver = 20000
        }
    }
]
`

// Daemon is a private pipewire a test runs.
type Daemon struct {
	cmd  *exec.Cmd
	once sync.Once
}

// Start runs a private pipewire for the test and points the
// client library at it. A missing pipewire fails the test: the dev
// shell provides one.
func Start(t *testing.T) *Daemon {
	t.Helper()
	bin, err := exec.LookPath("pipewire")
	if err != nil {
		t.Fatalf("pipewire not on PATH: %v", err)
	}
	dir := sockdir.Dir(t)
	conf := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(conf, []byte(daemonConf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPEWIRE_RUNTIME_DIR", dir)
	t.Setenv("PIPEWIRE_REMOTE", "pipewire-0")
	cmd := exec.Command(bin, "-c", conf) //nolint:gosec
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cmd: cmd}
	t.Cleanup(d.Kill)
	sock := filepath.Join(dir, "pipewire-0")
	for range 100 {
		if _, err := os.Stat(sock); err == nil {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pipewire did not create its socket")
	return nil
}

// Kill ends the daemon at once, as a crash would, and reaps it.
func (d *Daemon) Kill() {
	d.once.Do(func() {
		_ = d.cmd.Process.Kill()
		_ = d.cmd.Wait()
	})
}
