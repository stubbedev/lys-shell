package launcher

import (
	"fmt"
	"image"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/launcheripc"
)

type fakeWalls struct {
	images   []string
	shown    map[string]string
	previews []string
	ended    int
	commits  [][2]string
	loads    []string
}

func (f *fakeWalls) Images() ([]string, error)    { return f.images, nil }
func (f *fakeWalls) Shown(monitor string) string  { return f.shown[monitor] }
func (f *fakeWalls) EndPreview()                  { f.ended++ }
func (f *fakeWalls) Preview(path, _ string) error { f.previews = append(f.previews, path); return nil }

func (f *fakeWalls) Commit(path, mon string) error {
	f.commits = append(f.commits, [2]string{path, mon})
	return nil
}

func (f *fakeWalls) LoadThumbnail(path string) (image.Image, error) {
	f.loads = append(f.loads, path)
	return image.NewRGBA(image.Rect(0, 0, 4, 3)), nil
}

type pickerHarness struct {
	*harness
	walls  *fakeWalls
	queued []func()
	reply  <-chan launcheripc.ServerFrame
}

func newPickerHarness(t *testing.T, images ...string) *pickerHarness {
	t.Helper()
	h := &pickerHarness{harness: newHarness(t), walls: &fakeWalls{images: images, shown: map[string]string{}}}
	h.s.d.Wallpapers = h.walls
	h.s.d.FocusedOutput = func() (string, bool) { return "DP-1", true }
	h.s.spawn = func(fn func()) { h.queued = append(h.queued, fn) }
	return h
}

func (h *pickerHarness) openPicker() *picker {
	h.t.Helper()
	mode := WallpaperMode
	_, h.reply = h.open(launcheripc.SessionOptions{Mode: &mode}, false)
	h.until("the picker", func() bool { return h.s.picker != nil })
	return h.s.picker
}

func (h *pickerHarness) press(spec string) {
	h.t.Helper()
	accel, err := app.ParseAccel(spec)
	if err != nil {
		h.t.Fatal(err)
	}
	if !h.windows[len(h.windows)-1].cfg.KeyCapture(accel) {
		h.t.Fatalf("%s went to the field", spec)
	}
}

func (h *pickerHarness) layout(p *picker) {
	p.grid.Arrange(render.Rect{W: pickerColumns * p.cellPx[0], H: pickerLines * p.cellPx[1]})
}

func (h *pickerHarness) runWork() {
	for len(h.queued) > 0 {
		fn := h.queued[0]
		h.queued = h.queued[1:]
		fn()
	}
	h.loop.pump()
}

func (h *pickerHarness) settle() {
	h.t.Helper()
	for _, tm := range h.timers {
		if tm.d == wallpaperSettle && !tm.stopped {
			tm.stopped = true
			tm.fn()
		}
	}
}

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("/walls/img%04d.png", i)
	}
	return out
}

func TestPickerListsEveryImageAndTheFilterNarrowsByName(t *testing.T) {
	h := newPickerHarness(t, "/w/beach.png", "/w/beach-night.png", "/w/forest.jpg")
	p := h.openPicker()
	if len(p.shown) != 3 {
		t.Fatalf("%d images shown, want 3", len(p.shown))
	}
	p.filter("beach")
	if got := len(p.shown); got != 2 {
		t.Errorf("beach shows %d images, want 2", got)
	}
	p.filter("forest")
	if path, _ := p.path(0); len(p.shown) != 1 || path != "/w/forest.jpg" {
		t.Errorf("forest shows %v", p.shown)
	}
	p.filter("nothing-like-it")
	if len(p.shown) != 0 || !p.chrome.message.Visible() {
		t.Errorf("an unmatched filter shows %d images, message %t", len(p.shown), p.chrome.message.Visible())
	}
}

func TestPickerThumbnailsOnlyTheCellsInView(t *testing.T) {
	h := newPickerHarness(t, names(1000)...)
	p := h.openPicker()
	h.layout(p)
	for range 300 {
		h.press("Down")
		h.layout(p)
	}
	h.runWork()
	if limit := pickerColumns * (pickerLines + 1); len(h.walls.loads) > limit {
		t.Fatalf("arrowing through 1,000 images made %d thumbnails, want at most %d", len(h.walls.loads), limit)
	}
	t.Logf("%d thumbnails made", len(h.walls.loads))
	selected, _ := p.path(p.grid.Selected())
	if !slices.Contains(h.walls.loads, selected) {
		t.Errorf("the selected image %s has no thumbnail; made %v", selected, h.walls.loads)
	}
	loads := len(h.walls.loads)
	for range 2 {
		h.layout(p)
	}
	h.runWork()
	if len(h.walls.loads) != loads {
		t.Errorf("laying the same cells out again made %d more thumbnails", len(h.walls.loads)-loads)
	}
}

func TestPickerEscapeAfterAPreviewChangesNothing(t *testing.T) {
	h := newPickerHarness(t, "/w/a.png", "/w/b.png", "/w/c.png")
	h.walls.shown["DP-1"] = "/w/a.png"
	p := h.openPicker()
	if p.grid.Selected() != 0 {
		t.Fatalf("the picker opened on %d, want the shown image", p.grid.Selected())
	}
	h.settle()
	if len(h.walls.previews) != 0 {
		t.Fatalf("opening previewed %v", h.walls.previews)
	}
	h.press("Right")
	h.settle()
	if !slices.Equal(h.walls.previews, []string{"/w/b.png"}) {
		t.Fatalf("previews %v, want b once the selection rested", h.walls.previews)
	}
	h.press("Escape")
	if h.walls.ended != 1 || len(h.walls.commits) != 0 {
		t.Errorf("escape: %d preview ends, commits %v; want the preview ended and nothing set", h.walls.ended, h.walls.commits)
	}
	if f := <-h.reply; f.Type != launcheripc.FrameCancelled {
		t.Errorf("escape answered %+v", f)
	}
}

func TestPickerArrowingPastAnImagePreviewsOnlyWhereItRests(t *testing.T) {
	h := newPickerHarness(t, names(12)...)
	h.layout(h.openPicker())
	h.press("Right")
	h.press("Right")
	h.press("Down")
	h.settle()
	if !slices.Equal(h.walls.previews, []string{"/walls/img0006.png"}) {
		t.Errorf("previews %v, want only where the selection rested", h.walls.previews)
	}
}

func TestPickerEnterSetsTheFocusedMonitorAndControlEnterEveryOne(t *testing.T) {
	h := newPickerHarness(t, "/w/a.png", "/w/b.png")
	h.openPicker()
	h.press("Right")
	h.press("Return")
	if !slices.Equal(h.walls.commits, [][2]string{{"/w/b.png", "DP-1"}}) || h.walls.ended != 1 {
		t.Errorf("enter committed %v, %d preview ends", h.walls.commits, h.walls.ended)
	}
	if f := <-h.reply; f.Type != launcheripc.FrameResult || len(f.Selected) != 1 || f.Selected[0].Text != "/w/b.png" {
		t.Errorf("enter answered %+v", f)
	}

	all := newPickerHarness(t, "/w/a.png", "/w/b.png")
	all.cfg.Launcher.Monitor = "HDMI-1"
	p := all.openPicker()
	if p.monitor != "HDMI-1" {
		t.Errorf("launcher.monitor HDMI-1 set the picker on %q", p.monitor)
	}
	all.press("Control+Return")
	if !slices.Equal(all.walls.commits, [][2]string{{"/w/a.png", ""}}) {
		t.Errorf("control+enter committed %v, want every monitor", all.walls.commits)
	}
}

func TestPickerWithoutTheWallpaperServiceCancels(t *testing.T) {
	h := newHarness(t)
	mode := WallpaperMode
	_, reply := h.open(launcheripc.SessionOptions{Mode: &mode}, false)
	h.loop.pump()
	if f := <-reply; f.Type != launcheripc.FrameCancelled || h.s.picker != nil || len(h.windows) != 0 {
		t.Errorf("no wallpaper service: %+v, picker %v, %d windows", f, h.s.picker, len(h.windows))
	}
}
