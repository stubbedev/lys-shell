package filechooser

import (
	"testing"

	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/dialogsurface"
)

func (hs *harness) lastSpec(t *testing.T) dialogsurface.Spec {
	t.Helper()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.specs) == 0 {
		t.Fatal("no dialog opened")
	}
	return hs.specs[len(hs.specs)-1]
}

func (hs *harness) openWindow(t *testing.T) chan []string {
	t.Helper()
	hs.mu.Lock()
	hs.surface = dialogsurface.OnWindow
	hs.mu.Unlock()
	return hs.open(t, OpenRequest{})
}

func TestAChooserWindowOpensAtItsSizeWithinThePanelBounds(t *testing.T) {
	hs := newHarness(t, "")
	hs.openWindow(t)
	w := hs.lastSpec(t).Window
	if w.Initial != panelSize || w.Min != panelMin || w.Max != panelMax || w.OnResize == nil {
		t.Errorf("the window opens at %v within %v to %v (resize heard %v), want %v within %v to %v, its resizes heard",
			w.Initial, w.Min, w.Max, w.OnResize != nil, panelSize, panelMin, panelMax)
	}
}

func TestAChooserWindowShrinksToItsFloor(t *testing.T) {
	hs := newHarness(t, "")
	hs.openWindow(t)
	root := hs.cfg.Root
	hs.run(func() {
		lay(root, panelSize.W, panelSize.H)
		least := widget.Size{W: root.Measure(widget.Row, widget.Unknown).Min, H: root.Measure(widget.Column, panelMin.W).Min}
		if least.W > panelMin.W || least.H > panelMin.H {
			t.Errorf("the window's content needs %v, want it to lay out at the %v floor so the window shrinks that far", least, panelMin)
		}
	})
}

func TestAChooserWindowIsFramedByTheWindowAlone(t *testing.T) {
	hs := newHarness(t, "")
	hs.openWindow(t)
	root := hs.cfg.Root
	full := render.Rect{W: panelSize.W, H: panelSize.H}
	bars := func() (header, footer render.Rect) {
		return classed(t, root, "file-chooser-header").Bounds(), classed(t, root, "file-chooser-footer").Bounds()
	}
	hs.run(func() {
		if n := len(treetest.All[*widget.FloatingPanel](root)); n != 0 {
			t.Errorf("the window floats %d panels, want its content to fill it", n)
		}
		if region, ok := widget.InputRegionOf(root); ok && len(region) > 0 {
			t.Errorf("the window takes input only over %v, want all of it", region)
		}
		lay(root, full.W, full.H)
		header, footer := bars()
		if header.X != 0 || header.Y != 0 || header.W != full.W || footer.Y+footer.H != full.H || footer.W != full.W {
			t.Errorf("header %v and footer %v, want them spanning the %v window edge to edge", header, footer, full)
		}
		px := make([]byte, render.Stride(full.W)*full.H)
		root.Paint(render.New(px, render.Stride(full.W), full.W, full.H))
		for _, at := range []widget.Point{{X: 0, Y: 0}, {X: full.W - 1, Y: 0}, {X: 0, Y: full.H - 1}, {X: full.W - 1, Y: full.H - 1}} {
			if alpha := px[at.Y*render.Stride(full.W)+at.X*4+3]; alpha != 0xff {
				t.Errorf("the window's corner %v has alpha %d, want it opaque: no rounding or shadow", at, alpha)
			}
		}
	})
	header, footer := bars()
	hs.do(func() {
		r, grab := treetest.Pointer(root, classed(t, root, "file-chooser-title"), full.W, full.H)
		drag(r, grab, widget.Point{X: grab.X + 40, Y: grab.Y + 30})
		corner := widget.Point{X: full.W - 4, Y: full.H - 4}
		drag(r, corner, widget.Point{X: corner.X - 100, Y: corner.Y - 100})
		lay(root, full.W, full.H)
	})
	if h, f := bars(); h != header || f != footer {
		t.Errorf("dragging the title and the corner moved the content to header %v and footer %v, want it where it was", h, f)
	}
}

func TestAChooserWindowMovedToTheOverlayFloatsAgain(t *testing.T) {
	hs := newHarness(t, "")
	hs.openWindow(t)
	spec := hs.lastSpec(t)
	hs.run(func() {
		root := spec.Root(dialogsurface.OnOverlay)
		lay(root, 1920, 1080)
		panel := treetest.First[*widget.FloatingPanel](t, root)
		want := render.Rect{X: (1920 - panelSize.W) / 2, Y: (1080 - panelSize.H) / 2, W: panelSize.W, H: panelSize.H}
		if p := panel.PanelRect(); p != want {
			t.Errorf("the panel on the overlay is at %v, want %v", p, want)
		}
		if title := classed(t, root, "file-chooser-title").Bounds(); !inside(title, within(want)) {
			t.Errorf("the title at %v, want it inside the panel %v", title, want)
		}
	})
}

func TestAChooserWindowResizeIsKeptForTheNextRequest(t *testing.T) {
	for name, tc := range map[string]struct{ resized, kept widget.Size }{
		"within the bounds": {resized: widget.Size{W: 900, H: 600}, kept: widget.Size{W: 900, H: 600}},
		"below the floor":   {resized: widget.Size{W: 300, H: 200}, kept: panelMin},
		"above the ceiling": {resized: widget.Size{W: 2000, H: 1400}, kept: panelMax},
	} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, "")
			got := hs.openWindow(t)
			onResize := hs.lastSpec(t).Window.OnResize
			hs.do(func() { onResize(tc.resized) })
			hs.do(func() { hs.c.finish(nil) })
			answer(t, got)
			got = hs.openWindow(t)
			if initial := hs.lastSpec(t).Window.Initial; initial != tc.kept {
				t.Errorf("after a resize to %v the next window opened at %v, want %v", tc.resized, initial, tc.kept)
			}
			hs.do(func() { hs.c.finish(nil) })
			answer(t, got)
			hs.mu.Lock()
			hs.surface = dialogsurface.OnOverlay
			hs.mu.Unlock()
			hs.open(t, OpenRequest{})
			hs.run(func() { lay(hs.cfg.Root, 3840, 2160) })
			if p := hs.panel(t).PanelRect(); p.W != tc.kept.W || p.H != tc.kept.H {
				t.Errorf("after a window resize to %v the overlay's panel opened at %v, want %v", tc.resized, p, tc.kept)
			}
		})
	}
}
