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

// On sway an auto-hiding dock without window geometry hides to a strip
// as wide as the panel it shows, reveals when the pointer reaches the
// edge, stays while the pointer rests there, and hides again a delay
// after the pointer leaves.
func TestHeadlessAutoHidingDockRevealsAtTheEdgeOnSway(t *testing.T) {
	swaytest.Start(t)
	autoHidingDockRevealsAtTheEdge(t)
}

func autoHidingDockRevealsAtTheEdge(t *testing.T) {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	application := app.NewApplication(sess)
	cfg := config.Defaults()
	cfg.Dock.Enabled = true
	cfg.Dock.AutoHide = true
	cfg.Dock.AutoHideDelayMs = 200
	cfg.Dock.Pinned = []string{"firefox", "foot"}
	ctx := styledContext(t, cfg)
	ctx.App = application
	ctx.Tasks = newTaskHub(&fakeToplevels{}, application.Invoke)
	ctx.Tasks.apps.goOff = func(func()) {}
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	outputs := sess.Outputs()
	if len(outputs) == 0 {
		t.Fatal("the compositor shows no outputs")
	}
	layer, err := (&dockHost{app: application, rt: rt}).open(outputs[0], cfg.Dock)
	if err != nil {
		t.Fatal(err)
	}
	w := layer.(*dockWindow)
	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	t.Cleanup(func() {
		application.Invoke(func() {
			w.Close()
			application.Quit()
		})
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
	})
	type state struct {
		width, height int
		hidden        bool
		hides         int
		rest          int
	}
	read := func() state {
		got := make(chan state, 1)
		application.Invoke(func() {
			width, height := w.layer.Size()
			got <- state{width, height, w.dock.hide.hidden, w.dock.hide.hides, w.dock.size.W}
		})
		return <-got
	}
	wait := func(what string, cond func(state) bool) state {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			s := read()
			if cond(s) {
				return s
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %+v", what, s)
			}
		}
	}
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pointer.Close()
	ow, oh := pointer.OutputSize()
	cursor := func(x, y int32) {
		t.Helper()
		if err := pointer.MotionAbsolute(intconv.Clamp[uint32](x), intconv.Clamp[uint32](y), intconv.Clamp[uint32](ow), intconv.Clamp[uint32](oh)); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}

	cursor(ow/2, oh/2)
	hidden := wait("the dock to hide", func(s state) bool { return s.hidden && s.height == revealPx })
	if hidden.rest == 0 || hidden.width != hidden.rest {
		t.Fatalf("the strip is %d wide, want the shown panel's %d", hidden.width, hidden.rest)
	}
	cursor(ow/2, oh-1)
	cursor(ow/2+1, oh-1)
	wait("the pointer at the edge to reveal it", func(s state) bool { return !s.hidden && s.height > revealPx })
	time.Sleep(3 * time.Duration(cfg.Dock.AutoHideDelayMs) * time.Millisecond)
	if s := read(); s.hidden || s.hides != hidden.hides {
		t.Fatalf("with the pointer resting on it the dock went %+v, hiding again", s)
	}
	cursor(ow/2, oh/2)
	wait("the dock to hide once the pointer left", func(s state) bool { return s.hidden && s.hides == hidden.hides+1 })
}
