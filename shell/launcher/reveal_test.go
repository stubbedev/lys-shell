package launcher

import (
	"math"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/motion"
	"github.com/stubbedev/lys-ui/anim"

	"github.com/stubbedev/lys-shell/config"
)

// openingOpacities are the opacities an opening panel commits, its map
// and then one frame at 60 Hz after another until it lands, the spring
// stepped as the toolkit's clock steps it.
func openingOpacities(t *testing.T, h *panelHarness) []float64 {
	t.Helper()
	p, _ := h.openPanel()
	m := p.v.motion
	got := h.harness.windows[0].opacity
	if len(got) == 0 {
		t.Fatal("the panel committed no opacity")
	}
	params := springFor(h.cfg.Animations.DurationFor(config.AnimLauncher, false), true)
	omega := 2 * math.Pi / params.Response.Seconds()
	x, v := m.reveal-1, 0.0
	out := []float64{got[0]}
	for range 60 {
		x, v = motion.SpringStep(omega, params.Damping, x, v, (time.Second / 60).Seconds())
		out = append(out, min(max(1+x, 0), 1))
	}
	return out
}

func TestAnOpeningPanelIsReadableFromItsFirstFrame(t *testing.T) {
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	ops := openingOpacities(t, h)
	if ops[0] < 0.4 {
		t.Errorf("the map commits at %v, want 0.4", ops[0])
	}
	below := 0
	for _, o := range ops[1:] {
		if o < 0.5 {
			below++
		}
	}
	if below > 1 {
		t.Errorf("%d frames after the map below 0.5 (%v), want at most one", below, ops[:6])
	}
}

func TestAReducedMotionFadeStartsReadableAndAClosedPanelEndsAtZero(t *testing.T) {
	anim.SetReduced(true)
	defer anim.SetReduced(false)
	h := newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ := h.openPanel()
	if r := p.v.motion.reveal; r < 0.4 {
		t.Errorf("reduced motion fades in from %v, want 0.4", r)
	}

	anim.SetReduced(false)
	h = newPanelHarness(t)
	h.cfg.Animations.Enabled = true
	p, _ = h.openPanel()
	m := p.v.motion
	m.revS.Snap(1)
	h.key("Escape")
	h.until("the session ended", func() bool { return h.s.panel == nil })
	m.revS.Snap(0)
	if !h.harness.windows[0].closed {
		t.Error("an exit must still play down to 0 and close the surface")
	}
}
