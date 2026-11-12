package bar

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-ui/app"
)

type heldWallpapers struct {
	pending   map[*app.Output]func()
	cancelled map[*app.Output]int
}

func newHeldWallpapers() *heldWallpapers {
	return &heldWallpapers{pending: map[*app.Output]func(){}, cancelled: map[*app.Output]int{}}
}

func (h *heldWallpapers) await(out *app.Output, done func()) func() {
	h.pending[out] = done
	return func() {
		delete(h.pending, out)
		h.cancelled[out]++
	}
}

func (h *heldWallpapers) present(t *testing.T, out *app.Output) {
	t.Helper()
	done, ok := h.pending[out]
	if !ok {
		t.Fatalf("nothing waits for %s's wallpaper", out.Name)
	}
	delete(h.pending, out)
	done()
}

func TestSurfaceOpensOnlyOnceItsOutputsWallpaperPresented(t *testing.T) {
	walls := newHeldWallpapers()
	o := &fakeOpener{}
	set := newSurfaceSet(o.open, walls.await)
	dp1, dp2 := &app.Output{Name: "DP-1"}, &app.Output{Name: "DP-2"}
	set.plug(dp1, clockLayout, true)
	set.plug(dp2, clockLayout, true)
	if len(o.layers) != 0 {
		t.Fatalf("%d surfaces opened before any wallpaper presented", len(o.layers))
	}
	if got := set.connectors(); len(got) != 2 {
		t.Errorf("connectors = %v while the wallpapers decode, want both outputs", got)
	}
	walls.present(t, dp2)
	if len(o.layers) != 1 || set.bars["DP-2"].layer == nil || set.bars["DP-1"].layer != nil {
		t.Fatalf("DP-2's wallpaper presented: opened %d, DP-1 open %v", len(o.layers), set.bars["DP-1"].layer != nil)
	}
	walls.present(t, dp1)
	if len(o.layers) != 2 || set.bars["DP-1"].layer == nil {
		t.Fatalf("both wallpapers presented: opened %d", len(o.layers))
	}
	set.apply(map[string]bool{"DP-1": true})
	set.apply(nil)
	if len(walls.pending) != 0 || len(o.layers) != 3 {
		t.Errorf("hide and show waited again (%d waits) or opened %d surfaces, want 3", len(walls.pending), len(o.layers))
	}
}

func TestSurfaceHiddenOrUnshownWhileWaitingStaysClosed(t *testing.T) {
	walls := newHeldWallpapers()
	o := &fakeOpener{}
	set := newSurfaceSet(o.open, walls.await)
	dp1, dp2 := &app.Output{Name: "DP-1"}, &app.Output{Name: "DP-2"}
	set.plug(dp1, clockLayout, true)
	set.plug(dp2, config.BarLayout{}, false)
	set.apply(map[string]bool{"DP-1": true})
	walls.present(t, dp1)
	walls.present(t, dp2)
	if len(o.layers) != 0 {
		t.Fatalf("%d surfaces opened on a hidden bar and an output without one", len(o.layers))
	}
	set.apply(nil)
	if len(o.layers) != 1 || set.bars["DP-1"].layer == nil {
		t.Fatalf("showing the ready bar opened %d surfaces", len(o.layers))
	}
}

func TestUnpluggedOutputStopsWaitingForItsWallpaper(t *testing.T) {
	walls := newHeldWallpapers()
	o := &fakeOpener{}
	set := newSurfaceSet(o.open, walls.await)
	dp1 := &app.Output{Name: "DP-1"}
	set.plug(dp1, clockLayout, true)
	set.unplug(dp1)
	if walls.cancelled[dp1] != 1 || len(walls.pending) != 0 {
		t.Fatalf("the unplug cancelled the wait %d times, want 1", walls.cancelled[dp1])
	}
	if len(o.layers) != 0 {
		t.Errorf("the unplugged output opened %d surfaces", len(o.layers))
	}
}
