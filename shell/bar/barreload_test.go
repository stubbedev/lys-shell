package bar

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/swaytest"
)

// Snapshots published before the loop runs apply once, from the first
// old snapshot to the latest; one published after that apply queues a
// new one.
func TestReloadQueueCoalescesABurst(t *testing.T) {
	var queued []func()
	type change struct{ old, next *config.Config }
	var applied []change
	q := newReloadQueue(func(fn func()) { queued = append(queued, fn) }, func(old, next *config.Config) {
		applied = append(applied, change{old, next})
	})
	snaps := make([]*config.Config, 6)
	for i := range snaps {
		snaps[i] = config.Defaults()
	}
	for i := 1; i < len(snaps)-1; i++ {
		q.push(snaps[i-1], snaps[i])
	}
	if len(queued) != 1 {
		t.Fatalf("a burst of %d writes queued %d applies, want 1", len(snaps)-2, len(queued))
	}
	queued[0]()
	if len(applied) != 1 || applied[0].old != snaps[0] || applied[0].next != snaps[len(snaps)-2] {
		t.Fatalf("applied %d changes, want one from the first old to the latest", len(applied))
	}
	q.push(snaps[len(snaps)-2], snaps[len(snaps)-1])
	if len(queued) != 2 {
		t.Fatalf("a write after the apply queued %d applies in all, want 2", len(queued))
	}
	queued[1]()
	if len(applied) != 2 || applied[1].next != snaps[len(snaps)-1] {
		t.Fatalf("the later write did not apply on its own: %d applies", len(applied))
	}
}

// fillBar refills a built bar in place: the same root takes the new
// floating class, thickness and modules, and keeps none of the old
// ones; a config without an inset drops the class again.
func TestFillBarRefillsTheSameRoot(t *testing.T) {
	cfg := config.Defaults()
	out := widget.Size{W: 1600, H: 400}
	bar := buildRoot(styledContext(t, cfg), clockLayout, "DP-1")
	thick := barThickness(bar, out)
	oldItem := bar.Start().Children()[0]

	next := config.Defaults()
	next.Bar.Padding = config.Px(30)
	next.Bar.InsetEdge = config.Px(8)
	layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}, {Module: "clock"}}}
	fillBar(bar, styledContext(t, next), layout)
	if !widget.HasClass(bar, "floating") || !widget.HasClass(bar, "DP-1") || !widget.HasClass(bar, "top") {
		t.Errorf("refilled classes %v, want floating beside DP-1 and top", bar.Classes())
	}
	if got := barThickness(bar, out); got <= thick {
		t.Errorf("thickness %d after a larger padding, want more than %d", got, thick)
	}
	if len(bar.Start().Children()) != 0 || len(bar.Center().Children()) != 2 {
		t.Errorf("zones hold %d start and %d center items, want 0 and 2", len(bar.Start().Children()), len(bar.Center().Children()))
	}
	if oldItem.(interface{ Parent() widget.Widget }).Parent() != nil {
		t.Error("the old item is still parented to the bar")
	}

	fillBar(bar, styledContext(t, config.Defaults()), clockLayout)
	if widget.HasClass(bar, "floating") {
		t.Error("a bar without insets stayed floating")
	}
}

// Against a real compositor, every reload rebuilds the bar on the
// surface it opened with, which never closes: a larger padding and a
// second module grow it, and turning exclusive off and on drops the
// workspace's reserved edge to 0 and back to the bar's thickness.
func TestBarReloadKeepsTheSurface(t *testing.T) {
	swaytest.Start(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	application := app.NewApplication(sess)
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.App = application
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	rt.ctx.Dropdowns = newDropdownRegistry(application, cfg, rt.font, &rt.style, rt.ctx)
	host := &barHost{app: application, rt: rt}

	var opened []*barWindow
	closes := 0
	set := newBarSet(func(o *app.Output, l config.BarLayout) (barLayer, error) {
		layer, err := host.open(o, l)
		if err != nil {
			return nil, err
		}
		w := layer.(*barWindow)
		opened = append(opened, w)
		return &probeLayer{barWindow: w, onClose: func() { closes++ }}, nil
	})
	outputs := sess.Outputs()
	if len(outputs) == 0 || outputs[0].Name == "" {
		t.Fatalf("the headless sway shows outputs %v", outputs)
	}
	set.plug(outputs[0], clockLayout, true)
	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	defer func() {
		application.Invoke(application.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
	}()
	onLoop := func(fn func()) {
		ran := make(chan struct{})
		application.Invoke(func() {
			fn()
			close(ran)
		})
		<-ran
	}
	bar := opened[0]
	thickness := func() int {
		var h int
		onLoop(func() { _, h = bar.layer.Size() })
		return h
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (thickness %d, reserved %d)", what, thickness(), reservedTop(t))
			}
		}
	}
	reload := func(next *config.Config, layout config.BarLayout) {
		onLoop(func() {
			rt.mount(application, next)
			set.reload(func(string) (config.BarLayout, bool) { return layout, true })
		})
	}
	wait("the exclusive bar's zone", func() bool { h := thickness(); return h > 0 && reservedTop(t) == h })
	before := thickness()

	taller := config.Defaults()
	taller.Bar.Padding = config.Px(30)
	twoClocks := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "clock"}}}
	reload(taller, twoClocks)
	wait("the taller bar's zone", func() bool { h := thickness(); return h > before && reservedTop(t) == h })
	onLoop(func() {
		if n := len(bar.bar.Start().Children()); n != 2 {
			t.Errorf("the rebuilt bar holds %d items, want 2", n)
		}
	})

	shared := config.Defaults()
	shared.Bar.Padding = config.Px(30)
	shared.Bar.Exclusive = false
	reload(shared, twoClocks)
	wait("the zone to drop", func() bool { return reservedTop(t) == 0 })

	reload(taller, twoClocks)
	wait("the zone to return", func() bool { h := thickness(); return h > before && reservedTop(t) == h })

	onLoop(func() {
		if len(opened) != 1 || closes != 0 || bar.layer.Closed() {
			t.Errorf("the reloads opened %d surfaces and closed %d, want 1 and none", len(opened), closes)
		}
	})
}

// reservedTop is how far the focused workspace of the test's sway
// starts below its output's top: the exclusive zone of a top bar.
func reservedTop(t *testing.T) int {
	t.Helper()
	var workspaces []struct {
		Focused bool `json:"focused"`
		Rect    struct {
			Y int `json:"y"`
		} `json:"rect"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_workspaces"), &workspaces); err != nil {
		t.Fatal(err)
	}
	for _, ws := range workspaces {
		if ws.Focused {
			return ws.Rect.Y
		}
	}
	t.Fatal("sway has no focused workspace")
	return 0
}

// probeLayer reports a bar layer's Close before it runs.
type probeLayer struct {
	*barWindow
	onClose func()
}

func (p *probeLayer) Close() {
	p.onClose()
	p.barWindow.Close()
}
