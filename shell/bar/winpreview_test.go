package bar

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strconv"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

type previewRig struct {
	p        *windowPreview
	timers   []*pendingTimer
	queued   []func()
	opened   int
	dismiss  int
	closed   func()
	content  widget.Widget
	captured map[string]int
	focused  []string
	closedW  []string
}

type rigActions struct{ r *previewRig }

func (a rigActions) capture(_ context.Context, w previewWindow) (image.Image, error) {
	a.r.captured[w.id]++
	return image.NewRGBA(image.Rect(0, 0, 160, 90)), nil
}
func (a rigActions) focus(w previewWindow) { a.r.focused = append(a.r.focused, w.id) }
func (a rigActions) close(w previewWindow) { a.r.closedW = append(a.r.closedW, w.id) }

func newPreviewRig(t *testing.T) *previewRig {
	r := &previewRig{captured: map[string]int{}}
	ctx := newTestContext(t, config.Defaults())
	r.p = newWindowPreview(ctx, nil)
	r.p.after = func(d time.Duration, fn func()) func() {
		tm := &pendingTimer{d: d, fn: fn}
		r.timers = append(r.timers, tm)
		return func() { tm.stopped = true }
	}
	r.p.spawn = func(fn func()) { r.queued = append(r.queued, fn) }
	r.p.invoke = func(fn func()) { fn() }
	r.p.open = func(_ app.Host, _ widget.Boundser, content widget.Widget, closed func()) (func(), error) {
		r.opened++
		r.content, r.closed = content, closed
		return func() { r.dismiss++ }, nil
	}
	r.p.Attach(nil)
	r.p.host = app.Host(nil)
	return r
}

func (r *previewRig) fire() {
	timers := r.timers
	r.timers = nil
	for _, tm := range timers {
		if !tm.stopped {
			tm.fn()
		}
	}
}

func (r *previewRig) work() {
	for len(r.queued) > 0 {
		fn := r.queued[0]
		r.queued = r.queued[1:]
		fn()
	}
}

func threeWindows() []previewWindow {
	return []previewWindow{
		{id: "1", title: "editor", appID: "code", rect: lyswm.Rect{X: 0, Y: 0, Width: 960, Height: 1080}, hasRect: true},
		{id: "2", title: "browser", appID: "firefox", rect: lyswm.Rect{X: 960, Y: 0, Width: 960, Height: 540}, hasRect: true},
		{id: "3", title: "terminal", appID: "foot", rect: lyswm.Rect{X: 960, Y: 540, Width: 960, Height: 540}, hasRect: true},
	}
}

func (r *previewRig) gather() ([]previewWindow, previewActions) { return threeWindows(), rigActions{r} }

func TestWindowPreviewSweepingAcrossTheBarCapturesNothing(t *testing.T) {
	r := newPreviewRig(t)
	r.p.host = fakeHost{}
	anchor := widget.NewBox(widget.Row, 0, 0)
	for range 10 {
		r.p.hover(true, anchor, r.gather)
		r.p.hover(false, anchor, nil)
	}
	r.fire()
	r.work()
	if r.opened != 0 || r.p.captures != 0 || len(r.captured) != 0 {
		t.Errorf("a sweep opened %d previews and captured %d windows, want none", r.opened, r.p.captures)
	}
}

func TestWindowPreviewCapturesEachWindowOnceWhileOpen(t *testing.T) {
	r := newPreviewRig(t)
	r.p.host = fakeHost{}
	anchor := widget.NewBox(widget.Row, 0, 0)
	r.p.hover(true, anchor, r.gather)
	r.fire()
	if r.opened != 1 {
		t.Fatalf("a rest opened %d previews, want 1", r.opened)
	}
	r.work()
	for range 10 {
		r.p.hover(true, anchor, r.gather)
		r.fire()
		r.work()
	}
	for _, w := range threeWindows() {
		if r.captured[w.id] != 1 {
			t.Errorf("window %s captured %d times while open, want once", w.id, r.captured[w.id])
		}
	}
	if len(r.p.shown.images) != 3 {
		t.Fatalf("%d thumbnails held, want 3", len(r.p.shown.images))
	}
	shown := r.p.shown
	r.p.hover(false, anchor, nil)
	r.fire()
	if r.dismiss != 1 || r.p.shown != nil || shown.images != nil {
		t.Errorf("leaving: %d dismissals, shown %v, %d thumbnails held; want it closed and its thumbnails freed", r.dismiss, r.p.shown, len(shown.images))
	}
}

func TestWindowPreviewStaysWhileThePointerIsOnIt(t *testing.T) {
	r := newPreviewRig(t)
	r.p.host = fakeHost{}
	anchor := widget.NewBox(widget.Row, 0, 0)
	r.p.hover(true, anchor, r.gather)
	r.fire()
	r.p.hover(false, anchor, nil)
	r.p.shown.hovered = true
	r.p.syncLeave()
	r.fire()
	if r.p.shown == nil {
		t.Fatal("the preview closed under the pointer")
	}
	r.p.shown.hovered = false
	r.p.syncLeave()
	r.fire()
	if r.p.shown != nil {
		t.Error("the preview stayed after the pointer left it")
	}
}

func TestWindowPreviewClickFocusesAndMiddleClickCloses(t *testing.T) {
	r := newPreviewRig(t)
	r.p.host = fakeHost{}
	anchor := widget.NewBox(widget.Row, 0, 0)
	r.p.hover(true, anchor, r.gather)
	r.fire()
	cells := previewCells(r.content)
	if len(cells) != 3 {
		t.Fatalf("%d cells, want one per window", len(cells))
	}
	press(r.content, cells[1], widget.BTNLeft)
	if fmt.Sprint(r.focused) != "[2]" || r.p.shown != nil {
		t.Errorf("click focused %v, preview open %v; want window 2 focused and the preview closed", r.focused, r.p.shown != nil)
	}
	r.p.hover(true, anchor, r.gather)
	r.fire()
	press(r.content, previewCells(r.content)[2], widget.BTNMiddle)
	if fmt.Sprint(r.closedW) != "[3]" {
		t.Errorf("middle click closed %v, want window 3", r.closedW)
	}
	if r.closed != nil {
		r.closed()
	}
}

func TestPlaceWindowsKeepsTheirArrangement(t *testing.T) {
	placed := placeWindows(threeWindows(), 420, 240)
	if len(placed) != 3 {
		t.Fatalf("placed %v", placed)
	}
	left, topRight, bottomRight := placed[0], placed[1], placed[2]
	if left.X >= topRight.X || topRight.Y >= bottomRight.Y || topRight.X != bottomRight.X {
		t.Errorf("arrangement lost: %v", placed)
	}
	if left.H != topRight.H*2 || left.W != topRight.W {
		t.Errorf("proportions lost: %v", placed)
	}
	if placed := placeWindows([]previewWindow{{id: "x"}}, 420, 240); placed != nil {
		t.Errorf("a window without a rectangle is placed at %v", placed)
	}
}

func previewCells(content widget.Widget) []*widget.Box {
	var out []*widget.Box
	var walk func(w widget.Widget)
	walk = func(w widget.Widget) {
		if b, ok := w.(*widget.Box); ok && widget.HasClass(b, "window-preview-cell") {
			out = append(out, b)
			return
		}
		if c, ok := w.(interface{ Children() []widget.Widget }); ok {
			for _, k := range c.Children() {
				walk(k)
			}
		}
	}
	walk(content)
	return out
}

func press(root widget.Widget, b *widget.Box, button uint32) {
	root.Arrange(render.Rect{W: 600, H: 400})
	bounds := b.Bounds()
	p := widget.Point{X: bounds.X + bounds.W/2, Y: bounds.Y + bounds.H/2}
	r := &widget.Router{Root: root}
	r.Move(p)
	r.Press(button, p, 0)
	r.Release(button, p)
}

// On a real lys-wm a preview's thumbnail click focuses that window, and
// each window captures through the compositor.
func TestWindowPreviewFocusesAndCapturesOnLysWM(t *testing.T) {
	lyswmtest.Headless(t)
	backend := modes.DetectWindows()
	if backend == nil {
		t.Fatal("lys-wm is not detected")
	}
	startProbeProcess(t, "org.example.First", "first")
	startProbeProcess(t, "org.example.Second", "second")
	var first, second lyswm.Window
	until(t, "both probes on lys-wm", func() error {
		var err error
		if first, err = lysWMWindow("org.example.First"); err != nil {
			return err
		}
		second, err = lysWMWindow("org.example.Second")
		return err
	})
	target := first
	if first.Focused {
		target = second
	}
	actions := &compositorActions{backend: backend}
	w := previewWindow{id: strconv.FormatUint(target.ID, 10), title: target.Title, appID: target.AppID}
	img, err := actions.capture(context.Background(), w)
	if err != nil {
		t.Fatalf("capture through lys-wm: %v", err)
	}
	if b := img.Bounds(); min(b.Dx(), b.Dy()) > previewThumbPx || b.Empty() {
		t.Errorf("thumbnail %v, want its smaller side within %dpx", b, previewThumbPx)
	}
	actions.focus(w)
	until(t, "the clicked window focused", func() error {
		got, err := lysWMWindow(target.AppID)
		if err == nil && !got.Focused {
			err = errors.New("not focused")
		}
		return err
	})
}
