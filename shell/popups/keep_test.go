package popups

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-ui/app"
)

type fakeWindow struct{ closed bool }

func (w *fakeWindow) Close() { w.closed = true }

// mappedPopups maps its surfaces through a fake, recording each.
func mappedPopups(t *testing.T) (*Popups, func() []app.LayerConfig, func() []*fakeWindow) {
	t.Helper()
	p, _ := newTestPopups(t, config.DefaultsNotification())
	p.cfg.Animations.Enabled = false
	p.outputs = func() []*app.Output { return []*app.Output{{Name: "DP-1"}} }
	var opened []app.LayerConfig
	var wins []*fakeWindow
	p.open = func(c app.LayerConfig) (window, error) {
		w := &fakeWindow{}
		opened, wins = append(opened, c), append(wins, w)
		return w, nil
	}
	return p, func() []app.LayerConfig { return opened }, func() []*fakeWindow { return wins }
}

func TestAPopupAfterTheLastWentMapsOverTheKeptTree(t *testing.T) {
	p, opened, wins := mappedPopups(t)
	id := p.svc.Notify("app", 0, "", "One", "", nil, 0)
	p.sync()
	st := p.stacks[0]
	p.svc.DismissPopup(id)
	p.sync()
	if len(wins()) != 1 || !wins()[0].closed {
		t.Fatal("the surface must close with the last card")
	}
	if n := len(p.list.Children()); n != 0 {
		t.Errorf("the kept list holds %d stacks, want none", n)
	}
	p.svc.Notify("other", 0, "", "Two", "", nil, 0)
	p.sync()
	if len(opened()) != 2 {
		t.Fatalf("%d surfaces, want a new one for the new card", len(opened()))
	}
	if opened()[1].Root != opened()[0].Root {
		t.Error("the new surface built its tree again")
	}
	if len(p.stacks) != 1 || p.stacks[0] != st {
		t.Error("the new stack was built again rather than taken from the idle ones")
	}
	if p.stacks[0].key != "other" || len(p.stacks[0].cards) != 1 {
		t.Errorf("the reused stack is %q with %d cards, want the new app's one", p.stacks[0].key, len(p.stacks[0].cards))
	}
	if n := len(p.list.Children()); n != 1 {
		t.Errorf("the list shows %d stacks, want 1", n)
	}
}

func TestAReloadDropsTheKeptTree(t *testing.T) {
	p, opened, _ := mappedPopups(t)
	id := p.svc.Notify("app", 0, "", "One", "", nil, 0)
	p.sync()
	p.svc.DismissPopup(id)
	p.sync()
	next := config.Defaults()
	next.Animations.Enabled = false
	p.SetConfig(next)
	p.svc.Notify("app", 0, "", "Two", "", nil, 0)
	p.sync()
	if len(opened()) != 2 || opened()[1].Root == opened()[0].Root {
		t.Error("a tree built for the old config showed under the new one")
	}
	if len(p.idle) != 0 {
		t.Error("idle stacks of the old config were kept")
	}
}

func TestADetachedOutputClosesTheOldSurfaceBeforeTheTreeMovesOn(t *testing.T) {
	p, opened, wins := mappedPopups(t)
	p.svc.Notify("app", 0, "", "One", "", nil, 0)
	p.sync()
	outs := []*app.Output{{Name: "HDMI-A-1"}}
	p.outputs = func() []*app.Output { return outs }
	var closedFirst bool
	p.open = func(c app.LayerConfig) (window, error) {
		closedFirst = wins()[0].closed
		return &fakeWindow{}, nil
	}
	p.DetachOutput("DP-1")
	if !closedFirst {
		t.Error("the tree mapped on a new surface while the old one still showed it")
	}
	if len(opened()) != 1 {
		t.Errorf("opened %d", len(opened()))
	}
}
