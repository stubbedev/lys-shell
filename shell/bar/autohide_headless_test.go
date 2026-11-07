package bar

import (
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/swaytest"
)

// On a real compositor an always-hiding bar collapses to its strip after
// the delay, reveals when the pointer reaches the edge, stays while a
// dropdown opened from it is open, hides once it closes, and peeks for
// `panel reveal`; through all of it the workspace never loses the bar's
// edge, so no window is configured for it.
func TestHeadlessAutoHidingBarRevealsAtTheEdge(t *testing.T) {
	swaytest.Start(t)
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	application := app.NewApplication(sess)
	cfg := config.Defaults()
	cfg.Bar.AutoHide = config.AutoHideAlways
	cfg.Bar.AutoHideDelayMs = 50
	ctx := styledContext(t, cfg)
	ctx.App = application
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	rt.ctx.Dropdowns = newDropdownRegistry(application, cfg, rt.font, &rt.style, rt.ctx)
	host := &barHost{app: application, rt: rt}
	var bar *barWindow
	set := newSurfaceSet(func(o *app.Output, l config.BarLayout) (surfaceLayer[config.BarLayout], error) {
		layer, err := host.open(o, l)
		if err == nil {
			bar = layer.(*barWindow)
		}
		return layer, err
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
		if top := reservedTop(t); top != 0 {
			t.Fatalf("%s: the workspace starts %dpx down; the bar must reserve nothing", what, top)
		}
	}
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pointer.Close()
	cursor := func(x, y int) {
		t.Helper()
		w, h := pointer.OutputSize()
		if err := pointer.MotionAbsolute(intconv.Clamp[uint32](x), intconv.Clamp[uint32](y), intconv.Clamp[uint32](w), intconv.Clamp[uint32](h)); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	cursor(400, 400)
	wait("the bar to hide", func() bool { return thickness() == revealPx })
	cursor(50, 40)
	cursor(50, 1)
	wait("the pointer at the edge to reveal it", func() bool { return thickness() > revealPx })
	shown := thickness()

	onLoop(func() {
		anchor := bar.bar.Start().Children()[0]
		if err := rt.ctx.Dropdowns.open(output.Name, "calendar", anchor); err != nil {
			t.Errorf("open the calendar: %v", err)
		}
	})
	cursor(300, 500)
	time.Sleep(300 * time.Millisecond)
	if got := thickness(); got != shown {
		t.Fatalf("the bar is %d thick under an open dropdown, want %d", got, shown)
	}
	onLoop(rt.ctx.Dropdowns.closeAll)
	wait("the bar to hide once the dropdown closed", func() bool { return thickness() == revealPx })

	onLoop(func() { set.peek("", 200*time.Millisecond) })
	wait("panel reveal to show it", func() bool { return thickness() > revealPx })
	wait("the peek to end", func() bool { return thickness() == revealPx })
}
