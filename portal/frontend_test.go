package portal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/pipewire"
	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

const (
	frontendName     = "org.freedesktop.portal.Desktop"
	frontendPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	publicRemote     = "org.freedesktop.portal.RemoteDesktop"
	publicScreenCast = "org.freedesktop.portal.ScreenCast"
)

// frontend is the real xdg-desktop-portal on a rig's bus, routed to the
// backend by the shipped manifests: the contract an app sees, not the
// one the backend's own tests assume.
type frontend struct {
	conn   *dbus.Conn
	obj    dbus.BusObject
	tokens int
}

// lockedBuffer is the frontend's log, written by the process's pipe
// and read at cleanup.
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

// startFrontend runs $XDG_DESKTOP_PORTAL (the dev shell names
// nixpkgs' xdg-desktop-portal) on r's bus with lys-shell.portal and
// lys-shell-portals.conf as its only configuration and dataHome as its
// XDG data home, and waits for it to own its name. Its log is shown
// when the test fails.
func startFrontend(t *testing.T, r *rig, dataHome string) *frontend {
	t.Helper()
	bin := os.Getenv("XDG_DESKTOP_PORTAL")
	if bin == "" {
		t.Fatal("XDG_DESKTOP_PORTAL is unset: the dev shell names the xdg-desktop-portal binary")
	}
	portals := t.TempDir()
	for _, name := range []string{"lys-shell.portal", "lys-shell-portals.conf"} {
		if err := os.WriteFile(filepath.Join(portals, name), []byte(readResource(t, name)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	log := &lockedBuffer{}
	cmd := exec.Command(bin, "--verbose")
	cmd.Env = append(os.Environ(),
		"DBUS_SESSION_BUS_ADDRESS="+r.bus.Address,
		"XDG_DESKTOP_PORTAL_DIR="+portals,
		"XDG_CURRENT_DESKTOP=lys-shell",
		"XDG_DATA_HOME="+dataHome,
		"XDG_DATA_DIRS="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("xdg-desktop-portal:\n%s", log.String())
		}
	})
	conn := r.bus.Conn(t)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var owned bool
		if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, frontendName).Store(&owned); err != nil {
			t.Fatal(err)
		}
		if owned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("xdg-desktop-portal never owned %s", frontendName)
		}
	}
	return &frontend{conn: conn, obj: conn.Object(frontendName, frontendPath)}
}

// request calls an interactive public method with a fresh handle_token
// in its options (the last argument) and returns the Response emitted
// on the request object the token names.
func (f *frontend) request(t *testing.T, method string, args ...any) (uint32, map[string]dbus.Variant) {
	t.Helper()
	f.tokens++
	token := "lys" + strconv.Itoa(f.tokens)
	args[len(args)-1].(Vardict)["handle_token"] = dbus.MakeVariant(token)
	sender := strings.ReplaceAll(strings.TrimPrefix(f.conn.Names()[0], ":"), ".", "_")
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	if err := f.conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchMember("Response")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 4)
	f.conn.Signal(signals)
	defer f.conn.RemoveSignal(signals)
	var got dbus.ObjectPath
	if err := f.obj.Call(method, 0, args...).Store(&got); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if got != path {
		t.Fatalf("%s answered request %s, want %s", method, got, path)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case s := <-signals:
			if s.Path != path || s.Name != "org.freedesktop.portal.Request.Response" {
				continue
			}
			var code uint32
			var results map[string]dbus.Variant
			if err := dbus.Store(s.Body, &code, &results); err != nil {
				t.Fatal(err)
			}
			return code, results
		case <-timeout:
			t.Fatalf("%s: no Response on %s", method, path)
		}
	}
}

// session creates a session through iface's CreateSession and returns
// its handle.
func (f *frontend) session(t *testing.T, iface string) dbus.ObjectPath {
	t.Helper()
	code, res := f.request(t, iface+".CreateSession", Vardict{"session_handle_token": dbus.MakeVariant("lyssession")})
	handle, _ := res["session_handle"].Value().(string)
	if code != ResponseSuccess || handle == "" {
		t.Fatalf("CreateSession = %d %v", code, res)
	}
	return dbus.ObjectPath(handle)
}

// An app's attachment_fds reach the backend as the paths the frontend
// resolves, and from there xdg-email; a mail client missing fails the
// request instead of composing without the files.
func TestFrontendHandsEmailAttachmentsOver(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(doc, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := filepath.EvalSymlinks(doc)
	if err != nil {
		t.Fatal(err)
	}
	compose := func(f *frontend) uint32 {
		file, err := os.Open(doc)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		code, _ := f.request(t, "org.freedesktop.portal.Email.ComposeEmail", "", Vardict{
			"address":        dbus.MakeVariant("a@b.com"),
			"attachment_fds": dbus.MakeVariant([]dbus.UnixFD{dbus.UnixFD(file.Fd())}),
		})
		return code
	}

	r, s := spawnRig(t)
	if code := compose(startFrontend(t, r, t.TempDir())); code != ResponseSuccess {
		t.Errorf("ComposeEmail = %d", code)
	}
	want := []string{"xdg-email", "--utf8", "--attach", doc, "a@b.com"}
	if got := s.argv.Load(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("spawned %q, want %q", got, want)
	}

	r, _ = spawnRig(t, "xdg-email")
	if code := compose(startFrontend(t, r, t.TempDir())); code != ResponseOther {
		t.Errorf("ComposeEmail without xdg-email = %d, want other", code)
	}
}

// OpenURI with ask shows the AppChooser the handlers by the ids the
// frontend uses, and the frontend launches the one chosen; a cancel
// launches nothing.
func TestFrontendOpenURIAsksTheAppChooser(t *testing.T) {
	r := newRig(t, nil)
	d := serveDialogs(t, r, true)
	data := t.TempDir()
	opened := filepath.Join(t.TempDir(), "opened")
	script := filepath.Join(t.TempDir(), "open")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s %s' \"$1\" \"$2\" > '"+opened+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	apps := filepath.Join(data, "applications")
	if err := os.Mkdir(apps, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"org.a.App", "org.chosen.App"} {
		entry := "[Desktop Entry]\nType=Application\nName=" + id + "\nExec=" + script + " " + id + " %u\nMimeType=x-scheme-handler/lystest;\n"
		if err := os.WriteFile(filepath.Join(apps, id+".desktop"), []byte(entry), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// GIO looks handlers up in mimeinfo.cache, never in the entries.
	cache := "[MIME Cache]\nx-scheme-handler/lystest=org.a.App.desktop;org.chosen.App.desktop;\n"
	if err := os.WriteFile(filepath.Join(apps, "mimeinfo.cache"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	f := startFrontend(t, r, data)

	open := func() uint32 {
		code, _ := f.request(t, "org.freedesktop.portal.OpenURI.OpenURI", "", "lystest:hello", Vardict{"ask": dbus.MakeVariant(true)})
		return code
	}
	if code := open(); code != ResponseSuccess {
		t.Fatalf("OpenURI = %d", code)
	}
	asked, _ := d.asked.Load()[0].(portaldialogs.ChooserRequest)
	if got := slices.Sorted(slices.Values(asked.Choices)); !slices.Equal(got, []string{"org.a.App", "org.chosen.App"}) {
		t.Errorf("the chooser offered %q", asked.Choices)
	}
	waitFor(t, func() bool {
		b, err := os.ReadFile(opened)
		return err == nil && string(b) == "org.chosen.App lystest:hello"
	})

	if err := os.Remove(opened); err != nil {
		t.Fatal(err)
	}
	d.yes.Store(false)
	if code := open(); code != ResponseCancelled {
		t.Errorf("OpenURI cancelled = %d", code)
	}
	if _, err := os.Stat(opened); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a cancelled choice launched an app: %v", err)
	}
}

// A remote-desktop session that shares a screen gives the frontend
// the streams it needs to place absolute motion: a point inside the
// stream reaches the virtual pointer mapped onto the output, one
// outside every stream is refused by the frontend, and a stream the
// session does not run is refused by the backend. A finished scroll
// reaches the pointer as a finger scroll and its stop.
func TestFrontendDrivesRemoteDesktopThroughTheScreenStreams(t *testing.T) {
	r, sink := remoteRig(t, nil)
	serveDialogs(t, r, true)
	serveSharePicker(t, r, "/screen:DP-2")
	f := startFrontend(t, r, t.TempDir())

	session := f.session(t, publicRemote)
	if code, _ := f.request(t, publicRemote+".SelectDevices", session, Vardict{"types": dbus.MakeVariant(devicePointer)}); code != ResponseSuccess {
		t.Fatalf("SelectDevices = %d", code)
	}
	if code, _ := f.request(t, publicScreenCast+".SelectSources", session, Vardict{"types": dbus.MakeVariant(sourceMonitor)}); code != ResponseSuccess {
		t.Fatalf("SelectSources = %d", code)
	}
	code, res := f.request(t, publicRemote+".Start", session, "", Vardict{})
	if code != ResponseSuccess || res["devices"].Value() != devicePointer || res["clipboard_enabled"].Value() != false {
		t.Fatalf("Start = %d %v", code, res)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || pair(streams[0].Props["size"]) != [2]int32{1920, 1080} {
		t.Fatalf("streams = %+v", streams)
	}
	node := streams[0].Node

	move := func(stream uint32, x, y float64) error {
		return f.obj.Call(publicRemote+".NotifyPointerMotionAbsolute", 0, session, Vardict{}, stream, x, y).Err
	}
	if err := move(node, 5000, 5); err == nil {
		t.Error("the frontend took a point outside every stream")
	}
	for _, m := range []struct {
		stream uint32
		x, y   float64
	}{{node + 99, 1, 1}, {node, 100.5, 5}, {node, 0, 0}} {
		if err := move(m.stream, m.x, m.y); err != nil {
			t.Fatalf("NotifyPointerMotionAbsolute(%d, %v, %v): %v", m.stream, m.x, m.y, err)
		}
	}
	// A scroll with finish ends with a stop on both axes.
	if err := f.obj.Call(publicRemote+".NotifyPointerAxis", 0, session, Vardict{"finish": dbus.MakeVariant(true)}, 0.0, 2.0).Err; err != nil {
		t.Fatalf("NotifyPointerAxis: %v", err)
	}
	// DP-2 is at (1280, 0) in a layout 3200x1080, in 256ths of a pixel.
	want := []string{"abs 353408 1280 819200 276480", "abs 327680 0 819200 276480", "axis finger 0 2", "stop finger 0", "stop finger 1"}
	waitFor(t, func() bool { return len(sink.events.Load()) >= len(want) })
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("input = %q, want %q", got, want)
	}
}

// PreparePrint's page setup reaches the app under the key the frontend
// forwards, with the token; a declined dialog returns neither.
func TestFrontendReturnsThePrintPageSetup(t *testing.T) {
	prepare := func(granted bool) (uint32, map[string]dbus.Variant) {
		r := newRig(t, nil)
		servePrinter(t, r, &fakePrinter{granted: granted})
		return startFrontend(t, r, t.TempDir()).request(t, "org.freedesktop.portal.Print.PreparePrint", "", "Doc", Vardict{}, Vardict{}, Vardict{})
	}
	code, res := prepare(true)
	setup, _ := res["page-setup"].Value().(map[string]dbus.Variant)
	if code != ResponseSuccess || res["token"].Value() != uint32(7) || setup["Name"].Value() != "iso_a4" || setup["Orientation"].Value() != "landscape" {
		t.Errorf("PreparePrint = %d %v", code, res)
	}
	if code, res := prepare(false); code != ResponseCancelled || res["page-setup"] != (dbus.Variant{}) || res["token"] != (dbus.Variant{}) {
		t.Errorf("PreparePrint declined = %d %v", code, res)
	}
}

// pattern is a capture source that needs no compositor: every frame is
// one byte value.
type pattern struct{}

func (pattern) Fill(dst []byte) ([]pipewire.Rect, bool) {
	for i := range dst {
		dst[i] = 0x5a
	}
	return nil, true
}

// A ScreenCast session through the real frontend streams from the real
// producer into a libpipewire consumer linked to the node Start
// returned, and stays open while PipeWire runs; PipeWire going away
// mid-stream ends the stream, and the frontend tells the app its
// session closed.
func TestFrontendScreenCastStreamsToARealConsumer(t *testing.T) {
	daemon := pwtest.Start(t)
	r := newRig(t, func(b *Backend) {
		b.screenCast = newScreenCast(b.conn, b.sessions, func(sharepicker.Target, bool, uint32) (screenStream, error) {
			return startProducer(producerGeometry{width: 64, height: 32, stride: 256, format: capture.FormatXRGB8888, fps: 30},
				pattern{}, make(chan struct{}), func() {})
		}, (&fakeStarts{}).outputs)
	})
	serveSharePicker(t, r, "/screen:DP-2")
	f := startFrontend(t, r, t.TempDir())

	session := f.session(t, publicScreenCast)
	closed := make(chan *dbus.Signal, 4)
	if err := f.conn.AddMatchSignal(dbus.WithMatchObjectPath(session), dbus.WithMatchMember("Closed")); err != nil {
		t.Fatal(err)
	}
	f.conn.Signal(closed)
	if code, _ := f.request(t, publicScreenCast+".SelectSources", session, Vardict{"types": dbus.MakeVariant(sourceMonitor)}); code != ResponseSuccess {
		t.Fatalf("SelectSources = %d", code)
	}
	code, res := f.request(t, publicScreenCast+".Start", session, "", Vardict{})
	if code != ResponseSuccess {
		t.Fatalf("Start = %d %v", code, res)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Node == 0 {
		t.Fatalf("streams = %+v", streams)
	}

	consumer := pwtest.Consume(t, "lys-shell-screencast")
	for range 3 {
		if size := consumer.Frame(t); size != 256*32 {
			t.Fatalf("a frame of %d bytes, want %d", size, 256*32)
		}
	}
	sessionClosed := func(within time.Duration) bool {
		deadline := time.After(within)
		for {
			select {
			case s := <-closed:
				if s.Path == session && s.Name == "org.freedesktop.portal.Session.Closed" {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	if sessionClosed(100 * time.Millisecond) {
		t.Fatal("the session closed while PipeWire ran")
	}

	daemon.Kill()
	if !sessionClosed(10 * time.Second) {
		t.Fatal("the session outlived PipeWire")
	}
	if _, live := r.backend.sessions.get(session); live {
		t.Error("the backend kept the session")
	}
}
