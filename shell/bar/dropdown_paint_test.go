package bar

import (
	"math"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The dropdown panels paint from the stylesheet: a migrated label
// carries no programmatic color and the cascade inks it, the flat
// panel buttons carry no Go hover fills, and the panel icons follow
// the cascade color instead of a pinned tint.
func TestDropdownPanelsPaintFromTheStylesheet(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	v := audioDropdown(ctx).(*audioView)
	ctx.Theme.Attach(v)
	sz := v.Measure(widget.Constraints{Max: widget.Size{W: 400, H: 600}})
	v.Arrange(render.Rect{W: sz.W, H: 600})

	name := treetest.WithClass(v.output.item, "title")[0].(*widget.Label)
	if got := name.Color(); got != 0 {
		t.Errorf("the device name carries a programmatic color %#08x", uint32(got))
	}
	if v.output.mute.BgHover != 0 || v.output.mute.BgPressed != 0 {
		t.Error("the mute button carries Go hover fills")
	}
	if got := v.output.icon.Tint(); got != 0 {
		t.Errorf("the device icon carries a pinned tint %#08x", uint32(got))
	}

	// The sheet demonstrably drives the ink: the same uncolored label
	// paints differently under the panel's sheet (the cascade's
	// fg-default) than alone (the widget theme's fallback).
	name.SetText("Speakers")
	withSheet := paintLabel(name)
	bare := widget.NewLabel(ctx.Font, ctx.Style.labelPx, "Speakers", 0)
	withoutSheet := paintLabel(bare)
	if string(withSheet) == string(withoutSheet) {
		t.Error("the stylesheet did not reach the panel tree: the label painted the fallback ink")
	}
}

// paintLabel paints one label into a fresh buffer.
func paintLabel(l *widget.Label) []byte {
	sz := l.Measure(widget.Constraints{Max: widget.Size{W: 200, H: 40}})
	l.Arrange(render.Rect{W: sz.W + 8, H: 24})
	data := make([]byte, render.Stride(200)*40)
	cv := render.NewScaled(data, render.Stride(200), 200, 40, 1, 1)
	cv.Clear(cv.Rect(), 0)
	l.Paint(cv)
	return data
}

// countColor counts the pixels col accepts.
func countColor(data []byte, col func(render.Color) bool) int {
	n := 0
	for i := 0; i+3 < len(data); i += 4 {
		if col(render.ColorFromBytes(data[i:])) {
			n++
		}
	}
	return n
}

// The battery gauge is a levelbar: the trough and the filled block
// paint from the levelbar.battery-gauge rules, the variant class
// recolors the block, and no Go color survives.
func TestBatteryGaugePaintsFromTheLevelbarRules(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	// The tree pass first: the gauge's element resolves on parenting.
	sz0 := v.Measure(widget.Constraints{Max: widget.Size{W: 400, H: 600}})
	v.Arrange(render.Rect{W: sz0.W, H: 600})
	ctx.Theme.Attach(v)
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	v.gauge.SetValue(0.5)
	v.gauge.RemoveClass(batteryVariants...)
	v.gauge.AddClass("good")
	sz := v.gauge.Measure(widget.Constraints{Max: widget.Size{W: 200, H: 40}})
	v.gauge.Arrange(render.Rect{X: 0, Y: 0, W: 200, H: sz.H})

	palette := ctx.Theme.RenderPalette()
	match := func(want render.Color) func(render.Color) bool {
		return func(c render.Color) bool {
			return max(int(c.R())-int(want.R()), int(want.R())-int(c.R())) < 24 &&
				max(int(c.G())-int(want.G()), int(want.G())-int(c.G())) < 24
		}
	}
	paint := func() []byte {
		data := make([]byte, render.Stride(200)*40)
		cv := render.NewScaled(data, render.Stride(200), 200, 40, 1, 1)
		cv.Clear(cv.Rect(), 0)
		v.gauge.Paint(cv)
		return data
	}
	// The good block: the success color, half the bar.
	data := paint()
	a, b := spanOf(data, sz.H, match(palette.Green))
	if a < 0 || (b-a) < 90 || a > 10 {
		t.Errorf("the good block spans %d..%d, want the left half", a, b)
	}
	if n := countColor(data, match(palette.Red)); n != 0 {
		t.Error("the good gauge shows the error color")
	}
	// The crit class recolors it without a Go color.
	v.gauge.RemoveClass("good")
	v.gauge.AddClass("crit")
	a, b = spanOf(paint(), sz.H, match(palette.Red))
	if a < 0 || (b-a) < 90 {
		t.Errorf("the crit block spans %d..%d, want the left half", a, b)
	}
	if v.gauge.Value() != 0.5 {
		t.Errorf("the gauge value moved to %v", v.gauge.Value())
	}
}

// The dashboard rings are lys-ui's ring in the palette's colors: the
// threshold variant recolors the sweep, and dropping the variant
// returns it to the accent.
func TestStatRingPaintsTheThresholdColor(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	defer restore()
	ctx := styledContext(t, config.Defaults())
	var r *statRing
	fresh := func() {
		r = newStatRing(widget.NewLabel(ctx.Font, 12, "", 0))
		ctx.Theme.Attach(r.ring)
	}
	fresh()
	paintRing := func() []byte {
		sz := r.ring.Measure(widget.Constraints{Max: widget.Size{W: 100, H: 100}})
		r.ring.Arrange(render.Rect{W: sz.W, H: sz.H})
		data := make([]byte, render.Stride(100)*100)
		cv := render.NewScaled(data, render.Stride(100), 100, 100, 1, 1)
		cv.Clear(cv.Rect(), 0)
		r.ring.Paint(cv)
		return data
	}
	green := func(c render.Color) bool { return c.A() > 200 && c.G() > 150 && c.R() < 150 }
	red := func(c render.Color) bool { return c.A() > 200 && c.R() > 150 && c.G() < 150 }
	r.set(0.75, "75%", "success")
	if n := countColor(paintRing(), green); n == 0 {
		t.Error("the success ring painted no green")
	}
	r.set(0.75, "75%", "error")
	if n := countColor(paintRing(), red); n == 0 {
		t.Error("the error ring painted no red")
	}
	fresh()
	r.set(0.75, "75%", "success")
	r.set(0.75, "75%", "")
	if n := countColor(paintRing(), green); n != 0 {
		t.Errorf("the accent ring still painted %d green pixels", n)
	}
}

// spanOf walks one painted row set for the block's extent.
func spanOf(data []byte, h int, want func(render.Color) bool) (minX, maxX int) {
	minX, maxX = 1<<30, -1
	for y := range h {
		for x := range 200 {
			if want(render.ColorFromBytes(data[(y*200+x)*4:])) {
				minX = min(minX, x)
				maxX = max(maxX, x)
			}
		}
	}
	return minX, maxX
}

// The stylesheet sizes the dashboard ring to 4rem, past lys-ui's
// default diameter, and its value sits on the ring's centre.
func TestStatRingTakesTheStylesheetDiameter(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	bare := newStatRing(widget.NewLabel(ctx.Font, 12, "", 0))
	if s := widget.MeasureAsChild(bare.ring, widget.Constraints{Max: widget.Size{W: 400, H: 400}}); s.H >= 48 {
		t.Fatalf("a ring outside the stylesheet measures %v: its size is not the stylesheet's", s)
	}
	r := newStatRing(widget.NewLabel(ctx.Font, 12, "", 0))
	ctx.Theme.Attach(r.ring)
	r.set(0.5, "50%", "")
	sz := widget.MeasureAsChild(r.ring, widget.Constraints{Max: widget.Size{W: 400, H: 400}})
	if sz.W < 60 || sz.W != sz.H {
		t.Fatalf("the ring measures %v, want the square 4rem .stat-ring", sz)
	}
	widget.ArrangeAsChild(r.ring, render.Rect{W: sz.W, H: sz.H})
	lb, rb := r.label.Bounds(), r.ring.Bounds()
	if dx, dy := (lb.X+lb.W/2)-(rb.X+rb.W/2), (lb.Y+lb.H/2)-(rb.Y+rb.H/2); dx < -1 || dx > 1 || dy < -1 || dy > 1 {
		t.Errorf("the label %+v sits off the ring's center %+v", lb, rb)
	}
}

// A ring's glyph sits inside the track: its corners clear the track's
// inner edge by at least half lys-ui's default 4px stroke.
func TestGlyphRingGlyphClearsTheTrack(t *testing.T) {
	const stroke = 4
	ctx := styledContext(t, config.Defaults())
	r := newGlyphRing("ld-wifi-symbolic", 65)
	ctx.Theme.Attach(r)
	sz := widget.MeasureAsChild(r, widget.Constraints{Max: widget.Size{W: 400, H: 400}})
	widget.ArrangeAsChild(r, render.Rect{W: sz.W, H: sz.H})
	ring, glyph := r.Bounds(), r.glyph.Bounds()
	if glyph.W == 0 || glyph.H == 0 {
		t.Fatalf("the glyph is %dx%d", glyph.W, glyph.H)
	}
	hole := float64(ring.W)/2 - stroke
	if corner := math.Hypot(float64(glyph.W), float64(glyph.H)) / 2; corner > hole-stroke/2 {
		t.Errorf("a %dx%d glyph's corners reach %.1fpx out in a %dpx ring whose track starts at %.1fpx", glyph.W, glyph.H, corner, ring.W, hole)
	}
}

// A header action keeps its own height on the row's middle: a switch
// beside the taller icon button stays a pill instead of stretching to
// the row and drawing its knob as a ring.
func TestDropdownHeaderActionsKeepTheirHeight(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	sw := widget.NewSwitch(true)
	scan := dropdownButton(widget.NewThemeIcon("tb-refresh-symbolic", 16), nil, css.Ghost, css.Icon)
	header := dropdownHeader(ctx.Font, "ld-bluetooth-symbolic", "Bluetooth", scan, sw)
	ctx.Theme.Attach(header)
	natural := widget.MeasureAsChild(sw, widget.Constraints{Max: widget.Size{W: 400, H: 400}})
	sz := header.Measure(widget.Constraints{Max: widget.Size{W: 400, H: 400}})
	header.Arrange(render.Rect{W: 400, H: sz.H})
	if scan.Bounds().H <= natural.H {
		t.Fatalf("the icon button (%d) is no taller than the switch (%d): nothing to stretch against", scan.Bounds().H, natural.H)
	}
	b := sw.Bounds()
	if b.H != natural.H {
		t.Errorf("the switch spans %d of the %d row, want its own %d", b.H, scan.Bounds().H, natural.H)
	}
	if mid, rowMid := b.Y+b.H/2, scan.Bounds().Y+scan.Bounds().H/2; mid < rowMid-1 || mid > rowMid+1 {
		t.Errorf("the switch's middle %d is off the row's %d", mid, rowMid)
	}
}
