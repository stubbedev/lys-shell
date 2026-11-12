package printdialog

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

func TestTheSheetFloatsOnTheOverlayAndFillsAWindow(t *testing.T) {
	hs := newHarness(t)
	got, _ := hs.prepare(t)
	hs.mu.Lock()
	spec := hs.specs[len(hs.specs)-1]
	hs.mu.Unlock()
	for _, tc := range []struct {
		surface  dialogsurface.Surface
		floating bool
	}{{dialogsurface.OnWindow, false}, {dialogsurface.OnOverlay, true}, {dialogsurface.OnWindow, false}} {
		hs.do(func() {
			root := spec.Root(tc.surface)
			if floats := widget.HasClass(root, "print-window"); floats != tc.floating {
				t.Errorf("on %v the root carries print-window: %v, want %v", tc.surface, floats, tc.floating)
			}
			corners := surfacetest.CornerAlphas(root)
			if opaque := corners == [4]byte{0xff, 0xff, 0xff, 0xff}; opaque == tc.floating {
				t.Errorf("on %v the sheet's corners have alpha %v, want them opaque only in a window", tc.surface, corners)
			}
		})
	}
	hs.do(spec.Failed)
	<-got
}
