package sharepicker

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

type closingWindow struct{ closed bool }

func (w *closingWindow) Close()                     { w.closed = true }
func (*closingWindow) SetFocus(widget.Widget) error { return nil }
func (*closingWindow) HostSurface() *wl.Surface     { return nil }

func TestThePickerFloatsOnTheOverlayAndFillsAWindow(t *testing.T) {
	p, ch := testPicker(t, config.DefaultsSharePicker(), false, false)
	p.d.Sheet = apptheme.New(config.Defaults()).Sheet
	for _, tc := range []struct {
		surface  dialogsurface.Surface
		floating bool
	}{{dialogsurface.OnWindow, false}, {dialogsurface.OnOverlay, true}, {dialogsurface.OnWindow, false}, {dialogsurface.OnOverlay, true}} {
		root := p.root(tc.surface)
		if floats := widget.HasClass(root, "share-picker-window"); floats != tc.floating {
			t.Errorf("on %v the root carries share-picker-window: %v, want %v", tc.surface, floats, tc.floating)
		}
		corners := surfacetest.CornerAlphas(root)
		if opaque := corners == [4]byte{0xff, 0xff, 0xff, 0xff}; opaque == tc.floating {
			t.Errorf("on %v the picker's corners have alpha %v, want them opaque only in a window", tc.surface, corners)
		}
		if !treetest.Contains(root, p.nb) || !treetest.Contains(root, p.rev) || !p.rev.Revealed() {
			t.Errorf("on %v the root does not hold the picker's notebook behind its revealed reveal", tc.surface)
		}
	}
	shown := &closingWindow{}
	p.layer = shown
	p.answer("")
	if got := <-ch; got != "" {
		t.Fatalf("the cancel replied %q", got)
	}
	if p.rev.Revealed() || !shown.closed {
		t.Errorf("after moving to the overlay, dismissing left the reveal revealed %v and the surface closed %v, want it hidden and closed", p.rev.Revealed(), shown.closed)
	}
}

func TestAWindowedPickerOpensAtItsConfiguredSizeAndGrowsFromIt(t *testing.T) {
	p, _ := testPicker(t, config.DefaultsSharePicker(), false, false)
	size := p.size()
	if got, want := p.spec().Window, (dialogsurface.WindowSize{Initial: size, Min: size}); got.Initial != want.Initial || got.Min != want.Min || got.Max != want.Max {
		t.Errorf("the window's size %+v, want it opening at and bounded below by the configured %v, unbounded above", got, size)
	}
	p.d.Sheet = apptheme.New(config.Defaults()).Sheet
	root := p.root(dialogsurface.OnWindow)
	if natural := widget.SizeWithin(root, widget.Size{W: 4096, H: 4096}); natural != size {
		t.Errorf("the window's tree measures %v, want the %v it opens at", natural, size)
	}
	grown := widget.Size{W: size.W + 200, H: size.H + 100}
	root.Arrange(render.Rect{W: grown.W, H: grown.H})
	if got := p.surface.Bounds(); got.W != grown.W || got.H != grown.H {
		t.Errorf("in a %v window the card spans %v, want it to fill the window", grown, got)
	}
}
