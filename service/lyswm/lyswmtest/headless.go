package lyswmtest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/service/lyswm"
)

const headlessConfig = "xwayland:\n  enabled: false\n"

// Headless runs a real lys-wm on its headless backend with the software
// renderer in a private runtime directory, without Xwayland or a session
// bus, and points WAYLAND_DISPLAY, XDG_RUNTIME_DIR and the lys-wm socket
// at it for the test. Its log is shown when the test fails. A host
// without lys-wm skips the test.
func Headless(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("lys-wm"); err != nil {
		t.Skip("lys-wm is not installed (the nix dev shell carries it)")
	}
	dir := sockdir.Dir(t)
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(headlessConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		logs bytes.Buffer
	)
	out := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logs.Write(p)
	})
	cmd := exec.Command("lys-wm", "-backend", "headless", "-renderer", "soft", "-config", config) //nolint:gosec // lys-wm from PATH, for a test
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir, "WAYLAND_DISPLAY=", "DISPLAY=", lyswm.SocketEnv+"=", "DBUS_SESSION_BUS_ADDRESS=unix:path="+filepath.Join(dir, "no-bus"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	exited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(timeout):
			_ = cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			mu.Lock()
			t.Logf("lys-wm said:\n%s", logs.String())
			mu.Unlock()
		}
	})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		select {
		case <-exited:
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("lys-wm exited: %v\n%s", waitErr, logs.String())
		default:
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("lys-wm never answered on its socket:\n%s", logs.String())
		}
		sockets, _ := filepath.Glob(filepath.Join(dir, "lys-wm", "*.sock"))
		if len(sockets) == 0 {
			continue
		}
		t.Setenv("XDG_RUNTIME_DIR", dir)
		t.Setenv(lyswm.SocketEnv, sockets[0])
		t.Setenv("WAYLAND_DISPLAY", strings.TrimSuffix(filepath.Base(sockets[0]), ".sock"))
		if _, err := lyswm.Outputs(); err == nil {
			return
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
