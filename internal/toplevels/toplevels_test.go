package toplevels

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/wlr"
)

// A window is listed from its first done batch, with the batch's app id
// and activated state; a done that changes nothing announces nothing,
// and a closed window leaves the list and frees its handle.
func TestTrackerAppliesBatchesOnDone(t *testing.T) {
	tr := newTracker()
	var changes atomic.Int32
	tr.changed = func() { changes.Add(1) }
	destroyed := false
	h := tr.add(func() { destroyed = true })
	h.HandleZwlrForeignToplevelHandleV1AppId(wlr.ZwlrForeignToplevelHandleV1AppIdEvent{AppId: "org.example.App"})
	h.HandleZwlrForeignToplevelHandleV1State(wlr.ZwlrForeignToplevelHandleV1StateEvent{State: []int32{wlr.ZwlrForeignToplevelHandleV1StateMaximized, wlr.ZwlrForeignToplevelHandleV1StateActivated}})
	if got := tr.windows(); len(got) != 0 || changes.Load() != 0 {
		t.Fatalf("before done: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if got := tr.windows(); !slices.Equal(got, []Window{{AppID: "org.example.App", Activated: true}}) || changes.Load() != 1 {
		t.Errorf("after done: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if changes.Load() != 1 {
		t.Errorf("an unchanged batch announced a change")
	}
	h.HandleZwlrForeignToplevelHandleV1State(wlr.ZwlrForeignToplevelHandleV1StateEvent{State: []int32{}})
	h.HandleZwlrForeignToplevelHandleV1Done(wlr.ZwlrForeignToplevelHandleV1DoneEvent{})
	if got := tr.windows(); !slices.Equal(got, []Window{{AppID: "org.example.App"}}) || changes.Load() != 2 {
		t.Errorf("deactivated: %v, %d changes", got, changes.Load())
	}
	h.HandleZwlrForeignToplevelHandleV1Closed(wlr.ZwlrForeignToplevelHandleV1ClosedEvent{})
	if got := tr.windows(); len(got) != 0 || changes.Load() != 3 || !destroyed {
		t.Errorf("closed: %v, %d changes, destroyed %v", got, changes.Load(), destroyed)
	}
	// A window closing before its first batch was never listed.
	early := tr.add(func() {})
	early.HandleZwlrForeignToplevelHandleV1Closed(wlr.ZwlrForeignToplevelHandleV1ClosedEvent{})
	if changes.Load() != 3 {
		t.Error("a window never listed announced its close")
	}
}

// startSway runs a private headless sway and points WAYLAND_DISPLAY and
// XDG_RUNTIME_DIR at it for the test.
func startSway(t *testing.T) *os.Process {
	t.Helper()
	if _, err := exec.LookPath("sway"); err != nil {
		t.Skip("sway is not installed (the nix dev shell carries it)")
	}
	dir, err := os.MkdirTemp("", "lys-sway")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sway", "-c", config)
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "WLR_LIBINPUT_NO_DEVICES=1")
	cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "WAYLAND_DISPLAY=") || strings.HasPrefix(kv, "SWAYSOCK=")
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("sway never opened its socket")
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

// waitWindows polls the watcher until want holds.
func waitWindows(t *testing.T, w *Watcher, want func([]Window) bool) []Window {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got := w.Windows()
		if want(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("windows = %v", got)
		}
	}
}

// probeEnv makes the test binary a probe client: it maps one window
// under this app id until its stdin closes.
const probeEnv = "LYS_TOPLEVELS_PROBE"

func TestMain(m *testing.M) {
	if appID := os.Getenv(probeEnv); appID != "" {
		os.Exit(runProbe(appID))
	}
	os.Exit(m.Run())
}

// runProbe maps the probe window and exits when stdin closes; the
// window goes away with the connection.
func runProbe(appID string) int {
	sess, err := app.Connect()
	if err != nil {
		return 1
	}
	a := app.NewApplication(sess)
	if _, err := a.NewWindow(app.WindowConfig{Title: "probe", AppID: appID, Width: 200, Height: 100, Root: widget.NewBox(widget.Column, 0, 0)}); err != nil {
		return 1
	}
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()
	_ = a.Run()
	return 1
}

// startProbe maps a window under appID in a child process (a separate
// client, as a real app is); closing the returned writer ends it.
func startProbe(t *testing.T, appID string) io.Closer {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), probeEnv+"="+appID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	return stdin
}

// Against a real compositor: a window that maps is listed under its app
// id and activated, and leaves the list when it closes; Close ends the
// watcher without an error.
func TestWatcherFollowsAWindow(t *testing.T) {
	startSway(t)
	var changes atomic.Int32
	w, err := Watch(func() { changes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Windows(); len(got) != 0 {
		t.Fatalf("an empty session lists %v", got)
	}
	probe := startProbe(t, "org.example.Probe")
	waitWindows(t, w, func(ws []Window) bool {
		return slices.Equal(ws, []Window{{AppID: "org.example.Probe", Activated: true}})
	})
	if changes.Load() == 0 {
		t.Error("the window mapped without a change")
	}
	_ = probe.Close()
	waitWindows(t, w, func(ws []Window) bool { return len(ws) == 0 })
	w.Close()
	if err := w.Err(); err != nil {
		t.Errorf("Close ended the watcher with %v", err)
	}
}

// A compositor going away ends the watcher with an error.
func TestWatcherEndsWithTheCompositor(t *testing.T) {
	sway := startSway(t)
	w, err := Watch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sway.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Ended():
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher outlived its compositor")
	}
	if w.Err() == nil {
		t.Error("the compositor went away without an error")
	}
}
