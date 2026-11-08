package markup

import (
	"errors"
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
	done func(error)
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
			Save: func(img *image.RGBA, path string, done func(error)) {
				h.saves <- saved{img, path, done}
			},
			SaveAs: func(_ *image.RGBA, _ string, done func(error)) { done(nil) },
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
	apptest.On(a, func() bool { s.done(nil); return true })
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

// The write runs off the loop: the editor stays open until it is done,
// takes no second save meanwhile, and a failed write keeps it open
// with the error on show. A crop then saves only the kept part, and
// undoing the crop saves the whole capture again.
func TestSaveWaitsForTheWriteAndAFailureKeepsTheEditorOpen(t *testing.T) {
	a := apptest.Run(t)
	h := openEditor(t, a, filepath.Join(t.TempDir(), "x.png"))
	keys := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)

	apptest.On(a, func() bool { h.e.SetTool(ToolCrop); return true })
	h.drag(t, image.Pt(20, 10), image.Pt(90, 60))
	keys.Tap(keytest.Return)
	s := apptest.Answer(t, "Enter starts the write", h.saves)
	if got := s.img.Bounds(); got != image.Rect(0, 0, 70, 50) {
		t.Fatalf("the cropped save is %v, want the 70x50 kept part", got)
	}
	if !apptest.On(a, h.e.Saving) {
		t.Fatal("the editor is not saving while the write runs")
	}
	keys.Tap(keytest.Return)
	apptest.Never(t, "a second Enter during the write saved again", h.saves)
	apptest.Never(t, "the editor closed before the write finished", h.closed)

	apptest.On(a, func() bool { s.done(errors.New("cannot save x.png: disk full")); return true })
	apptest.Never(t, "a failed write closed the editor", h.closed)
	if got := apptest.On(a, func() string { return h.e.status.Text() }); got != "cannot save x.png: disk full" {
		t.Fatalf("the editor shows %q after a failed write", got)
	}

	apptest.On(a, func() bool { h.e.Undo(); return true })
	keys.Tap(keytest.Return)
	s = apptest.Answer(t, "Enter retries the write", h.saves)
	if got := s.img.Bounds(); got != image.Rect(0, 0, 200, 120) {
		t.Fatalf("with the crop undone the save is %v, want the whole capture", got)
	}
	if got := apptest.On(a, func() string { return h.e.status.Text() }); got != "" {
		t.Fatalf("a retry still shows %q", got)
	}
	apptest.On(a, func() bool { s.done(nil); return true })
	apptest.Answer(t, "a finished write closes the editor", h.closed)
}

// Escape during a drag drops the mark being drawn and keeps the
// editor; the next Escape closes it.
func TestEscapeCancelsADragBeforeClosing(t *testing.T) {
	a := apptest.Run(t)
	h := openEditor(t, a, filepath.Join(t.TempDir(), "x.png"))
	keys := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)

	apptest.On(a, func() bool {
		v := h.e.view.fit()
		x0, y0 := v.point(image.Pt(10, 10))
		x1, y1 := v.point(image.Pt(80, 60))
		h.e.view.PressAt(widget.Point{X: x0, Y: y0})
		h.e.view.DragMove(widget.Point{X: x1, Y: y1})
		return true
	})
	keys.Tap(keytest.Escape)
	apptest.Never(t, "Escape during a drag closed the editor", h.closed)
	apptest.On(a, func() bool { h.e.view.PressEnd(); return true })
	if n := apptest.On(a, func() int { return len(h.e.doc.Marks()) }); n != 0 {
		t.Fatalf("the cancelled drag left %d marks", n)
	}
	keys.Tap(keytest.Escape)
	apptest.Answer(t, "Escape with no drag closes the editor", h.closed)
}

// Undo and redo are offered only when there is something to take back
// or reapply.
func TestUndoAndRedoAreEnabledOnlyWithHistory(t *testing.T) {
	a := apptest.Run(t)
	h := openEditor(t, a, filepath.Join(t.TempDir(), "x.png"))
	state := func() [2]bool {
		return apptest.On(a, func() [2]bool { return [2]bool{h.e.undo.Enabled(), h.e.redo.Enabled()} })
	}
	if got := state(); got != [2]bool{false, false} {
		t.Fatalf("a fresh editor offers undo, redo = %v", got)
	}
	h.drag(t, image.Pt(10, 10), image.Pt(80, 60))
	if got := state(); got != [2]bool{true, false} {
		t.Fatalf("after a mark undo, redo = %v", got)
	}
	apptest.On(a, func() bool { h.e.Undo(); return true })
	if got := state(); got != [2]bool{false, true} {
		t.Fatalf("after undoing it undo, redo = %v", got)
	}
}

// The text tool shows the text caret, every other tool the crosshair;
// the marker tool previews the numbered mark a click would place under
// the pointer, and nothing when the pointer leaves the image or another
// tool is picked.
func TestPointerShapeAndMarkerPreviewFollowTheTool(t *testing.T) {
	a := apptest.Run(t)
	h := openEditor(t, a, filepath.Join(t.TempDir(), "x.png"))
	at := func(p image.Point) widget.Point {
		x, y := h.e.view.fit().point(p)
		return widget.Point{X: x, Y: y}
	}
	cursor := func(tool Tool) string {
		return apptest.On(a, func() string { h.e.SetTool(tool); return h.e.view.CursorName() })
	}
	for _, tool := range Tools {
		want := "crosshair"
		if tool == ToolText {
			want = "text"
		}
		if got := cursor(tool); got != want {
			t.Fatalf("tool %d shows cursor %q, want %q", tool, got, want)
		}
	}

	type preview struct {
		mark Mark
		ok   bool
	}
	ghost := func() preview {
		return apptest.On(a, func() preview { m, ok := h.e.view.ghost(); return preview{m, ok} })
	}
	apptest.On(a, func() bool { h.e.SetTool(ToolMarker); h.e.view.HoverMove(at(image.Pt(50, 40))); return true })
	if g := ghost(); !g.ok || g.mark.Number != 1 || g.mark.Points[0] != image.Pt(50, 40) {
		t.Fatalf("hovering the image with the marker previews %+v", g)
	}
	apptest.On(a, func() bool { h.e.view.PressAt(at(image.Pt(50, 40))); h.e.view.PressEnd(); return true })
	if g := ghost(); !g.ok || g.mark.Number != 2 {
		t.Fatalf("after placing a marker the preview is %+v, want number 2", g)
	}
	apptest.On(a, func() bool { h.e.view.HoverMove(widget.Point{X: 0, Y: 0}); return true })
	if g := ghost(); g.ok {
		t.Fatalf("the pointer off the image still previews %+v", g)
	}
	apptest.On(a, func() bool { h.e.view.HoverMove(at(image.Pt(50, 40))); h.e.SetTool(ToolRect); return true })
	if g := ghost(); g.ok {
		t.Fatalf("the rectangle tool previews a marker %+v", g)
	}
}
