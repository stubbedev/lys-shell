package bar

import (
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/layering"
)

// Each location shows its bar on lys-ui's bar window along that edge,
// on the output, in the configured layer and the lys-shell-bar
// namespace, with no layer margins: the insets are the bar's CSS
// margins.
func TestBarConfigForRunsAlongTheLocation(t *testing.T) {
	out := &app.Output{Name: "DP-1"}
	for _, tc := range []struct {
		location config.Location
		edge     widget.BarEdge
	}{
		{config.LocationTop, widget.BarTop},
		{config.LocationBottom, widget.BarBottom},
		{config.LocationLeft, widget.BarLeft},
		{config.LocationRight, widget.BarRight},
	} {
		cfg := config.Defaults()
		cfg.Bar.Location = tc.location
		cfg.Bar.Layer = config.LayerOverlay
		bc := barConfigFor(newTestContext(t, cfg), config.BarLayout{Monitor: "*"}, out)
		if bc.Bar.Edge() != tc.edge {
			t.Errorf("%s: bar edge %v, want %v", tc.location, bc.Bar.Edge(), tc.edge)
		}
		if bc.Output != out || bc.Namespace != "lys-shell-bar-DP-1" || bc.Margin != 0 {
			t.Errorf("%s: output %v namespace %q margin %d, want DP-1, lys-shell-bar-DP-1 and none", tc.location, bc.Output, bc.Namespace, bc.Margin)
		}
		if want := layering.For(cfg.General, config.LayerOverlay); bc.Layer != want {
			t.Errorf("%s: layer %v, want %v", tc.location, bc.Layer, want)
		}
	}
}

// An exclusive bar reserves its thickness; one that is not reserves
// nothing, and windows may run under it.
func TestBarConfigForReservesOnlyWhenExclusive(t *testing.T) {
	cfg := config.Defaults()
	if !barConfigFor(newTestContext(t, cfg), config.BarLayout{Monitor: "*"}, &app.Output{Name: "DP-1"}).Exclusive {
		t.Error("an exclusive bar reserves no zone")
	}
	cfg.Bar.Exclusive = false
	if barConfigFor(newTestContext(t, cfg), config.BarLayout{Monitor: "*"}, &app.Output{Name: "DP-1"}).Exclusive {
		t.Error("a non-exclusive bar reserves a zone")
	}
}

// A location outside the four edges is a broken config invariant and
// panics, rather than docking the bar to some default edge.
func TestBarEdgeRejectsAnUnknownLocation(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("barEdge(sideways) returned an edge")
		}
	}()
	barEdge("sideways")
}

// The zone spaces its items by module-gap and a group its modules by
// the group module gap: box gaps between the items, whatever either
// is set to.
func TestZonesSpaceItemsByTheModuleGap(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.ModuleGap = config.Size{Value: 20, Unit: config.SizePixels}
	cfg.Bar.ButtonGroupModuleGap = config.Size{Value: 3, Unit: config.SizePixels}
	layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{
		{Module: "clock"},
		{Group: &config.BarGroup{Name: "g", Modules: []config.BarItem{{Module: "clock"}, {Module: "clock"}}}},
	}}
	bar := buildRoot(newTestContext(t, cfg), layout, "DP-1")
	settleBar(bar, widget.Size{W: 1600, H: 60})
	items := bar.Start().Children()
	if len(items) != 2 {
		t.Fatalf("start zone holds %d items, want 2", len(items))
	}
	if gap := items[1].(*widget.Box).Bounds().X - right(items[0]); gap != 20 {
		t.Errorf("the items sit %dpx apart, want the 20px module gap", gap)
	}
	modules := items[1].(*widget.Box).Children()
	if gap := modules[1].(*widget.Box).Bounds().X - right(modules[0]); gap != 3 {
		t.Errorf("the grouped modules sit %dpx apart, want the 3px group gap", gap)
	}
}

// right is the right edge of w's bounds.
func right(w widget.Widget) int {
	b := w.(widget.Boundser).Bounds()
	return b.X + b.W
}

// settleBar lays bar out within out until its styles settle, as lys-ui's
// bar window does, and returns its natural size.
func settleBar(bar *widget.Bar, out widget.Size) widget.Size {
	size := widget.SizeWithin(bar, out)
	for range 4 {
		bar.Arrange(render.Rect{W: size.W, H: size.H})
		if !widget.LayoutPending(bar) {
			break
		}
		size = widget.SizeWithin(bar, out)
	}
	return size
}

// barThickness is the bar's natural extent across its edge within out.
func barThickness(bar *widget.Bar, out widget.Size) int {
	size := settleBar(bar, out)
	if bar.Edge().Vertical() {
		return size.W
	}
	return size.H
}
