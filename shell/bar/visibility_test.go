package bar

import (
	"errors"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
)

// fakeLayer is a bar layer whose Rebuild fails with refuse.
type fakeLayer struct {
	closed  bool
	refuse  error
	rebuilt []config.BarLayout
}

func (f *fakeLayer) Close() { f.closed = true }

func (f *fakeLayer) Rebuild(layout config.BarLayout) error {
	if f.refuse != nil {
		return f.refuse
	}
	f.rebuilt = append(f.rebuilt, layout)
	return nil
}

// fakeOpener opens fakeLayers for newBarSet, failing while fail is
// set; layers lists them in opening order.
type fakeOpener struct {
	fail   bool
	layers []*fakeLayer
}

func (o *fakeOpener) open(*app.Output, config.BarLayout) (barLayer, error) {
	if o.fail {
		return nil, errors.New("no surface")
	}
	l := &fakeLayer{}
	o.layers = append(o.layers, l)
	return l, nil
}

var clockLayout = config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}}}

// showClock gives every output the clock layout.
func showClock(string) (config.BarLayout, bool) { return clockLayout, true }

// A reload keeps every open bar's layer: nothing closes or opens, and
// the layer is rebuilt with the new layout.
func TestBarSetReloadRebuildsInPlace(t *testing.T) {
	o := &fakeOpener{}
	set := newBarSet(o.open)
	set.plug(&app.Output{Name: "DP-1"}, config.BarLayout{}, true)
	set.reload(showClock)
	set.reload(showClock)
	if len(o.layers) != 1 || o.layers[0].closed {
		t.Fatalf("reloads opened %d layers, first closed %v", len(o.layers), o.layers[0].closed)
	}
	if got := o.layers[0].rebuilt; len(got) != 2 || !slices.EqualFunc(got[1].Left, clockLayout.Left, func(a, b config.BarItem) bool { return a.Module == b.Module }) {
		t.Fatalf("rebuilt with %+v, want the clock layout twice", got)
	}
}

// A surface that refuses the rebuild is closed and opened afresh; one
// that then cannot open leaves the output without a bar until the next
// apply retries it.
func TestBarSetReloadReopensARefusingSurface(t *testing.T) {
	o := &fakeOpener{}
	set := newBarSet(o.open)
	set.plug(&app.Output{Name: "DP-1"}, config.BarLayout{}, true)
	o.layers[0].refuse = errors.New("gone")
	set.reload(showClock)
	if len(o.layers) != 2 || !o.layers[0].closed || set.bars["DP-1"].layer != o.layers[1] {
		t.Fatalf("a refused rebuild: opened %d, old closed %v", len(o.layers), o.layers[0].closed)
	}
	o.layers[1].refuse = errors.New("gone")
	o.fail = true
	set.reload(showClock)
	if !o.layers[1].closed || set.bars["DP-1"].layer != nil {
		t.Fatalf("a failed reopen: old closed %v, layer %v", o.layers[1].closed, set.bars["DP-1"].layer)
	}
	o.fail = false
	set.apply(nil)
	if len(o.layers) != 3 || o.layers[2].closed {
		t.Fatalf("retry: opened %d", len(o.layers))
	}
}

// A hidden bar or one the new layout drops is not rebuilt.
func TestBarSetReloadLeavesHiddenAndDroppedBars(t *testing.T) {
	o := &fakeOpener{}
	set := newBarSet(o.open)
	set.plug(&app.Output{Name: "DP-1"}, config.BarLayout{}, true)
	set.plug(&app.Output{Name: "DP-2"}, config.BarLayout{}, true)
	set.apply(map[string]bool{"DP-2": true})
	set.reload(func(name string) (config.BarLayout, bool) { return clockLayout, name != "DP-1" })
	if !o.layers[0].closed || len(o.layers[0].rebuilt) != 0 {
		t.Fatalf("the dropped DP-1 bar: closed %v, rebuilt %d", o.layers[0].closed, len(o.layers[0].rebuilt))
	}
	if len(o.layers) != 2 || set.bars["DP-2"].layer != nil {
		t.Fatalf("the hidden DP-2 bar reopened: layers %d", len(o.layers))
	}
}

func TestBarSetClosesHiddenAndReopensShown(t *testing.T) {
	opened := map[string]int{}
	layers := map[string]*fakeLayer{}
	var failOpen bool
	set := newBarSet(func(o *app.Output, _ config.BarLayout) (barLayer, error) {
		if failOpen {
			return nil, errors.New("no surface")
		}
		opened[o.Name]++
		layers[o.Name] = &fakeLayer{}
		return layers[o.Name], nil
	})
	set.plug(&app.Output{Name: "DP-1"}, config.BarLayout{}, true)
	set.plug(&app.Output{Name: "DP-2"}, config.BarLayout{}, true)
	dp1, dp2 := layers["DP-1"], layers["DP-2"]
	clear(opened)
	if got := set.connectors(); len(got) != 2 || got[0] != "DP-1" || got[1] != "DP-2" {
		t.Fatalf("connectors = %v", got)
	}

	set.apply(map[string]bool{"DP-1": true})
	if !dp1.closed || dp2.closed || len(opened) != 0 {
		t.Fatalf("hide DP-1: dp1 closed %v, dp2 closed %v, opened %v", dp1.closed, dp2.closed, opened)
	}
	set.apply(map[string]bool{"DP-1": true}) // already hidden: nothing happens
	set.apply(map[string]bool{})
	if opened["DP-1"] != 1 || opened["DP-2"] != 0 {
		t.Fatalf("show all: opened %v", opened)
	}

	// A bar that cannot reopen stays hidden and is retried next time.
	set.apply(map[string]bool{"DP-2": true})
	failOpen = true
	set.apply(map[string]bool{})
	failOpen = false
	set.apply(map[string]bool{})
	if opened["DP-2"] != 1 {
		t.Fatalf("retry reopen: opened %v", opened)
	}
}

// A hotplugged output gets its bar when its layout shows one; an
// unplugged one loses it and leaves the set; a known output plugged
// again is not reopened.
func TestBarSetFollowsHotplug(t *testing.T) {
	var layers []*fakeLayer
	set := newBarSet(func(*app.Output, config.BarLayout) (barLayer, error) {
		l := &fakeLayer{}
		layers = append(layers, l)
		return l, nil
	})
	dp1 := &app.Output{Name: "DP-1"}
	set.plug(dp1, config.BarLayout{}, true)
	if len(layers) != 1 || !slices.Equal(set.connectors(), []string{"DP-1"}) {
		t.Fatalf("plug DP-1: layers %d connectors %v", len(layers), set.connectors())
	}
	set.plug(dp1, config.BarLayout{}, true)
	if len(layers) != 1 {
		t.Fatal("a known output was opened twice")
	}
	// A layout without a bar registers the output but opens nothing.
	hdmi := &app.Output{Name: "HDMI-A-1"}
	set.plug(hdmi, config.BarLayout{}, false)
	if len(layers) != 1 || len(set.order) != 2 {
		t.Fatalf("plug hidden HDMI: layers %d order %v", len(layers), set.order)
	}

	set.unplug(dp1)
	if !layers[0].closed || len(set.connectors()) != 0 || len(set.order) != 1 {
		t.Fatalf("unplug DP-1: closed %v connectors %v order %v", layers[0].closed, set.connectors(), set.order)
	}
	// An output that is not registered unplugs as a no-op.
	set.unplug(&app.Output{Name: "DP-9"})
	if len(set.order) != 1 {
		t.Fatalf("unknown unplug changed the set: %v", set.order)
	}

	// A renamed output moves to its new name.
	hdmi.Name = "HDMI-A-2"
	set.plug(hdmi, config.BarLayout{}, true)
	if !slices.Equal(set.order, []string{"HDMI-A-2"}) || len(layers) != 2 {
		t.Fatalf("rename: order %v layers %d", set.order, len(layers))
	}
}
