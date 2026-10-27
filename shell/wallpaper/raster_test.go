package wallpaper

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// photo is a decoder's YCbCr output with detail in every channel.
func photo(w, h int) image.Image {
	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for y := range h {
		for x := range w {
			img.Y[img.YOffset(x, y)] = uint8(x*7 + y*3 + x*y/5)
			ci := img.COffset(x, y)
			img.Cb[ci], img.Cr[ci] = uint8(x*2+y), uint8(255-x-y*2)
		}
	}
	return img
}

// The pixels on screen are the ones the Go-heap pipeline drew before
// the rasters moved off the heap (render.Resample, then the widget),
// hashed from it, for every fit: downscaled, upscaled at a fractional
// and an integer scale, and shown as decoded.
func TestRasterPixelsMatchTheHeapPipeline(t *testing.T) {
	src := photo(300, 170)
	for _, tc := range []struct {
		box  target
		fit  wallpaper.FitMode
		want string
	}{
		{target{w: 128, h: 72, scale: 1}, wallpaper.FitFill, "a2574480a11dfd5b58be80db66d82bf2d955e0b18f8c9569374aa099bb171e69"},
		{target{w: 128, h: 72, scale: 1}, wallpaper.FitFit, "aee5510f001996a48919da4b1a61878bcf25ec87c70a5db9869a3ac719014dc6"},
		{target{w: 128, h: 72, scale: 1}, wallpaper.FitCenter, "aee5510f001996a48919da4b1a61878bcf25ec87c70a5db9869a3ac719014dc6"},
		{target{w: 128, h: 72, scale: 1}, wallpaper.FitStretch, "aa03c09eda71cf79605a29aaf38fe207ae9c7b4281a19de6f0f12de81e61ad48"},
		{target{w: 96, h: 120, scale: 1.5}, wallpaper.FitFill, "b44a1df67a8d2c5eefc5c23c3dd9a5b7e0efcf8bc191f39a126746aed3ae27c7"},
		{target{w: 96, h: 120, scale: 1.5}, wallpaper.FitFit, "0e40ccc8b66f40136cab24df3be885d629c7fe3d4c6abb5a7ae5068e37b2a7e5"},
		{target{w: 96, h: 120, scale: 1.5}, wallpaper.FitCenter, "0e40ccc8b66f40136cab24df3be885d629c7fe3d4c6abb5a7ae5068e37b2a7e5"},
		{target{w: 96, h: 120, scale: 1.5}, wallpaper.FitStretch, "cc2b5bcf407f093d81531e40b4dfb3d4d96c9d6b3f09406b16b8700ee19796e2"},
		{target{w: 640, h: 360, scale: 2}, wallpaper.FitFill, "d0566fbe9390a87840cd2e793af0e773ff87d082727b9ca60ff93ca02965bf5f"},
		{target{w: 640, h: 360, scale: 2}, wallpaper.FitFit, "e0c2ebd9b559b8075b98cdc7342dff633d41b0a4f2674b0348a6376dd1db972e"},
		{target{w: 640, h: 360, scale: 2}, wallpaper.FitCenter, "904716d7c34418c1004b0f871a130b790f249cbb7c2154f402fb6937d6d43c9f"},
		{target{w: 640, h: 360, scale: 2}, wallpaper.FitStretch, "d246f14ef685efb962b6614638269199fa80224bce2d40f3fd25277f2b7d51e2"},
		{target{w: 640, h: 360, scale: 1}, wallpaper.FitFill, "d0566fbe9390a87840cd2e793af0e773ff87d082727b9ca60ff93ca02965bf5f"},
		{target{w: 640, h: 360, scale: 1}, wallpaper.FitFit, "e0c2ebd9b559b8075b98cdc7342dff633d41b0a4f2674b0348a6376dd1db972e"},
		{target{w: 640, h: 360, scale: 1}, wallpaper.FitCenter, "e221d1bde70863dcca61f9ce3e91109303f13ebed15a2a15daa819c7dd025d57"},
		{target{w: 640, h: 360, scale: 1}, wallpaper.FitStretch, "d246f14ef685efb962b6614638269199fa80224bce2d40f3fd25277f2b7d51e2"},
	} {
		var mem memory.Shared
		r, err := rasterize(&mem, src, tc.fit, tc.box, true)
		if err != nil {
			t.Fatal(err)
		}
		pic := widget.NewImage(r.image)
		pic.SetScale(r.scale)
		v := newView()
		v.show(&shown{pic: pic, size: r.size(), release: func() {}}, TransitionNone)
		data, _ := paintView(v, tc.box.w, tc.box.h)
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != tc.want {
			t.Errorf("%v on %+v: the screen changed", tc.fit, tc.box)
		}
		if err := mem.Trim(); err != nil || mem.Stats().Live != 1 {
			t.Errorf("%v on %+v: %d allocations, want the raster alone", tc.fit, tc.box, mem.Stats().Live)
		}
	}
}

// An output whose size is not reported yet shows the whole image, by
// the fit's widget policy, still off the heap.
func TestRasterUnknownOutputKeepsTheSourceSize(t *testing.T) {
	var mem memory.Shared
	r, err := rasterize(&mem, photo(30, 20), wallpaper.FitFit, target{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.size() != image.Pt(30, 20) || r.scale != widget.ImageFit || r.image.ID == 0 || mem.Stats().Live != 1 {
		t.Errorf("raster %v scale %v id %d, %d allocations", r.size(), r.scale, r.image.ID, mem.Stats().Live)
	}
}

// harness runs a shell on fake surfaces: work off the loop waits in a
// queue until run, decodes come from a map, and every renderer forget
// is recorded and checked.
type harness struct {
	t       *testing.T
	s       *Shell
	dir     string
	images  map[string]image.Image
	decodes map[string]int
	queued  []func()
	// forgotten are the IDs the renderer was told to forget, in order.
	forgotten []render.ImageID
	// closed counts each output's closed layers; onClosed is its
	// compositor-side close.
	closed   map[string]int
	onClosed map[string]func()
	ticks    map[string]func()
}

// newHarness starts a shell on monitors, all showing a.png, with the
// wallpaper transition kind lasting a millisecond.
func newHarness(t *testing.T, kind config.AnimationType, monitors ...string) *harness {
	t.Helper()
	h := &harness{
		t: t, dir: files(t, "a.png", "b.png", "c.png"),
		images: map[string]image.Image{}, decodes: map[string]int{},
		closed: map[string]int{}, onClosed: map[string]func(){}, ticks: map[string]func(){},
	}
	for _, n := range []string{"a.png", "b.png", "c.png"} {
		h.images[h.path(n)] = photo(120, 80)
	}
	cfg := config.Defaults()
	ms := uint32(1)
	cfg.Animations.Wallpaper.Enter, cfg.Animations.Wallpaper.EnterDuration = &kind, &ms
	cfg.Wallpaper.Wallpaper = h.path("a.png")
	svc := newSvc(monitors...)
	Bootstrap(svc, cfg.Wallpaper)
	ui := loop{
		newLayer: func(c app.LayerConfig) (func(), error) {
			name := c.Output.Name
			h.onClosed[name] = c.OnClosed
			return func() { h.closed[name]++ }, nil
		},
		every: func(_ time.Duration, fn func()) func() {
			name := h.tickingOutput()
			h.ticks[name] = fn
			return func() { delete(h.ticks, name) }
		},
	}
	h.s = newShell(ui, svc, cfg, rasterDeps{
		decode: func(path string) (image.Image, error) {
			h.decodes[path]++
			if img, ok := h.images[path]; ok {
				return img, nil
			}
			return nil, errors.New("no such image")
		},
		spawn:  func(fn func()) { h.queued = append(h.queued, fn) },
		invoke: func(fn func()) { fn() },
		forget: func(id render.ImageID) {
			if id == 0 {
				t.Error("forgot a raster without an ImageID")
			}
			for _, f := range h.forgotten {
				if f == id {
					t.Errorf("forgot raster %d twice", id)
				}
			}
			h.forgotten = append(h.forgotten, id)
		},
	})
	return h
}

func (h *harness) path(name string) string { return filepath.Join(h.dir, name) }

// tickingOutput is the output whose surface is starting a transition:
// the one whose view has an outgoing image without a ticker.
func (h *harness) tickingOutput() string {
	for name, surf := range h.s.surfaces {
		if _, ok := h.ticks[name]; !ok && surf.view.prev != nil {
			return name
		}
	}
	h.t.Fatal("a ticker started without a transition")
	return ""
}

// plug adds an output of w x h device pixels at scale 1.
func (h *harness) plug(name string, w, ht int) *app.Output {
	out := &app.Output{Name: name, ModeW: w, ModeH: ht, Scale: 1, LogicalW: int32(w), LogicalH: int32(ht)}
	h.s.AddOutput(out)
	return out
}

// run does the queued work off the loop until none is left.
func (h *harness) run() {
	for len(h.queued) > 0 {
		fn := h.queued[0]
		h.queued[0] = nil
		h.queued = h.queued[1:]
		fn()
	}
}

// show sets path on every monitor and lets it land.
func (h *harness) show(name string) {
	h.t.Helper()
	if err := h.s.svc.SetWallpaper(h.path(name), ""); err != nil {
		h.t.Fatal(err)
	}
	h.s.reconcile()
	h.run()
}

// finish plays every running transition to its end.
func (h *harness) finish() {
	time.Sleep(3 * time.Millisecond)
	for _, tick := range h.ticks {
		tick()
	}
}

func (h *harness) live() int { return h.s.rasters.mem.Stats().Live }

// current is the raster an output shows.
func (h *harness) current(name string) render.ImageID {
	h.t.Helper()
	surf := h.s.surfaces[name]
	if surf == nil || surf.view.cur == nil {
		h.t.Fatalf("%s shows nothing", name)
	}
	box, known := outputTarget(surf.out)
	r, ok := h.s.rasters.live[rasterKey{path: surf.path, fit: surf.fit, box: box, known: known}]
	if !ok {
		h.t.Fatalf("%s shows no live raster", name)
	}
	return r.image.ID
}

func (h *harness) wantLive(n int, why string) {
	h.t.Helper()
	if got := h.live(); got != n {
		h.t.Fatalf("%s: %d rasters allocated, want %d", why, got, n)
	}
}

func (h *harness) wantForgotten(why string, ids ...render.ImageID) {
	h.t.Helper()
	if fmt.Sprint(h.forgotten) != fmt.Sprint(ids) {
		h.t.Fatalf("%s: forgot %v, want %v", why, h.forgotten, ids)
	}
}

// The outgoing raster lives through its crossfade and is forgotten and
// freed when it ends, not before.
func TestCrossfadeFreesTheOutgoingRasterWhenItEnds(t *testing.T) {
	h := newHarness(t, config.AnimationFade, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	a := h.current("DP-1")
	h.wantLive(1, "a shown")
	h.show("b.png")
	h.wantLive(2, "mid-crossfade")
	h.wantForgotten("mid-crossfade")
	h.finish()
	h.wantLive(1, "crossfade done")
	h.wantForgotten("crossfade done", a)
}

// Without a transition the replaced raster goes at once.
func TestSwapWithoutTransitionFreesAtOnce(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	a := h.current("DP-1")
	h.show("b.png")
	h.wantLive(1, "b replaced a")
	h.wantForgotten("b replaced a", a)
	if len(h.ticks) != 0 {
		t.Error("a swap without a transition started a ticker")
	}
}

// A swap that overtakes a running crossfade drops the image still
// fading out; the one it fades from stays until its own fade ends.
func TestSwapMidCrossfadeFreesTheImageFadingOut(t *testing.T) {
	h := newHarness(t, config.AnimationFade, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	a := h.current("DP-1")
	h.show("b.png")
	b := h.current("DP-1")
	h.show("c.png")
	h.wantLive(2, "c fading in over b")
	h.wantForgotten("c overtook the a -> b fade", a)
	h.finish()
	h.wantLive(1, "c landed")
	h.wantForgotten("c landed", a, b)
}

// A decode a newer request overtook is released when it lands, never
// shown, and freed.
func TestOvertakenDecodeIsFreed(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	if err := h.s.svc.SetWallpaper(h.path("b.png"), ""); err != nil {
		t.Fatal(err)
	}
	h.s.reconcile()
	h.run()
	h.wantLive(1, "only b kept")
	if len(h.forgotten) != 1 {
		t.Fatalf("forgot %v, want the overtaken a", h.forgotten)
	}
	if got := h.current("DP-1"); got == h.forgotten[0] {
		t.Error("the overtaken raster is on screen")
	}
}

// Removing an output closes its surface and frees its raster.
func TestOutputRemovalFreesItsRaster(t *testing.T) {
	h := newHarness(t, config.AnimationFade, "DP-1", "DP-2")
	out := h.plug("DP-1", 64, 36)
	h.plug("DP-2", 32, 18)
	h.run()
	gone := h.current("DP-1")
	h.wantLive(2, "two sizes")
	h.s.RemoveOutput(out)
	h.wantLive(1, "DP-1 removed")
	h.wantForgotten("DP-1 removed", gone)
	if h.closed["DP-1"] != 1 || h.closed["DP-2"] != 0 {
		t.Errorf("closed layers %v, want DP-1's alone", h.closed)
	}
}

// A surface the compositor closes lets go of its raster too, including
// one mid-crossfade.
func TestCompositorCloseFreesTheRasters(t *testing.T) {
	h := newHarness(t, config.AnimationFade, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	h.show("b.png")
	h.wantLive(2, "mid-crossfade")
	h.onClosed["DP-1"]()
	h.wantLive(0, "closed by the compositor")
	if len(h.forgotten) != 2 || len(h.ticks) != 0 {
		t.Errorf("forgot %v with tickers %v left", h.forgotten, h.ticks)
	}
}

// Shutdown frees every raster, outgoing ones included, forgetting each
// once.
func TestShutdownFreesEveryRaster(t *testing.T) {
	h := newHarness(t, config.AnimationFade, "DP-1", "DP-2")
	h.plug("DP-1", 64, 36)
	h.plug("DP-2", 32, 18)
	h.run()
	h.show("b.png")
	h.wantLive(4, "both outputs mid-crossfade")
	h.s.dropSurfaces()
	h.wantLive(0, "shut down")
	if len(h.forgotten) != 4 || len(h.s.surfaces) != 0 || len(h.s.rasters.live) != 0 {
		t.Errorf("forgot %v; %d surfaces, %d rasters left", h.forgotten, len(h.s.surfaces), len(h.s.rasters.live))
	}
}

// Nothing is freed while a List recorded before the release is open:
// a frame still being played may read it.
func TestRasterOutlivesTheListsThatDrawIt(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	drawing := render.NewRecorder(1, 1, 1, 1).Recording()
	h.show("b.png")
	if len(h.forgotten) != 1 {
		t.Fatalf("forgot %v, want a", h.forgotten)
	}
	h.wantLive(2, "a List from before the swap is open")
	drawing.End()
	h.wantLive(1, "the List ended")
}

// Outputs showing the same file at the same size share one raster,
// decoded once, which lives until its last user ends; a different size
// is its own raster from the same decode.
func TestSharedRasterLivesUntilItsLastUser(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1", "DP-2", "HDMI-1")
	one := h.plug("DP-1", 64, 36)
	two := h.plug("DP-2", 64, 36)
	h.plug("HDMI-1", 32, 18)
	h.run()
	if h.current("DP-1") != h.current("DP-2") || h.current("DP-1") == h.current("HDMI-1") {
		t.Fatal("the same size does not share, or another size does")
	}
	h.wantLive(2, "one raster per size")
	if n := h.decodes[h.path("a.png")]; n != 1 {
		t.Errorf("a.png decoded %d times, want once for every size", n)
	}
	h.s.RemoveOutput(one)
	h.wantLive(2, "DP-2 still shows the shared raster")
	h.wantForgotten("DP-2 still shows the shared raster")
	shared := h.current("DP-2")
	h.s.RemoveOutput(two)
	h.wantLive(1, "the last user is gone")
	h.wantForgotten("the last user is gone", shared)
}

// A later output asking for a live raster gets it without a decode.
func TestLiveRasterIsSharedWithoutADecode(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1", "DP-2")
	h.plug("DP-1", 64, 36)
	h.run()
	h.plug("DP-2", 64, 36)
	if len(h.queued) != 0 || h.decodes[h.path("a.png")] != 1 {
		t.Fatalf("a live raster was made again: %d decodes", h.decodes[h.path("a.png")])
	}
	if h.current("DP-1") != h.current("DP-2") {
		t.Error("the second output got another raster")
	}
	h.wantLive(1, "shared")
}

// A file that does not decode shows nothing new and leaves no raster
// or waiter behind.
func TestUndecodableFileLeavesNothing(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	delete(h.images, h.path("b.png"))
	h.show("b.png")
	h.wantLive(1, "a kept")
	h.wantForgotten("a kept")
	if len(h.s.rasters.waiting) != 0 || len(h.s.rasters.decoding) != 0 {
		t.Errorf("left %d waiting keys and %d decodes", len(h.s.rasters.waiting), len(h.s.rasters.decoding))
	}
}

// Releasing a raster nobody holds is a bug, not a no-op.
func TestReleaseOfAnUnheldRasterPanics(t *testing.T) {
	rs := newRasters(rasterDeps{})
	defer func() {
		if recover() == nil {
			t.Error("a double release passed")
		}
	}()
	rs.release(&raster{})
}
