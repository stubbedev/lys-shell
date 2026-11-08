package shotthumb

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/fakeclock"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
)

const btnLeft = 0x110

type harness struct {
	t       *testing.T
	a       *app.Application
	clock   *fakeclock.Clock
	thumb   *Thumbnail
	ptr     *vinput.Device
	layer   *app.LayerWindow
	edited  []Shot
	deleted []Shot
	done    []Shot
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	a := apptest.Run(t)
	h := &harness{t: t, a: a, clock: fakeclock.New()}
	h.thumb = New(Deps{
		Open: func(lc app.LayerConfig) (Window, error) {
			w, err := a.NewLayer(lc)
			if err != nil {
				return nil, err
			}
			h.layer = w
			return w, nil
		},
		KeySym: a.KeySym,
		Invoke: a.Invoke,
		Clock:  h.clock,
		Edit:   func(s Shot) { h.edited = append(h.edited, s) },
		Delete: func(s Shot) { h.deleted = append(h.deleted, s) },
		Done:   func(s Shot) { h.done = append(h.done, s) },
	})
	ptr, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ptr.Close)
	h.ptr = ptr
	return h
}

func shot(t *testing.T, w, h int) Shot {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return Shot{Path: path, Image: render.NewStillImage(img)}
}

func thumbConfig(ms uint32) config.ScreenshotConfig {
	cfg := config.DefaultsScreenshot()
	cfg.ThumbnailMs = ms
	return cfg
}

func (h *harness) show(s Shot, cfg config.ScreenshotConfig) {
	h.t.Helper()
	if err := apptest.On(h.a, func() error { return h.thumb.Show(s, nil, cfg) }); err != nil {
		h.t.Fatal(err)
	}
	if cfg.ThumbnailMs > 0 {
		apptest.Until(h.t, h.a, "the thumbnail is laid out", func() bool { return h.thumb.cur != nil && h.thumb.cur.view.Bounds().W > 0 })
		surface := apptest.On(h.a, func() *wl.Surface { return h.layer.HostSurface() })
		apptest.Drawn(h.t, h.a, surface, func() { h.thumb.cur.view.Invalidate() })
	}
}

func (h *harness) shown() bool {
	return apptest.On(h.a, func() bool { _, ok := h.thumb.Shown(); return ok })
}

func (h *harness) advance(d time.Duration) {
	h.clock.Advance(d)
	apptest.Dispatched(h.t, h.a)
	apptest.On(h.a, func() bool { return true })
}

// center is where the thumbnail sits on the output, in output pixels.
func (h *harness) center() (uint32, uint32) {
	b := apptest.On(h.a, func() render.Rect { return h.thumb.cur.view.Bounds() })
	w, hh := h.ptr.OutputSize()
	return uint32(int(w) - margin - b.W/2), uint32(int(hh) - margin - b.H/2)
}

func (h *harness) moveTo(x, y uint32) {
	h.t.Helper()
	w, hh := h.ptr.OutputSize()
	if err := h.ptr.MotionAbsolute(x, y, uint32(w), uint32(hh)); err != nil {
		h.t.Fatal(err)
	}
	h.sync()
}

func (h *harness) button(pressed bool) {
	h.t.Helper()
	if err := h.ptr.Button(btnLeft, pressed); err != nil {
		h.t.Fatal(err)
	}
	h.sync()
}

func (h *harness) sync() {
	h.t.Helper()
	if err := h.ptr.Roundtrip(); err != nil {
		h.t.Fatal(err)
	}
	apptest.Dispatched(h.t, h.a)
}

func TestThumbnailDismissesItselfAfterItsTime(t *testing.T) {
	h := newHarness(t)
	s := shot(t, 400, 300)
	h.show(s, thumbConfig(5000))
	if !h.shown() {
		t.Fatal("no thumbnail after a capture")
	}
	h.advance(4999 * time.Millisecond)
	if !h.shown() {
		t.Fatal("the thumbnail left before its time")
	}
	h.advance(time.Millisecond)
	if h.shown() {
		t.Fatal("the thumbnail outstayed its time")
	}
	if len(h.done) != 1 || h.done[0].Path != s.Path || len(h.edited)+len(h.deleted) != 0 {
		t.Fatalf("done %v edited %v deleted %v", h.done, h.edited, h.deleted)
	}
}

func TestDisabledThumbnailShowsNothing(t *testing.T) {
	h := newHarness(t)
	h.show(shot(t, 40, 30), thumbConfig(0))
	if h.shown() || len(h.done) != 1 {
		t.Fatalf("a 0 ms thumbnail: shown %v, done %d", h.shown(), len(h.done))
	}
}

func TestHoverPausesTheCountdown(t *testing.T) {
	h := newHarness(t)
	h.show(shot(t, 400, 300), thumbConfig(5000))
	h.advance(2000 * time.Millisecond)
	h.moveTo(h.center())
	if !apptest.On(h.a, func() bool { return h.thumb.cur.hovered }) {
		t.Fatal("the pointer over the thumbnail is not a hover")
	}
	h.advance(time.Minute)
	if !h.shown() {
		t.Fatal("the thumbnail left while hovered")
	}
	h.moveTo(5, 5)
	h.advance(2999 * time.Millisecond)
	if !h.shown() {
		t.Fatal("leaving restarted less than the remaining 3 s")
	}
	h.advance(time.Millisecond)
	if h.shown() {
		t.Fatal("the remaining time ran out and the thumbnail stayed")
	}
}

func TestClickEditsAndTheCloseButtonDeletes(t *testing.T) {
	h := newHarness(t)
	s := shot(t, 400, 300)
	h.show(s, thumbConfig(5000))
	h.moveTo(h.center())
	h.button(true)
	h.button(false)
	if len(h.edited) != 1 || h.edited[0] != s || h.shown() || len(h.deleted) != 0 {
		t.Fatalf("click: edited %v deleted %v shown %v", h.edited, h.deleted, h.shown())
	}

	h.show(s, thumbConfig(5000))
	c := apptest.On(h.a, func() render.Rect { return h.thumb.cur.view.closeRect() })
	b := apptest.On(h.a, func() render.Rect { return h.thumb.cur.view.Bounds() })
	w, hh := h.ptr.OutputSize()
	h.moveTo(uint32(int(w)-margin-b.W+c.X-b.X+c.W/2), uint32(int(hh)-margin-b.H+c.Y-b.Y+c.H/2))
	h.button(true)
	h.button(false)
	if len(h.deleted) != 1 || h.deleted[0] != s || len(h.edited) != 1 || h.shown() {
		t.Fatalf("close button: edited %v deleted %v shown %v", h.edited, h.deleted, h.shown())
	}
}

func TestSwipeDismissesWithoutTouchingTheFile(t *testing.T) {
	h := newHarness(t)
	s := shot(t, 400, 300)
	h.show(s, thumbConfig(5000))
	h.moveTo(h.center())
	for range 10 {
		if err := h.ptr.Axis(vinput.SourceFinger, vinput.AxisHorizontal, 15); err != nil {
			t.Fatal(err)
		}
		h.sync()
	}
	if h.shown() {
		t.Fatal("a sideways swipe left the thumbnail up")
	}
	if len(h.edited)+len(h.deleted) != 0 {
		t.Fatalf("a swipe edited %v or deleted %v", h.edited, h.deleted)
	}
	if _, err := os.Stat(s.Path); err != nil {
		t.Fatal(err)
	}
}

type dropTarget struct {
	widget.Base
	accept string
	got    chan []byte
}

func (d *dropTarget) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }

func (d *dropTarget) HitTest(p widget.Point) widget.Widget { return d.HitLeaf(d, p) }

func (d *dropTarget) Paint(*render.Canvas) {}

func (d *dropTarget) DragEnter(mimes []string, _ widget.Point) string {
	if slices.Contains(mimes, d.accept) {
		return d.accept
	}
	return ""
}

func (d *dropTarget) Drop(_ string, data []byte, _ widget.Point) { d.got <- data }

func (h *harness) dragTo(target *dropTarget) []byte {
	h.t.Helper()
	x, y := h.center()
	h.moveTo(x, y)
	h.button(true)
	for i := 1; i <= 20; i++ {
		h.moveTo(x-uint32(i*20), y-uint32(i*15))
	}
	h.button(false)
	return apptest.Answer(h.t, "the drop", target.got)
}

func TestDraggingDeliversTheFileAndThePNG(t *testing.T) {
	h := newHarness(t)
	s := shot(t, 400, 300)
	target := &dropTarget{accept: transfer.MimeURIList, got: make(chan []byte, 1)}
	var win *app.Window
	if err := apptest.On(h.a, func() error {
		var err error
		win, err = h.a.NewWindow(app.WindowConfig{Title: "drop", AppID: "drop", Root: target})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	apptest.Until(t, h.a, "the drop target is laid out", func() bool { return target.Bounds().W > 0 })
	apptest.Drawn(t, h.a, apptest.On(h.a, win.HostSurface), target.Invalidate)

	h.show(s, thumbConfig(5000))
	if got, want := string(h.dragTo(target)), transfer.FileURI(s.Path)+"\r\n"; got != want {
		t.Fatalf("dropped uri list %q, want %q", got, want)
	}
	if len(h.edited) != 0 {
		t.Fatal("a drag opened the editor")
	}

	apptest.On(h.a, func() bool { target.accept = transfer.MimePNG; return true })
	h.show(s, thumbConfig(5000))
	want, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.dragTo(target); string(got) != string(want) {
		t.Fatalf("dropped %d PNG bytes, want the file's %d", len(got), len(want))
	}
}

func TestSizeFitsWithoutEnlarging(t *testing.T) {
	for _, c := range []struct{ w, h, ww, wh int }{
		{480, 320, 240, 160},
		{100, 50, 100, 50},
		{2000, 100, 240, 12},
		{10, 4000, 1, 160},
	} {
		if w, h := Size(c.w, c.h); w != c.ww || h != c.wh {
			t.Errorf("Size(%d, %d) = %d, %d; want %d, %d", c.w, c.h, w, h, c.ww, c.wh)
		}
	}
}
