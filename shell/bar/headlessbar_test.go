package bar

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// headlessBar is one bar the shell opens on the first output of a
// headless sway, its loop running until the test ends.
type headlessBar struct {
	t   *testing.T
	app *app.Application
	// holdsKeyboard reports, on the loop, whether the compositor
	// focuses the bar's surface.
	holdsKeyboard func() bool
	rt            *barRuntime
	set           *surfaceSet[config.BarLayout]
	output        *app.Output
	bar           *barWindow
}

// openHeadlessBar opens layout's bar with cfg on the compositor the
// test started, as RunWith wires it, and runs the loop.
func openHeadlessBar(t *testing.T, cfg *config.Config, layout config.BarLayout) *headlessBar {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	application := app.NewApplication(sess)
	ctx := styledContext(t, cfg)
	ctx.App = application
	ctx.Polls = testPollHub(t)
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	rt.ctx.Dropdowns = newDropdownRegistry(application, cfg, rt.font, &rt.style, rt.ctx)
	host := &barHost{app: application, rt: rt}
	h := &headlessBar{t: t, app: application, rt: rt}
	h.holdsKeyboard = func() bool { return sess.KeyboardFocus() == h.bar.layer.HostSurface() }
	h.set = newSurfaceSet(func(o *app.Output, l config.BarLayout) (surfaceLayer[config.BarLayout], error) {
		layer, err := host.open(o, l)
		if err == nil {
			h.bar = layer.(*barWindow)
		}
		return layer, err
	}, noWallpaper)
	outputs := sess.Outputs()
	if len(outputs) == 0 || outputs[0].Name == "" {
		t.Fatalf("the headless sway shows outputs %v", outputs)
	}
	h.output = outputs[0]
	h.set.plug(h.output, layout, true)
	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	t.Cleanup(func() {
		h.onLoop(func() {
			h.set.unplug(h.output)
			rt.ctx.Dropdowns.release()
		})
		application.Invoke(application.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
		widget.WaitAssetLoads()
	})
	return h
}

// onLoop runs fn on the bar's loop and waits for it.
func (h *headlessBar) onLoop(fn func()) {
	ran := make(chan struct{})
	h.app.Invoke(func() {
		fn()
		close(ran)
	})
	<-ran
}

// wait polls cond for up to ten seconds and fails with its last error.
func (h *headlessBar) wait(what string, cond func() error) {
	h.t.Helper()
	var last error
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if last = cond(); last == nil {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s: %v", what, last)
		}
	}
}

// buttons waits for the open bar to show n module buttons and returns
// them in tree order.
func (h *headlessBar) buttons(n int) []*barButton {
	h.t.Helper()
	return h.buttonsWhere(fmt.Sprintf("%d module buttons", n), func(bs []*barButton) error {
		if len(bs) != n {
			return fmt.Errorf("%d module buttons", len(bs))
		}
		return nil
	})
}

// buttonsWhere waits until the open bar's module buttons, in tree
// order, pass and returns them.
func (h *headlessBar) buttonsWhere(what string, pass func([]*barButton) error) []*barButton {
	h.t.Helper()
	var out []*barButton
	h.wait(what, func() error {
		out = nil
		h.onLoop(func() {
			if h.bar == nil {
				return
			}
			for _, w := range treetest.WithClass(h.bar.bar, "bar-button") {
				if b, ok := w.(*barButton); ok {
					out = append(out, b)
				}
			}
		})
		return pass(out)
	})
	return out
}
