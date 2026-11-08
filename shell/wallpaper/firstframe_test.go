package wallpaper

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/render"
)

type firstFrameRun struct {
	*harness
	done int
}

func startFirstFrame(t *testing.T, monitors ...string) *firstFrameRun {
	t.Helper()
	r := &firstFrameRun{harness: newHarness(t, config.AnimationNone, monitors...)}
	for _, name := range monitors {
		r.s.outputs[name] = testOutput(name, 64, 36)
	}
	r.s.awaitFirstFrame(func() { r.done++ })
	return r
}

func (h *harness) liveBuiltin(name string) render.ImageID {
	h.t.Helper()
	surf := h.s.surfaces[name]
	box, known := outputTarget(surf.out)
	raster, ok := h.s.rasters.live[rasterKey{path: builtinWallpaper, fit: surf.fit, box: box, known: known}]
	if !ok {
		h.t.Fatalf("%s has no live built-in raster", name)
	}
	return raster.image.ID
}

func (r *firstFrameRun) wantDone(n int, why string) {
	r.t.Helper()
	if r.done != n {
		r.t.Errorf("%s: the bars were released %d times, want %d", why, r.done, n)
	}
}

func TestNoLayerOpensBeforeItsImage(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	if r.opened["DP-1"] != 0 {
		t.Fatal("a layer opened with no image to show")
	}
	r.wantDone(0, "decoding")
	r.run()
	if r.opened["DP-1"] != 1 {
		t.Fatalf("opened %d layers once the image landed, want 1", r.opened["DP-1"])
	}
	if r.s.surfaces["DP-1"].view.cur == nil {
		t.Fatal("the layer opened without its image")
	}
}

func TestBarsWaitForEveryOutputsFirstFrame(t *testing.T) {
	r := startFirstFrame(t, "DP-1", "DP-2")
	r.run()
	r.wantDone(0, "mapped, nothing presented")
	r.onPresented["DP-1"]()
	r.wantDone(0, "one of two presented")
	r.onPresented["DP-2"]()
	r.wantDone(1, "both presented")
	if r.limit != nil {
		t.Error("the limit still runs after every output presented")
	}
	r.onPresented["DP-1"]()
	r.wantDone(1, "presented again")
}

func TestNoOutputsReleasesTheBarsAtOnce(t *testing.T) {
	r := startFirstFrame(t)
	r.wantDone(1, "nothing to wait for")
}

func TestSlowImageIsBoundedByTheBuiltinWallpaper(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	decode := r.queued
	r.queued = nil
	logs := captureLog(t)
	r.limit()
	r.run()
	if !strings.Contains(logs.String(), "not ready after") {
		t.Errorf("the limit logged %q", logs.String())
	}
	if r.opened["DP-1"] != 1 {
		t.Fatalf("opened %d layers past the limit, want the built-in one", r.opened["DP-1"])
	}
	builtin := r.liveBuiltin("DP-1")
	r.wantDone(0, "built-in not presented yet")
	r.onPresented["DP-1"]()
	r.wantDone(1, "built-in presented")
	r.queued = decode
	r.run()
	if r.current("DP-1") == builtin {
		t.Error("the image never replaced the built-in wallpaper")
	}
	if r.opened["DP-1"] != 1 {
		t.Errorf("the image opened another layer: %d", r.opened["DP-1"])
	}
	r.wantForgotten("the built-in wallpaper gave way", builtin)
	r.wantLive(1, "the image alone")
	r.wantDone(1, "the image landed")
}

func TestImageLandingBeforeTheBuiltinKeepsItsPlace(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	decode := r.queued
	r.queued = nil
	r.limit()
	builtin := r.queued
	r.queued = decode
	r.run()
	image := r.current("DP-1")
	r.queued = builtin
	r.run()
	if r.current("DP-1") != image {
		t.Error("the built-in wallpaper replaced the image")
	}
	r.wantLive(1, "the built-in raster was freed")
}

func TestUndecodableImageShowsTheBuiltinWithoutWaiting(t *testing.T) {
	r := newHarness(t, config.AnimationNone, "DP-1")
	delete(r.decodable, r.path("a.png"))
	done := 0
	r.s.outputs["DP-1"] = testOutput("DP-1", 64, 36)
	r.s.awaitFirstFrame(func() { done++ })
	r.run()
	if r.opened["DP-1"] != 1 || r.s.surfaces["DP-1"].view.cur == nil {
		t.Fatal("an undecodable image left the output empty")
	}
	r.onPresented["DP-1"]()
	if done != 1 {
		t.Errorf("the bars were released %d times, want 1", done)
	}
}

func TestPresentationThatNeverComesIsBounded(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	r.run()
	logs := captureLog(t)
	r.limit()
	r.wantDone(0, "first limit")
	r.limit()
	r.wantDone(1, "second limit")
	if !strings.Contains(logs.String(), "without waiting") {
		t.Errorf("giving up logged %q", logs.String())
	}
}

func TestOutputRemovedWhileWaitingReleasesTheBars(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	r.s.RemoveOutput(r.s.outputs["DP-1"])
	r.wantDone(1, "the only output left")
	r.run()
	r.wantLive(0, "nothing draws the image")
}

func TestCompositorClosingTheLayerReleasesTheBars(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	r.run()
	r.onClosed["DP-1"]()
	r.wantDone(1, "the layer closed")
}

func TestRefusedLayerReleasesTheBars(t *testing.T) {
	r := startFirstFrame(t, "DP-1")
	r.refuseLayers = true
	r.run()
	r.wantDone(1, "the layer refused")
	r.wantLive(0, "nothing draws the image")
	if _, ok := r.s.surfaces["DP-1"]; ok {
		t.Error("the refused surface stayed")
	}
}
