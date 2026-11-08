package wallpaper

import (
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/wallpaper/extract"
	"github.com/stubbedev/lys-desktop/styling/material"
)

func (h *harness) sample(monitor string) ([]material.ARGB, error) {
	h.t.Helper()
	var (
		pixels []material.ARGB
		err    error
		done   bool
	)
	h.s.sampleOnLoop(monitor, func(p []material.ARGB, e error) { pixels, err, done = p, e, true })
	h.run()
	if !done {
		h.t.Fatal("sampling never finished")
	}
	return pixels, err
}

func TestThemingReadsTheShownRasterWithoutOpeningTheFile(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	h.run()
	decodes := h.decodes[h.path("a.png")]
	pixels, err := h.sample("DP-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) != 64*36 {
		t.Errorf("%d samples of a 64x36 raster, want every pixel", len(pixels))
	}
	if got := h.decodes[h.path("a.png")]; got != decodes {
		t.Errorf("sampling decoded the wallpaper %d more times, want 0", got-decodes)
	}
	h.wantLive(1, "after sampling")
	h.wantForgotten("after sampling")
}

func TestThemingJoinsADecodeInFlight(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 64, 36)
	var pixels []material.ARGB
	h.s.sampleOnLoop("DP-1", func(p []material.ARGB, err error) {
		if err != nil {
			t.Error(err)
		}
		pixels = p
	})
	h.run()
	if got := h.decodes[h.path("a.png")]; got != 1 || len(pixels) == 0 {
		t.Errorf("%d decodes and %d samples, want one decode shared by surface and theming", got, len(pixels))
	}
	h.wantLive(1, "surface and theming share one raster")
}

func TestThemingSampleIsBoundedForALargeOutput(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.plug("DP-1", 1280, 720)
	h.run()
	pixels, err := h.sample("DP-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) != extract.SampleSide*72 {
		t.Errorf("%d samples of a 1280x720 raster, want %dx72", len(pixels), extract.SampleSide)
	}
}

func TestThemingAnUnknownMonitorIsAnError(t *testing.T) {
	h := newHarness(t, config.AnimationNone, "DP-1")
	if _, err := h.sample("HDMI-9"); err == nil {
		t.Error("sampled a monitor without an output")
	}
}
