package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/logind/logindtest"
	"github.com/stubbedev/lys-lib/sockdir"
	"github.com/stubbedev/lys-lib/wmipc"

	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sharepreview"
)

const startupBound = 30 * time.Second

func redJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []uint8{0xd0, 0x20, 0x20, 0xff})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func isRed(c color.RGBA) bool { return c.R > 0xa0 && c.G < 0x50 && c.B < 0x50 }

type startedShell struct {
	cmd *exec.Cmd
	log string
}

func (s *startedShell) logs(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(s.log)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func startShellProcess(t *testing.T, wallpaper string, env ...string) *startedShell {
	t.Helper()
	dir := t.TempDir()
	configDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lys-shell")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("wallpaper:\n  wallpaper: %s\nosd:\n  enabled: false\nmodules:\n  brightness:\n    enable-external: false\n", strconv.Quote(wallpaper))
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "shell.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(os.Args[0], "shell")
	cmd.Env = append(append(os.Environ(), "LYS_SHELL_TEST_CLI=1", "LYS_UI_RENDERER=cpu"), env...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &startedShell{cmd: cmd, log: logPath}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			t.Logf("shell log:\n%s", s.logs(t))
		}
	})
	return s
}

func captureCenters(t *testing.T) []color.RGBA {
	t.Helper()
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	outs := c.Outputs()
	if len(outs) == 0 {
		t.Fatal("the compositor has no output")
	}
	centers := make([]color.RGBA, 0, len(outs))
	for _, out := range outs {
		f, err := c.CaptureOutput(out, capture.Options{})
		if err != nil {
			t.Fatal(err)
		}
		img, err := sharepreview.FrameImage(f, out.Transform)
		if err != nil {
			t.Fatal(err)
		}
		b := img.Bounds()
		centers = append(centers, img.RGBAAt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2))
	}
	return centers
}

func captureCenter(t *testing.T) color.RGBA {
	t.Helper()
	return captureCenters(t)[0]
}

func swayUsableHeight(t *testing.T) (usable, whole int) {
	t.Helper()
	var workspaces []struct {
		Rect struct {
			Height int `json:"height"`
		} `json:"rect"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_workspaces"), &workspaces); err != nil {
		t.Fatal(err)
	}
	var outputs []struct {
		Rect struct {
			Height int `json:"height"`
		} `json:"rect"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_outputs"), &outputs); err != nil {
		t.Fatal(err)
	}
	if len(workspaces) == 0 || len(outputs) == 0 {
		t.Fatal("sway reports no workspace or output")
	}
	return workspaces[0].Rect.Height, outputs[0].Rect.Height
}

func barMapped(t *testing.T) bool {
	t.Helper()
	usable, whole := swayUsableHeight(t)
	return usable < whole
}

func awaitBar(t *testing.T, s *startedShell) {
	t.Helper()
	await(t, s, "the bar to map", func() bool { return barMapped(t) })
}

func await(t *testing.T, s *startedShell, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(startupBound); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s\nshell log:\n%s", startupBound, what, s.logs(t))
		}
	}
}

func isBlack(c color.RGBA) bool { return c.R == 0 && c.G == 0 && c.B == 0 }

func startBareSession(t *testing.T) (loginCalls string) {
	t.Helper()
	for _, name := range []string{wmipc.SocketEnv, "HYPRLAND_INSTANCE_SIGNATURE", niri.SocketEnv, mango.SocketEnv, "SWAYSOCK", "XDG_SESSION_ID"} {
		t.Setenv(name, "")
	}
	swaytest.Start(t)
	dbustest.Session(t)
	dbustest.Start(t).UseAsSystemBus(t)
	bin := t.TempDir()
	loginCalls = filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %s\nexit 1\n", strconv.Quote(loginCalls))
	if err := os.WriteFile(filepath.Join(bin, "lys-login"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return loginCalls
}

func TestShellUnderTestNeverReachesTheHostsLogind(t *testing.T) {
	host := logindtest.Start(t, logindtest.Config{Display: "c1"})
	host.SetLockedHint(t, "c1", true)
	host.Bus.UseAsSystemBus(t)
	loginCalls := startBareSession(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	await(t, s, "the lock client to look for logind", func() bool {
		logs := s.logs(t)
		return strings.Contains(logs, "lock: logind unavailable") || strings.Contains(logs, "session was locked at startup")
	})
	if strings.Contains(s.logs(t), "session was locked at startup") {
		t.Error("the shell read a locked session off the host's logind")
	}
	if calls := host.Methods(); len(calls) != 0 {
		t.Errorf("the shell called the host's logind: %v", calls)
	}
	if calls, err := os.ReadFile(loginCalls); err == nil && strings.Contains(string(calls), "lock") {
		t.Errorf("the shell started the lock screen: %s", calls)
	}
	awaitBar(t, s)
	if got := captureCenter(t); !isRed(got) {
		t.Errorf("the bar mapped over %v, want the red wallpaper", got)
	}
}

func TestBarLandsOnAVisibleWallpaper(t *testing.T) {
	startBareSession(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	awaitBar(t, s)
	if got := captureCenter(t); !isRed(got) {
		t.Errorf("the bar mapped over %v, want the red wallpaper", got)
	}
}

func TestBarsLandOnAVisibleWallpaperOnEveryOutput(t *testing.T) {
	startBareSession(t)
	swaytest.Msg(t, "create_output")
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	awaitBar(t, s)
	centers := captureCenters(t)
	if len(centers) != 2 {
		t.Fatalf("%d outputs, want 2", len(centers))
	}
	for i, got := range centers {
		if !isRed(got) {
			t.Errorf("output %d shows %v under the bar, want the red wallpaper", i, got)
		}
	}
}

func TestDecodeThatHangsShowsTheBuiltinWallpaperUnderTheBar(t *testing.T) {
	startBareSession(t)
	fifo := filepath.Join(t.TempDir(), "held.jpg")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, fifo)
	awaitBar(t, s)
	got := captureCenter(t)
	if isBlack(got) {
		t.Fatalf("the bar mapped over a black desktop while the image decodes")
	}
	if !strings.Contains(s.logs(t), "is not ready after") {
		t.Errorf("the slow image logged nothing:\n%s", s.logs(t))
	}
	if err := os.WriteFile(fifo, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	await(t, s, "the image to replace the built-in wallpaper", func() bool { return isRed(captureCenter(t)) })
}

func TestUndecodableImageShowsTheBuiltinWallpaperUnderTheBar(t *testing.T) {
	startBareSession(t)
	image := filepath.Join(t.TempDir(), "broken.jpg")
	if err := os.WriteFile(image, []byte("this is no image"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	awaitBar(t, s)
	if got := captureCenter(t); isBlack(got) {
		t.Fatalf("the bar mapped over a black desktop: %v", got)
	}
	if !strings.Contains(s.logs(t), "cannot decode wallpaper") {
		t.Errorf("the broken image logged nothing:\n%s", s.logs(t))
	}
}

func silentBus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "bus")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err == nil {
				held = append(held, c)
			}
		}
	}()
	return "unix:path=" + path
}

func TestWallpaperDoesNotWaitForServices(t *testing.T) {
	startBareSession(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image, "DBUS_SYSTEM_BUS_ADDRESS="+silentBus(t))
	await(t, s, "the wallpaper to show while a service hangs", func() bool { return isRed(captureCenter(t)) })
	if barMapped(t) {
		t.Error("the bar mapped before the services it waits for")
	}
}
