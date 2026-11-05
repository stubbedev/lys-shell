package launcher

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

func TestTheNextPanelReusesTheViewsClearedOfTheLastOne(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.typed(p, "alpha")
	h.waitTitles(p, "Alpha Docs", "Alpha Issues", webRow("alpha"))
	h.key("Escape")
	h.until("closed", func() bool { return h.s.panel == nil })

	next, _ := h.openPanel()
	if next.v != p.v {
		t.Fatal("the second panel built its views again")
	}
	if next.v.p != next {
		t.Error("the views still answer to the first panel")
	}
	if got := next.v.entry.Text(); got != "" {
		t.Errorf("field = %q, want it empty", got)
	}
	h.typed(next, "issues")
	h.waitTitles(next, "Alpha Issues", webRow("issues"))
	h.key("Return")
	h.until("the row ran", func() bool { return len(h.spawned) == 1 })
}

func TestAChangedConfigOrAPanelStillLeavingBuildsNewViews(t *testing.T) {
	h := newPanelHarness(t)
	p, _ := h.openPanel()
	h.key("Escape")
	h.until("closed", func() bool { return h.s.panel == nil })
	cfg := *h.cfg
	h.cfg = &cfg
	next, _ := h.openPanel()
	if next.v == p.v {
		t.Error("a new config must build new views")
	}

	h = newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ = h.openPanel()
	h.key("Escape")
	h.until("the session ended", func() bool { return h.s.panel == nil })
	if h.harness.windows[0].closed {
		t.Fatal("the exit must still be playing")
	}
	next, _ = h.openPanel()
	if next.v == p.v {
		t.Error("views still leaving the screen were taken by the next panel")
	}
}

func TestTheFrecencyStoreOpensOnceAcrossPanels(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Launcher.History.Enable = true
	opens := 0
	path := t.TempDir() + "/f.json"
	h.s.d.OpenFrecency = func() (*search.Frecency, error) {
		h.loop.invoke(func() { opens++ })
		return search.OpenFrecency(path, time.Now)
	}
	for range 2 {
		p, _ := h.openPanel()
		h.typed(p, "alpha")
		h.key("Escape")
		h.until("closed", func() bool { return h.s.panel == nil })
	}
	h.loop.pump()
	if opens != 1 {
		t.Errorf("opens = %d, want 1", opens)
	}
}

func TestKeptSourcesFollowTheSurfacesWatch(t *testing.T) {
	h := newPanelHarness(t)
	var watched [][]string
	h.s = New(Deps{
		Invoke: h.loop.invoke, After: h.s.d.After, Config: h.s.d.Config, Open: h.s.d.Open, Font: h.s.d.Font,
		Search: h.s.d.Search, MonitorWidth: h.s.d.MonitorWidth,
		WatchFiles: func(paths []string, _ bool, _ func()) (func(), error) {
			watched = append(watched, paths)
			return func() {}, nil
		},
	})
	h.cfg.Launcher.Search.Providers = append(h.cfg.Launcher.Search.Providers, config.SearchScripts)
	for range 2 {
		h.openPanel()
		h.key("Escape")
		h.until("closed", func() bool { return h.s.panel == nil })
	}
	if len(watched) != 1 {
		t.Errorf("watches = %q, want the scripts' directory once", watched)
	}
}
