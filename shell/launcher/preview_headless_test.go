package launcher

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

// previewCode is what the clipboard and the file hold in the shots.
const previewCode = `package main

import "fmt"

// greet says hello to name.
func greet(name string) string {
	return fmt.Sprintf("hello, %s: %d", name, 42)
}

func main() {
	for _, n := range []string{"lys", "shell"} {
		fmt.Println(greet(n))
	}
}
`

// headlessPreview is the surface on a private headless sway with a real
// toplevel to capture, a clipboard history and a file to preview.
type headlessPreview struct {
	a      *app.Application
	s      *Surface
	layers *[]*app.LayerWindow
	kb     *keytest.Keyboard
}

func startHeadlessPreview(t *testing.T) *headlessPreview {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "greet.go"), []byte(previewCode), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	widget.SetFaceResolver(apptheme.FontResolver)
	t.Cleanup(func() { widget.SetFaceResolver(nil) })
	sheet := apptheme.New(cfg).Sheet
	a := apptest.Run(t)
	sockets, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "sway-ipc.*.sock"))
	if len(sockets) != 1 {
		t.Fatalf("sway's IPC sockets: %v", sockets)
	}
	t.Setenv("SWAYSOCK", sockets[0])
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchFiles}
	var layers []*app.LayerWindow
	s := New(Deps{
		Invoke: a.Invoke,
		After: func(d time.Duration, fn func()) func() {
			tm := time.AfterFunc(d, func() { a.Invoke(fn) })
			return func() { tm.Stop() }
		},
		Config: func() *config.Config { return cfg },
		Open: func(c app.LayerConfig) (Window, error) {
			w, err := a.NewLayer(c)
			layers = append(layers, w)
			return w, err
		},
		Font:      font,
		Sheet:     sheet,
		Clipboard: newLauncherClipboard(t, [2]string{"application/x-thing", "12345"}, [2]string{"text/plain", previewCode}),
		Search: func() search.Deps {
			return search.Deps{Env: search.Env{Spawn: func([]string) {}, SpawnShell: func(string) {}, Open: func(string) {}}}
		},
	})
	var editor *widget.Label
	var win *app.Window
	if err := apptest.On(a, func() (err error) {
		editor = widget.NewLabel(font, 22, "notes.md\n\nA window the launcher pictures.", 0)
		win, err = a.NewWindow(app.WindowConfig{Title: "notes.md", AppID: "editor", Width: 640, Height: 400, Root: editor, Background: render.RGB(0xf6, 0xd3, 0x2d)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	apptest.Drawn(t, a, win.HostSurface(), editor.Invalidate)
	return &headlessPreview{a: a, s: s, layers: &layers, kb: keytest.Dial(t, a)}
}

// open starts a session and waits for its surface to take the keys.
func (h *headlessPreview) open(t *testing.T, id uint64, opts launcheripc.SessionOptions) <-chan launcheripc.ServerFrame {
	t.Helper()
	rows := make(chan []string)
	close(rows)
	sess, reply := launcheripc.NewSession(id, opts, false, rows)
	h.s.Open(sess)
	keytest.AwaitFocus(t, h.a)
	return reply
}

// shoot draws the newest surface and, when LYS_SHELL_LAUNCHER_SHOTS names
// a directory, writes the compositor's output there as name.png.
func (h *headlessPreview) shoot(t *testing.T, name string, invalidate func()) {
	t.Helper()
	layer := apptest.On(h.a, func() *app.LayerWindow { return (*h.layers)[len(*h.layers)-1] })
	apptest.Drawn(t, h.a, layer.HostSurface(), invalidate)
	dir := os.Getenv("LYS_SHELL_LAUNCHER_SHOTS")
	if dir == "" {
		return
	}
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	outs := c.Outputs()
	if len(outs) == 0 {
		t.Fatal("the compositor has no output")
	}
	frame, err := c.CaptureOutput(outs[0], capture.Options{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := frame.Image()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// On a real compositor each mode's pane shows its selected row: the
// window mode a capture of a real toplevel with its facts, the clipboard
// mode an entry in full, and the search panel a file.
func TestHeadlessEveryModePreviewsItsSelectedRow(t *testing.T) {
	h := startHeadlessPreview(t)
	a, s := h.a, h.s

	win := "window"
	reply := h.open(t, 1, launcheripc.SessionOptions{Mode: &win})
	apptest.Until(t, a, "the window's picture", func() bool {
		return s.cur != nil && s.cur.views.pane.image.Visible() && s.cur.views.pane.facts.Visible()
	})
	if v := apptest.On(a, func() string { return s.cur.views.pane.rows[0].value.Text() }); v != "notes.md" {
		t.Errorf("the window's title fact is %q", v)
	}
	h.shoot(t, "window-mode", func() { s.cur.views.root.Invalidate() })
	h.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the window session's reply", reply)

	clip := "clipboard"
	reply = h.open(t, 2, launcheripc.SessionOptions{Mode: &clip})
	apptest.Until(t, a, "the clipboard entry", func() bool { return s.cur != nil && s.cur.views.pane.code.Visible() })
	h.shoot(t, "clipboard-mode", func() { s.cur.views.root.Invalidate() })
	h.kb.Tap(keytest.Down)
	apptest.Until(t, a, "the entry's facts", func() bool {
		return s.cur.views.pane.facts.Visible() && !s.cur.views.pane.code.Visible()
	})
	h.shoot(t, "clipboard-mode-other", func() { s.cur.views.root.Invalidate() })
	h.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the clipboard session's reply", reply)

	mode, query := SearchMode, "~/greet"
	reply = h.open(t, 3, launcheripc.SessionOptions{Mode: &mode, Filter: &query})
	apptest.Until(t, a, "the file's text", func() bool { return s.panel != nil && s.panel.v.pane.code.Visible() })
	h.shoot(t, "search-mode", func() { s.panel.v.root.Invalidate() })
	h.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the search session's reply", reply)
}
