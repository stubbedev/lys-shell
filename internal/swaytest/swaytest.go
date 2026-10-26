// Package swaytest runs a private headless sway for tests, so Wayland
// clients connect to it instead of the developer's session.
package swaytest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/internal/dbustest"
)

// Start runs a private headless sway rendering with pixman, env
// overriding its environment (WLR_RENDERER=vulkan, a render node), and
// points WAYLAND_DISPLAY and XDG_RUNTIME_DIR at it for the test. It gets
// a private session bus: without one, nixpkgs' wrapper runs it under
// dbus-run-session, whose process is not sway's. Its log is shown when
// it fails to start. A host without sway skips the test.
func Start(t *testing.T, env ...string) *os.Process {
	t.Helper()
	if _, err := exec.LookPath("sway"); err != nil {
		t.Skip("sway is not installed (the nix dev shell carries it)")
	}
	dir := sockdir.Dir(t)
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "log")) //nolint:gosec // a file in the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("sway", "-c", config) //nolint:gosec // sway from PATH, for a test
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir, "DBUS_SESSION_BUS_ADDRESS="+dbustest.Start(t).Address, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "WLR_LIBINPUT_NO_DEVICES=1")
	cmd.Env = append(slices.DeleteFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "WAYLAND_DISPLAY=") || strings.HasPrefix(kv, "SWAYSOCK=")
	}), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			out, _ := os.ReadFile(log.Name())
			t.Fatalf("sway never opened its socket:\n%s", out)
		}
		sockets, _ := filepath.Glob(filepath.Join(dir, "wayland-*"))
		sockets = slices.DeleteFunc(sockets, func(s string) bool { return strings.HasSuffix(s, ".lock") })
		if len(sockets) > 0 {
			t.Setenv("XDG_RUNTIME_DIR", dir)
			t.Setenv("WAYLAND_DISPLAY", filepath.Base(sockets[0]))
			return cmd.Process
		}
	}
}
