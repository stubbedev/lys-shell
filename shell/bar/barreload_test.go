package bar

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/swaytest"
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

// Against a real compositor: a reload that changes the bar's content
// and size rebuilds it on the same surface, which never closes and
// grows to the new thickness; one that changes whether the bar is
// exclusive opens the new surface and closes the old one only once the
// new one is configured and drawn.
func TestBarReloadKeepsOrReplacesTheSurface(t *testing.T) {
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
	var replacementUp []bool
	set := newBarSet(func(o *app.Output, l config.BarLayout) (barLayer, error) {
		layer, err := host.open(o, l)
		if err != nil {
			return nil, err
		}
		w := layer.(*barWindow)
		opened = append(opened, w)
		return &probeLayer{barWindow: w, onClose: func() {
			last := opened[len(opened)-1]
			replacementUp = append(replacementUp, last != w && last.layer.EnsureUsable() == nil && !last.layer.Closed())
		}}, nil
	})
	outputs := sess.Outputs()
	if len(outputs) == 0 || outputs[0].Name == "" {
		t.Fatalf("the headless sway shows outputs %v", outputs)
	}
	output := outputs[0]
	set.plug(output, clockLayout, true)
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
	waitLoop := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			var ok bool
			onLoop(func() { ok = cond() })
			if ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	first := opened[0]
	thickness := func(w *barWindow) int { _, h := w.layer.Size(); return h }
	waitLoop("the bar's first configure", func() bool { return first.layer.EnsureUsable() == nil && thickness(first) > 0 })
	var before int
	onLoop(func() { before = thickness(first) })

	taller := config.Defaults()
	taller.Bar.Padding = config.Px(30)
	twoClocks := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "clock"}}}
	onLoop(func() {
		rt.mount(application, taller)
		set.reload(func(string) (config.BarLayout, bool) { return twoClocks, true })
	})
	waitLoop("the rebuilt bar's new thickness", func() bool { return thickness(first) > before })
	onLoop(func() {
		if len(opened) != 1 || first.layer.Closed() || len(replacementUp) != 0 {
			t.Errorf("an in-place reload opened %d surfaces, closed the first %v, closed %d", len(opened), first.layer.Closed(), len(replacementUp))
		}
		if n := len(first.bar.Start().Children()); n != 2 {
			t.Errorf("the rebuilt bar holds %d items, want 2", n)
		}
	})

	shared := config.Defaults()
	shared.Bar.Padding = config.Px(30)
	shared.Bar.Exclusive = false
	onLoop(func() {
		rt.mount(application, shared)
		set.reload(func(string) (config.BarLayout, bool) { return twoClocks, true })
		if len(opened) != 2 || first.layer.Closed() {
			t.Errorf("an exclusive change opened %d surfaces in all and closed the old one at once: %v", len(opened), first.layer.Closed())
		}
	})
	waitLoop("the old surface to close", func() bool { return first.layer.Closed() })
	onLoop(func() {
		if len(replacementUp) != 1 || !replacementUp[0] {
			t.Errorf("the old surface closed with the replacement up: %v, want one close after it drew", replacementUp)
		}
		if set.bars[output.Name].layer.(*probeLayer).barWindow != opened[1] {
			t.Error("the set does not hold the replacement")
		}
	})
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
