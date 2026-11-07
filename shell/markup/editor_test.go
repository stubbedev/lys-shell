package markup

import (
	"image"
	"image/color"
	"path/filepath"
	"runtime"
	"testing"
	"weak"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
)

type saved struct {
	img  *image.RGBA
	path string
}

type editorHarness struct {
	a      *app.Application
	e      *Editor
	saves  chan saved
	closed chan struct{}
	pixels weak.Pointer[image.RGBA]
}

func openEditor(t *testing.T, a *app.Application, path string) *editorHarness {
	t.Helper()
	font, err := render.NewFixtureTypeface()
	if err != nil {
		t.Fatal(err)
	}
	h := &editorHarness{a: a, saves: make(chan saved, 1), closed: make(chan struct{})}
	base := canvasImage(200, 120, color.RGBA{255, 255, 255, 255})
	h.pixels = weak.Make(base.RGBA)
	id := base.ID
	var openErr error
	apptest.On(a, func() bool {
		h.e, openErr = Open(Deps{
			App:     a,
			Font:    font,
			Px:      14,
			Palette: []render.Color{render.RGB(255, 0, 0), render.RGB(0, 0, 255)},
			Save: func(img *image.RGBA, path string) error {
				h.saves <- saved{img, path}
				return nil
			},
			SaveAs: func(*image.RGBA, string) error { return nil },
			Closed: func() {
				a.ForgetImage(id)
				close(h.closed)
			},
		}, base, path)
		return true
	})
	if openErr != nil {
		t.Fatal(openErr)
	}
	apptest.Until(t, a, "the canvas is laid out", func() bool { return h.e.view.Bounds().W > 0 })
	return h
}

func (h *editorHarness) drag(t *testing.T, from, to image.Point) {
	t.Helper()
	apptest.On(h.a, func() bool {
		v := h.e.view.fit()
		at := func(p image.Point) widget.Point {
			x, y := v.point(p)
			return widget.Point{X: x, Y: y}
		}
		h.e.view.PressAt(at(from))
		h.e.view.DragMove(at(to))
		h.e.view.PressEnd()
		return true
	})
}

// The editor draws the capture from memory: the file it names does not
// even exist. Marks drawn, text typed and Enter flatten them into the
// image handed to Save, and once closed nothing holds the capture.
func TestEditorSavesTheMarksAndLetsTheCaptureGo(t *testing.T) {
	a := apptest.Run(t)
	path := filepath.Join(t.TempDir(), "never-written.png")
	h := openEditor(t, a, path)
	keys := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)

	apptest.On(a, func() bool { h.e.SetTool(ToolRect); return true })
	h.drag(t, image.Pt(10, 10), image.Pt(60, 50))
	apptest.On(a, func() bool { h.e.SetTool(ToolPixelate); return true })
	h.drag(t, image.Pt(100, 10), image.Pt(160, 60))
	apptest.On(a, func() bool { h.e.SetTool(ToolText); return true })
	h.drag(t, image.Pt(20, 80), image.Pt(20, 80))
	keys.Tap(keytest.H)
	keys.Tap(keytest.L)
	keys.Tap(keytest.Return)
	if n := apptest.On(a, func() int { return len(h.e.doc.Marks()) }); n != 3 {
		t.Fatalf("%d marks after a rectangle, a pixelation and a text", n)
	}
	if got := apptest.On(a, func() string { return h.e.doc.Marks()[2].Text }); got != "hl" {
		t.Fatalf("typed %q, want hl", got)
	}
	select {
	case s := <-h.saves:
		t.Fatalf("committing the text saved %v", s.path)
	default:
	}

	keys.Tap(keytest.Return)
	s := apptest.Answer(t, "Enter saves", h.saves)
	if s.path != path {
		t.Fatalf("saved to %q, want %q", s.path, path)
	}
	if got := s.img.RGBAAt(10, 30); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("the saved rectangle edge is %v", got)
	}
	apptest.Answer(t, "the editor closes after saving", h.closed)
	s = saved{}
	h.e = nil
	gone := false
	for range 20 {
		runtime.GC()
		if h.pixels.Value() == nil {
			gone = true
			break
		}
		apptest.Dispatched(t, a)
	}
	if !gone {
		t.Fatal("the capture's pixels are still held after the editor closed")
	}
}

func TestEscapeClosesWithoutSavingAndUndoTakesMarksBack(t *testing.T) {
	a := apptest.Run(t)
	h := openEditor(t, a, filepath.Join(t.TempDir(), "x.png"))
	keys := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)

	h.drag(t, image.Pt(10, 10), image.Pt(80, 60))
	apptest.On(a, func() bool { h.e.Undo(); return true })
	if n := apptest.On(a, func() int { return len(h.e.doc.Marks()) }); n != 0 {
		t.Fatalf("undo left %d marks", n)
	}
	apptest.On(a, func() bool { h.e.Redo(); return true })
	if n := apptest.On(a, func() int { return len(h.e.doc.Marks()) }); n != 1 {
		t.Fatalf("redo left %d marks", n)
	}

	keys.Tap(keytest.Escape)
	apptest.Answer(t, "Escape closes the editor", h.closed)
	apptest.Never(t, "Escape saved", h.saves)
}
