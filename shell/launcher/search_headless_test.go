package launcher

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/anim"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/shell/apptheme"
)

type headlessWindows struct{ focused chan string }

func (w headlessWindows) List(context.Context) ([]modes.WindowInfo, error) {
	return []modes.WindowInfo{{ID: "7", Title: "Hello World", Class: "writer", Workspace: "2"}}, nil
}

func (w headlessWindows) Focus(_ context.Context, id string) error {
	w.focused <- id
	return nil
}
func (headlessWindows) Close(context.Context, string) error { return nil }

type headlessPanel struct {
	a       *app.Application
	s       *Surface
	layer   *app.LayerWindow
	kb      *keytest.Keyboard
	focused chan string
	reply   <-chan launcheripc.ServerFrame
}

// startHeadlessPanel opens the panel on a private headless sway.
func startHeadlessPanel(t *testing.T) *headlessPanel {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	// The theme installs before the loop runs, as the shell does it: the
	// toolkit's theme belongs to the loop once there is one.
	sheet := apptheme.New(cfg).Sheet
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Launcher.Search.Providers = []config.LauncherSearchProvider{config.SearchWindows, config.SearchCalculator, config.SearchWeb}
	focused := make(chan string, 1)
	var layer *app.LayerWindow
	s := New(Deps{
		Invoke: a.Invoke,
		After: func(d time.Duration, fn func()) func() {
			tm := time.AfterFunc(d, func() { a.Invoke(fn) })
			return func() { tm.Stop() }
		},
		Config: func() *config.Config { return cfg },
		Open: func(c app.LayerConfig) (Window, error) {
			w, err := a.NewLayer(c)
			layer = w
			return w, err
		},
		Font:  font,
		Sheet: sheet,
		Search: func() search.Deps {
			return search.Deps{Env: search.Env{Spawn: func([]string) {}, SpawnShell: func(string) {}}, Windows: headlessWindows{focused: focused}}
		},
	})
	mode := SearchMode
	rows := make(chan []string)
	close(rows)
	sess, reply := launcheripc.NewSession(1, launcheripc.SessionOptions{Mode: &mode}, false, rows)
	s.Open(sess)
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	apptest.Until(t, a, "the window row to load", func() bool {
		p := s.panel
		return p != nil && p.eng != nil
	})
	return &headlessPanel{a: a, s: s, layer: layer, kb: kb, focused: focused, reply: reply}
}

// On a real compositor, keys typed into a real layer surface find a row
// and Enter takes it; from the key to the frame showing its rows is
// measured here, through the socket, the engine and the renderer.
func TestHeadlessKeysFindARowAndEnterTakesIt(t *testing.T) {
	h := startHeadlessPanel(t)
	a, s, layer, kb, focused, reply := h.a, h.s, h.layer, h.kb, h.focused, h.reply

	var latencies []time.Duration
	want := ""
	for _, k := range []struct {
		code uint32
		text string
	}{{keytest.H, "h"}, {keytest.L, "l"}, {keytest.L, "l"}} {
		start := time.Now()
		kb.Tap(k.code)
		tapped := time.Since(start)
		want += k.text
		apptest.Until(t, a, "the rows for "+want, func() bool { return s.panel.frame.Query == want })
		answered := time.Since(start)
		apptest.Drawn(t, a, layer.HostSurface(), func() { s.panel.v.root.Invalidate() })
		latencies = append(latencies, time.Since(start))
		t.Logf("%q: tap returned %v, rows %v, frame %v", want, tapped, answered, time.Since(start))
	}
	slices.Sort(latencies)
	t.Logf("key to the compositor taking its frame on headless sway with pixman: %v", latencies)
	apptest.Until(t, a, "the window row", func() bool {
		r, ok := s.panel.selected()
		return ok && strings.Contains(r.Entry.Title, "Hello")
	})
	kb.Tap(keytest.Tab)
	apptest.Until(t, a, "the actions panel", func() bool { return s.panel.depth == 1 })
	kb.Tap(keytest.Escape)
	apptest.Until(t, a, "back at the list", func() bool { return s.panel.depth == 0 && s.panel.v.entry.Text() == "hll" })
	kb.Tap(keytest.Return)
	got := apptest.Answer(t, "the window's focus", focused)
	if got != "7" {
		t.Errorf("focused %q", got)
	}
	if f := apptest.Answer(t, "the session's reply", reply); f.Type != launcheripc.FrameResult {
		t.Errorf("reply %+v", f)
	}
}

// The desktop's preference, read the way any lys-ui application reads it
// (gtk-enable-animations in settings.ini), reaches the panel on a real
// compositor: it fades, it does not move, and its height lands at once.
func TestHeadlessDesktopReducedMotionReachesThePanel(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gtk-4.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gtk-4.0", "settings.ini"), []byte("[Settings]\ngtk-enable-animations=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Cleanup(func() { widget.SetReducedMotion(false) })
	h := startHeadlessPanel(t)
	a, s := h.a, h.s
	if !anim.Reduced() {
		t.Fatal("the application did not take the preference from settings.ini")
	}
	// The panel and its motion belong to the loop: everything about them
	// is read there.
	p := apptest.On(a, func() *panel { return s.panel })
	m := apptest.On(a, func() *panelMotion { return p.v.motion })
	apptest.Until(t, a, "the fade's end", func() bool { return m.reveal == 1 })
	if style := apptest.On(a, func() panelStyle { return m.style }); style.grow || style.shift != 0 {
		t.Errorf("style %+v: nothing may move", style)
	}
	rest := apptest.On(a, func() bool { return p.v.box.Bounds() == m.restRect(p.v.root.Bounds()) })
	if !rest {
		t.Error("the panel is not where it rests")
	}
	h.kb.Tap(keytest.H)
	apptest.Until(t, a, "the rows", func() bool { return p.frame.Query == "h" })
	settled := apptest.On(a, func() bool { return int(m.h) == m.targetH })
	if !settled {
		t.Error("the height must land at once under reduced motion")
	}
	h.kb.Tap(keytest.Escape)
	apptest.Answer(t, "the session's reply", h.reply)
}
