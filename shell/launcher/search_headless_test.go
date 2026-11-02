package launcher

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"

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

// On a real compositor, keys typed into a real layer surface find a row
// and Enter takes it; from the key to the frame showing its rows is
// measured here, through the socket, the engine and the renderer.
func TestHeadlessKeysFindARowAndEnterTakesIt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
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
		Sheet: apptheme.New(cfg).Sheet,
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
