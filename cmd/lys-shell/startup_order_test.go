package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/wmipc"

	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/sharepreview"
	"github.com/stubbedev/lys-shell/service/shellipc"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/shell/filechooser"
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

type monitorWallpaper struct{ monitor, path string }

func startShellProcess(t *testing.T, wallpaper string, env ...string) *startedShell {
	t.Helper()
	return startShellWithWallpapers(t, wallpaper, nil, env...)
}

func startShellWithWallpapers(t *testing.T, wallpaper string, monitors []monitorWallpaper, env ...string) *startedShell {
	t.Helper()
	dir := t.TempDir()
	configDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lys-shell")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var config strings.Builder
	fmt.Fprintf(&config, "wallpaper:\n  wallpaper: %s\n", strconv.Quote(wallpaper))
	if len(monitors) > 0 {
		config.WriteString("  monitors:\n")
	}
	for _, m := range monitors {
		fmt.Fprintf(&config, "    - name: %s\n      wallpaper: %s\n", strconv.Quote(m.monitor), strconv.Quote(m.path))
	}
	config.WriteString("osd:\n  enabled: false\nmodules:\n  brightness:\n    enable-external: false\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config.String()), 0o600); err != nil {
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

type outputCenter struct {
	output string
	color  color.RGBA
}

func captureCenters(t *testing.T) []outputCenter {
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
	centers := make([]outputCenter, 0, len(outs))
	for _, out := range outs {
		f, err := c.CaptureOutput(out, capture.Options{})
		if err != nil {
			t.Fatalf("capture %s: %v", out.Name, err)
		}
		img, err := sharepreview.FrameImage(f, out.Transform)
		if err != nil {
			t.Fatalf("frame of %s: %v", out.Name, err)
		}
		b := img.Bounds()
		centers = append(centers, outputCenter{output: out.Name, color: img.RGBAAt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2)})
	}
	return centers
}

func captureCenter(t *testing.T) color.RGBA {
	t.Helper()
	return captureCenters(t)[0].color
}

func captureCenterOf(t *testing.T, output string) color.RGBA {
	t.Helper()
	centers := captureCenters(t)
	i := slices.IndexFunc(centers, func(c outputCenter) bool { return c.output == output })
	if i < 0 {
		t.Fatalf("no capture of %s among %v", output, centers)
	}
	return centers[i].color
}

type swayRect struct {
	Height int `json:"height"`
}

type swayWorkspace struct {
	Output  string   `json:"output"`
	Visible bool     `json:"visible"`
	Rect    swayRect `json:"rect"`
}

func barsMapped(t *testing.T) map[string]bool {
	t.Helper()
	var workspaces []swayWorkspace
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_workspaces"), &workspaces); err != nil {
		t.Fatal(err)
	}
	var outputs []struct {
		Name string   `json:"name"`
		Rect swayRect `json:"rect"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_outputs"), &outputs); err != nil {
		t.Fatal(err)
	}
	if len(workspaces) == 0 || len(outputs) == 0 {
		t.Fatal("sway reports no workspace or output")
	}
	mapped := make(map[string]bool, len(outputs))
	for _, out := range outputs {
		i := slices.IndexFunc(workspaces, func(ws swayWorkspace) bool { return ws.Output == out.Name && ws.Visible })
		mapped[out.Name] = i >= 0 && workspaces[i].Rect.Height < out.Rect.Height
	}
	return mapped
}

func barMapped(t *testing.T) bool {
	t.Helper()
	for _, mapped := range barsMapped(t) {
		if mapped {
			return true
		}
	}
	return false
}

func barMappedOn(t *testing.T, output string) bool {
	t.Helper()
	return barsMapped(t)[output]
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
	await(t, s, "a bar on every output", func() bool {
		n := 0
		for _, mapped := range barsMapped(t) {
			if mapped {
				n++
			}
		}
		return n == 2
	})
	centers := captureCenters(t)
	if len(centers) != 2 {
		t.Fatalf("%d outputs, want 2", len(centers))
	}
	for _, got := range centers {
		if !isRed(got.color) {
			t.Errorf("%s shows %v under the bar, want the red wallpaper", got.output, got.color)
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
	feed(t, fifo, redJPEG(t))
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

const outputWaitLog = "the compositor has no output"

func swayOutputNames(t *testing.T) []string {
	t.Helper()
	var outputs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_outputs"), &outputs); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(outputs))
	for _, o := range outputs {
		names = append(names, o.Name)
	}
	return names
}

func unplugEveryOutput(t *testing.T) {
	t.Helper()
	for _, name := range swayOutputNames(t) {
		swaytest.Msg(t, "output", name, "unplug")
	}
	if left := swayOutputNames(t); len(left) != 0 {
		t.Fatalf("sway still shows %q after unplugging every output", left)
	}
}

var shellServices = []string{notifications.Interface, filechooser.ServiceName, sni.WatcherName, shellipc.ServiceName}

func serviceOwners(bus *dbus.Conn) (map[string]string, error) {
	owners := make(map[string]string, len(shellServices))
	for _, name := range shellServices {
		var owner string
		if err := bus.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, name).Store(&owner); err != nil {
			return nil, fmt.Errorf("owner of %s: %w", name, err)
		}
		owners[name] = owner
	}
	return owners, nil
}

func requireSameServices(t *testing.T, s *startedShell, bus *dbus.Conn, want map[string]string, when string) {
	t.Helper()
	got, err := serviceOwners(bus)
	if err != nil {
		t.Fatalf("%s: %v\nshell log:\n%s", when, err, s.logs(t))
	}
	if !maps.Equal(got, want) {
		t.Fatalf("%s: the services are owned by %v, want the shell's first %v", when, got, want)
	}
}

func TestShellWithoutAnOutputWaitsForOneAndKeepsItsServices(t *testing.T) {
	startBareSession(t)
	unplugEveryOutput(t)
	image := filepath.Join(t.TempDir(), "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShellProcess(t, image)
	bus := dbustest.SessionConn(t)
	var owners map[string]string
	await(t, s, "the shell's services without an output", func() bool {
		var err error
		owners, err = serviceOwners(bus)
		return err == nil
	})
	if n := strings.Count(s.logs(t), outputWaitLog); n != 1 {
		t.Fatalf("the wait for an output was logged %d times, want once:\n%s", n, s.logs(t))
	}

	swaytest.Msg(t, "create_output")
	awaitBar(t, s)
	await(t, s, "the wallpaper on the first output", func() bool { return isRed(captureCenter(t)) })
	requireSameServices(t, s, bus, owners, "after the first output appeared")

	unplugEveryOutput(t)
	await(t, s, "the shell to log the second wait", func() bool { return strings.Count(s.logs(t), outputWaitLog) == 2 })
	requireSameServices(t, s, bus, owners, "with every output unplugged")

	swaytest.Msg(t, "create_output")
	awaitBar(t, s)
	await(t, s, "the wallpaper on the returning output", func() bool { return isRed(captureCenter(t)) })
	requireSameServices(t, s, bus, owners, "after an output came back")
	if n := strings.Count(s.logs(t), outputWaitLog); n != 2 {
		t.Errorf("the two waits for an output were logged %d times:\n%s", n, s.logs(t))
	}
}

const pluggedOutput = "HEADLESS-2"

func startShellAwaitingAPluggedOutput(t *testing.T) (s *startedShell, heldImage string) {
	t.Helper()
	startBareSession(t)
	dir := t.TempDir()
	image := filepath.Join(dir, "red.jpg")
	if err := os.WriteFile(image, redJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	heldImage = filepath.Join(dir, "held.jpg")
	if err := syscall.Mkfifo(heldImage, 0o600); err != nil {
		t.Fatal(err)
	}
	s = startShellWithWallpapers(t, image, []monitorWallpaper{{monitor: pluggedOutput, path: heldImage}})
	awaitBar(t, s)
	swaytest.Msg(t, "create_output")
	if names := swayOutputNames(t); !slices.Contains(names, pluggedOutput) {
		t.Fatalf("sway added none of %q: %q", pluggedOutput, names)
	}
	return s, heldImage
}

func requireNoBarWhileTheImageIsHeld(t *testing.T, s *startedShell, hold time.Duration) {
	t.Helper()
	for until := time.Now().Add(hold); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		if barMappedOn(t, pluggedOutput) {
			t.Fatalf("the bar mapped on %s while its wallpaper was still decoding; it shows %v\nshell log:\n%s", pluggedOutput, captureCenterOf(t, pluggedOutput), s.logs(t))
		}
	}
}

func awaitBarOverAWallpaper(t *testing.T, s *startedShell) {
	t.Helper()
	await(t, s, "the bar on "+pluggedOutput, func() bool {
		if !barMappedOn(t, pluggedOutput) {
			return false
		}
		if got := captureCenterOf(t, pluggedOutput); isBlack(got) {
			t.Fatalf("the bar mapped on %s over a black desktop\nshell log:\n%s", pluggedOutput, s.logs(t))
		}
		return true
	})
}

func feed(t *testing.T, fifo string, data []byte) {
	t.Helper()
	fed := make(chan error, 1)
	go func() { fed <- os.WriteFile(fifo, data, 0o600) }()
	t.Cleanup(func() {
		select {
		case err := <-fed:
			if err != nil {
				t.Errorf("feed %s: %v", fifo, err)
			}
		case <-time.After(startupBound):
			t.Errorf("the shell never opened %s to read it in %v", fifo, startupBound)
		}
	})
}

func TestBarOnAPluggedOutputWaitsForItsWallpaper(t *testing.T) {
	s, held := startShellAwaitingAPluggedOutput(t)
	requireNoBarWhileTheImageIsHeld(t, s, 500*time.Millisecond)
	feed(t, held, redJPEG(t))
	awaitBarOverAWallpaper(t, s)
	await(t, s, "the image on "+pluggedOutput, func() bool { return isRed(captureCenterOf(t, pluggedOutput)) })
}

func TestBarOnAPluggedOutputMapsOverTheBuiltinWallpaperWhenItsImageHangs(t *testing.T) {
	s, held := startShellAwaitingAPluggedOutput(t)
	requireNoBarWhileTheImageIsHeld(t, s, 500*time.Millisecond)
	awaitBarOverAWallpaper(t, s)
	if !strings.Contains(s.logs(t), pluggedOutput+": "+held+" is not ready after") {
		t.Errorf("the hanging image on %s logged nothing:\n%s", pluggedOutput, s.logs(t))
	}
	feed(t, held, redJPEG(t))
	await(t, s, "the image to replace the built-in wallpaper on "+pluggedOutput, func() bool { return isRed(captureCenterOf(t, pluggedOutput)) })
}
