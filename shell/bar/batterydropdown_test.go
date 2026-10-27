package bar

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestBatteryFormatters(t *testing.T) {
	for in, want := range map[float64]string{8.2: "8.2W", 45: "45W", 9.96: "10.0W", 10: "10W"} {
		if got := formatWatts(in); got != want {
			t.Errorf("formatWatts(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{7.2: "7.2 Wh", 60: "60 Wh"} {
		if got := formatWattHours(in); got != want {
			t.Errorf("formatWattHours(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{92: "92%", 0: "--", -1: "--"} {
		if got := healthValue(in); got != want {
			t.Errorf("healthValue(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[uint32]uint32{80: 75, 5: 0, 3: 0} {
		if got := resumeThreshold(in); got != want {
			t.Errorf("resumeThreshold(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestBatteryClasses(t *testing.T) {
	crit := upower.Device{Percentage: 12, WarningLevel: upower.WarningCritical}
	low := upower.Device{Percentage: 15, WarningLevel: upower.WarningNone}
	normal := upower.Device{Percentage: 76, WarningLevel: upower.WarningNone}
	if got := batteryLevelClass(crit, "good"); got != "crit" {
		t.Errorf("gauge critical = %q", got)
	}
	if got := batteryLevelClass(low, "good"); got != "warn" {
		t.Errorf("gauge low = %q", got)
	}
	if got := batteryLevelClass(normal, "good"); got != "good" {
		t.Errorf("gauge normal = %q", got)
	}
	if got := batteryLevelClass(normal, ""); got != "" {
		t.Errorf("hero normal = %q, want none", got)
	}
	if got := heroStateClass(upower.Device{State: upower.StateCharging}); got != "good" {
		t.Errorf("hero charging = %q", got)
	}
	if got := heroStateClass(upower.Device{State: upower.StateCharging, WarningLevel: upower.WarningAction}); got != "crit" {
		t.Errorf("hero critical beats charging: %q", got)
	}
	if got := heroStateClass(upower.Device{State: upower.StateDischarging}); got != "" {
		t.Errorf("hero discharging = %q, want none", got)
	}
	for in, want := range map[float64]string{92: "good", 80: "good", 68: "fair", 50: "fair", 30: "poor", 0: "unknown"} {
		if got := healthClass(in); got != want {
			t.Errorf("healthClass(%v) = %q, want %q", in, got, want)
		}
	}
	if got := batteryStateLabel(upower.Device{State: upower.StateCharging, WarningLevel: upower.WarningLow}); got != i18n.T("dropdown-battery-critical") {
		t.Errorf("low while charging = %q, want critical", got)
	}
}

func presentBattery() upower.Device {
	return upower.Device{
		Percentage: 64, State: upower.StateDischarging, IsPresent: true,
		TimeToEmpty: 2 * time.Hour, EnergyRate: 8.2, EnergyFull: 55, Capacity: 91,
		WarningLevel: upower.WarningNone, ChargeEndThreshold: 80,
	}
}

func TestBatteryDropdownEmptyWithoutBattery(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	if v.body.Visible() != "empty" {
		t.Errorf("no service: page %q", v.body.Visible())
	}
	ctx.Battery = newFakeBattery(upower.Device{Percentage: 50, State: upower.StateDischarging})
	v = batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	if v.body.Visible() != "empty" {
		t.Errorf("IsPresent false: page %q, want empty", v.body.Visible())
	}
}

// styledRoot is a view root the stylesheet can attach to.
type styledRoot interface {
	widget.Widget
	AttachStylesheet(*widget.Stylesheet)
}

// batteryArrangeStyled attaches the bar stylesheet and lays the
// dropdown out at w, so the cascade's margins and gaps take part in
// the bounds the assertions read. Animations run instantly, as the
// paint tests need.
func batteryArrangeStyled(t *testing.T, ctx ModuleContext, v styledRoot, w int) {
	t.Helper()
	// The tree pass first: elements resolve on parenting.
	sz := widget.SizeWithin(v, widget.Size{W: w, H: 1 << 14})
	v.Arrange(render.Rect{W: w, H: max(sz.H, 1)})
	ctx.Theme.Attach(v)
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	sz = widget.SizeWithin(v, widget.Size{W: w, H: 1 << 14})
	v.Arrange(render.Rect{W: max(sz.W, 1), H: max(sz.H, 1)})
}

// alignedBounds reads a walked widget's arranged rect.
func alignedBounds(t *testing.T, w widget.Widget) render.Rect {
	t.Helper()
	b, ok := w.(interface{ Bounds() render.Rect })
	if !ok {
		t.Fatalf("%T reports no bounds", w)
	}
	return b.Bounds()
}

// alignDelta is |a-b| for midpoint comparisons.
func alignDelta(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// batterySlotHas reports whether slot contains w.
func batterySlotHas(slot, w widget.Widget) bool {
	found := false
	treetest.Walk(slot, func(k widget.Widget) {
		if k == w {
			found = true
		}
	})
	return found
}

// The details row is a CenterBox: the capacity column's midpoint is
// the row's own, the side slots hug the edges, and the centering is
// geometric — the capacity labels keep the default alignment.
func TestBatteryDetailsCenterTheCapacityColumn(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	src := newFakeBattery(presentBattery())
	ctx.Battery = src
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	batteryArrangeStyled(t, ctx, v, 320)

	boxed := treetest.WithClass(v, "battery-details")
	if len(boxed) != 1 {
		t.Fatalf("battery-details widgets = %d", len(boxed))
	}
	details, ok := boxed[0].(*widget.CenterBox)
	if !ok {
		t.Fatalf("battery-details = %T, want *widget.CenterBox", boxed[0])
	}
	slots := details.Children()
	if len(slots) != 3 {
		t.Fatalf("slots = %d, want start/center/end", len(slots))
	}
	if !batterySlotHas(slots[1], v.capacityValue) || !batterySlotHas(slots[0], v.drawValue) {
		t.Fatal("the center slot is not the capacity detail")
	}
	db, cb, eb := alignedBounds(t, slots[0]), alignedBounds(t, slots[1]), alignedBounds(t, slots[2])
	hb := details.Bounds()
	mid := func(r render.Rect) int { return r.X + r.W/2 }
	if d := alignDelta(mid(cb), mid(hb)); d > 1 {
		t.Errorf("capacity column midpoint off by %d (%d vs %d)", d, mid(cb), mid(hb))
	}
	// The side slots hug the content box's edges: their inset from the
	// margin box is the CenterBox's own CSS box, symmetric on both
	// sides.
	leftGap, rightGap := db.X-hb.X, hb.X+hb.W-(eb.X+eb.W)
	if leftGap < 0 || rightGap < 0 || alignDelta(leftGap, rightGap) > 1 {
		t.Errorf("side slots not symmetric in the content box: start %d vs %d, end %d vs %d",
			db.X, hb.X, eb.X+eb.W, hb.X+hb.W)
	}
	if v.capacityValue.Alignment() != render.AlignStart || v.capacityLabel.Alignment() != render.AlignStart {
		t.Error("the capacity labels are aligned; the centering must be the CenterBox's")
	}
}

// The health indicator is an empty classed box — no text glyph child —
// sized by its request; the CSS paints the circle (round clip, variant
// fill).
func TestBatteryHealthDotIsAClassedBoxTheCSSPaints(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	src := newFakeBattery(presentBattery()) // capacity 91 → good
	ctx.Battery = src
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()

	dot, ok := any(v.healthDot).(*widget.Box)
	if !ok {
		t.Fatalf("health dot = %T, want a bare box", v.healthDot)
	}
	if n := len(dot.Children()); n != 0 {
		t.Errorf("health dot children = %d, want none", n)
	}
	for _, l := range treetest.All[*widget.Label](v) {
		if strings.Contains(l.Text(), "●") {
			t.Errorf("the dot is drawn as text %q", l.Text())
		}
	}
	if !dot.HasClass("health-dot") || !dot.HasClass("good") {
		t.Errorf("dot classes = %v, want health-dot + good", dot.Classes())
	}

	// The size request gives the empty box extent, and the stylesheet's
	// variant fills it with the status color, clipped round.
	batteryArrangeStyled(t, ctx, v, 320)
	if sz := widget.SizeWithinAsChild(dot, widget.Size{W: 64, H: 64}); sz != (widget.Size{W: statusDotPx, H: statusDotPx}) {
		t.Errorf("dot measure = %+v, want the %dpx request", sz, statusDotPx)
	}
	palette := ctx.Theme.RenderPalette()
	fillOf := func(c render.Color) bool { return c.A() > 200 }
	greenish := func(c render.Color) bool { return fillOf(c) && c.G() > 150 && c.R() < 150 }
	reddish := func(c render.Color) bool { return fillOf(c) && c.R() > 150 && c.G() < 150 }
	paint := func() []byte {
		b := dot.Bounds()
		w, h := b.X+b.W+2, b.Y+b.H+2
		data := make([]byte, render.Stride(w)*h)
		cv := render.NewScaled(data, render.Stride(w), w, h, 1, 1)
		cv.Clear(cv.Rect(), 0)
		dot.Paint(cv)
		return data
	}
	width := func() int { b := dot.Bounds(); return b.X + b.W + 2 }
	b := dot.Bounds()
	if c := pixelAt(paint(), width(), b.X+b.W/2, b.Y+b.H/2); !greenish(c) {
		t.Errorf("the good dot's center = %#08x, want the success fill %#08x-ish", uint32(c), uint32(palette.Green))
	}
	if c := pixelAt(paint(), width(), b.X, b.Y); c.A() > 64 {
		t.Errorf("the dot's corner = %#08x, want the round clip", uint32(c))
	}

	// The variant class drives the fill: poor loses the success color.
	v.setVariant(dot, "poor")
	data := paint()
	if n := countColor(data, greenish); n != 0 {
		t.Error("the poor dot kept the success fill")
	}
	if n := countColor(data, reddish); n == 0 {
		t.Error("the poor dot shows no error fill")
	}
}

// The hero sets its percentage apart from the meta column, which sits
// on the row's middle (valign Center), and the gauge sits apart from
// the details: the group's spacing, lys-ui's.
func TestBatteryHeroAndGaugeKeepTheirGaps(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	src := newFakeBattery(presentBattery())
	ctx.Battery = src
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	batteryArrangeStyled(t, ctx, v, 320)

	pct, metaBox := v.heroPct.Bounds(), treetest.WithClass(v, "battery-hero-meta")[0]
	meta := alignedBounds(t, metaBox)
	if pct.X+pct.W >= meta.X {
		t.Errorf("no gap between the hero pct and the meta: %v then %v", pct, meta)
	}
	gauge, details := v.gauge.Bounds(), alignedBounds(t, treetest.WithClass(v, "battery-details")[0])
	if gap := details.Y - (gauge.Y + gauge.H); gap != widget.Current().Spacing {
		t.Errorf("gauge %v then details at %d: a %dpx gap, want the theme's spacing", gauge, details.Y, gap)
	}
	hero := alignedBounds(t, treetest.WithClass(v, "battery-hero")[0])
	if d := alignDelta(meta.Y+meta.H/2, hero.Y+hero.H/2); d > 1 {
		t.Errorf("hero meta midpoint off by %d", d)
	}
}

// set_homogeneous: the three segments measure equal though their
// labels differ, the seg equalizing them — no hand-set min-widths.
func TestBatteryProfileSegmentsEqualWidth(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	big := widget.Size{W: 1 << 14, H: 1 << 14}

	// The segments' own naturals differ (the precondition: there is
	// something to equalize).
	naturals := map[string]int{}
	for _, p := range batteryProfiles {
		naturals[p.name] = widget.SizeWithin(v.profileButtons[p.name], big).W
	}
	lo, hi := 1<<30, 0
	for _, w := range naturals {
		lo, hi = min(lo, w), max(hi, w)
	}
	if lo == hi {
		t.Fatal("the segments measure equal; nothing to equalize")
	}
	var seg *widget.Box
	for _, b := range treetest.WithClass(v, "profile-seg") {
		seg = b.(*widget.Box)
	}
	if seg == nil || !seg.Homogeneous() {
		t.Fatal("the segment row is missing or not homogeneous")
	}
	for _, p := range batteryProfiles {
		b := v.profileButtons[p.name]
		if strings.Contains(b.InlineStyle(), "min-width") {
			t.Errorf("%s carries a hand-set min-width floor: %q", p.name, b.InlineStyle())
		}
	}
	// The seg lays the segments out equal: one shared width, the
	// widest child's, give or take the remainder pixel GTK hands to
	// the first children so the row ends flush.
	arrangeDropdown(t, seg, hi*3+8, 60)
	narrow, wide := 1<<30, 0
	for _, p := range batteryProfiles {
		w := v.profileButtons[p.name].Bounds().W
		narrow, wide = min(narrow, w), max(wide, w)
	}
	if wide-narrow > 1 {
		t.Errorf("the segments span %d..%dpx, want one shared size within the remainder pixel", narrow, wide)
	}
	if narrow < hi {
		t.Errorf("the shared width %d is under the widest natural %d", narrow, hi)
	}
}

// The battery empty state is the shared StatusPage: the description
// wraps.
func TestBatteryEmptyStateMatchesTheTemplate(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	if v.body.Visible() != "empty" {
		t.Fatalf("page %q, want empty", v.body.Visible())
	}
	if desc := brightnessOne[*widget.Label](t, findByClass(v, "empty-state"), "description"); !desc.Wrap() {
		t.Error("the battery empty description does not wrap")
	}
}

func TestBatteryDropdownSection(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	src := newFakeBattery(presentBattery())
	ctx.Battery = src
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	if v.body.Visible() != "battery" {
		t.Fatalf("page %q, want battery", v.body.Visible())
	}
	if v.heroPct.Text() != "64%" || v.heroState.Text() != i18n.T("dropdown-battery-on-battery") {
		t.Errorf("hero = %q / %q", v.heroPct.Text(), v.heroState.Text())
	}
	if !v.heroTime.Visible() || v.heroInput.Visible() {
		t.Errorf("discharging: time shown %v, input shown %v", v.heroTime.Visible(), v.heroInput.Visible())
	}
	if v.drawValue.Text() != "8.2W" || v.drawLabel.Text() != i18n.T("dropdown-battery-draw") {
		t.Errorf("draw = %q %q", v.drawValue.Text(), v.drawLabel.Text())
	}
	if v.capacityValue.Text() != "55 Wh" || v.healthText.Text() != "91%" || !v.healthDot.HasClass("good") {
		t.Errorf("capacity %q health %q", v.capacityValue.Text(), v.healthText.Text())
	}
	if !v.gauge.HasClass("good") || v.gauge.Value() != 0.64 {
		t.Errorf("gauge %v good=%v", v.gauge.Value(), v.gauge.HasClass("good"))
	}
	if v.chargeCard.Visible() || !v.chargeUnsupported.Visible() {
		t.Error("unsupported threshold: want the note, not the card")
	}

	// Charging at a critical level with a supported, enabled limit.
	src.update(func(d *upower.Device) {
		d.State, d.WarningLevel, d.Percentage = upower.StateCharging, upower.WarningCritical, 9
		d.TimeToFull, d.ChargeThresholdSupported, d.ChargeThresholdEnabled = time.Hour, true, true
	})
	v.applyDevice(v.readDevice(t.Context()))
	if v.heroState.Text() != i18n.T("dropdown-battery-critical") || !v.heroState.HasClass("crit") || !v.heroPct.HasClass("crit") {
		t.Errorf("critical hero = %q", v.heroState.Text())
	}
	if v.heroPct.HasClass("good") || !v.gauge.HasClass("crit") || v.gauge.HasClass("good") {
		t.Error("variant classes did not swap")
	}
	if !v.heroInput.Visible() || v.drawLabel.Text() != i18n.T("dropdown-battery-input") {
		t.Error("charging with a rate: want the input line and label")
	}
	if !v.chargeCard.Visible() || v.chargeUnsupported.Visible() || !v.chargeSwitch.On() {
		t.Error("supported threshold: want the card, switched on")
	}
	if got := itemText(v.charge, "description"); got != i18n.T("dropdown-battery-resumes-at", i18n.Str("threshold", "75")) {
		t.Errorf("resume = %q", got)
	}
	if calls := src.thresholdCalls(); len(calls) != 0 {
		t.Errorf("a backend state change wrote the threshold: %v", calls)
	}

	// The user toggling writes it.
	v.chargeSwitch.Toggle()
	waitHeadless(t, "the threshold write", func() bool { return len(src.thresholdCalls()) == 1 })
	if calls := src.thresholdCalls(); calls[0] {
		t.Errorf("toggle off wrote %v", calls)
	}
}

func TestBatteryDropdownProfiles(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	if !v.profilesUnavailable.Visible() {
		t.Error("no daemon: want the note")
	}
	for name, b := range v.profileButtons {
		if b.Enabled() {
			t.Errorf("no daemon: %s enabled", name)
		}
	}

	pp := &fakePPSource{snap: powerprofiles.Snapshot{
		Available: true, Active: powerprofiles.ProfileBalanced,
		Profiles: []string{powerprofiles.ProfilePowerSaver, powerprofiles.ProfileBalanced},
	}, ticks: make(chan struct{}, 1)}
	ctx.PowerProfiles = pp
	v = batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	if v.profilesUnavailable.Visible() {
		t.Error("daemon up: the note shows")
	}
	if !v.profileButtons[powerprofiles.ProfileBalanced].HasState(widget.StateChecked) {
		t.Error("balanced is not marked active")
	}
	if v.profileButtons[powerprofiles.ProfilePerformance].Enabled() {
		t.Error("performance enabled though the daemon lacks it")
	}

	// Selecting the active segment writes nothing; another one does.
	onHeadlessLoop(func() bool { v.selectProfile(powerprofiles.ProfileBalanced); return true })
	onHeadlessLoop(func() bool { v.selectProfile(powerprofiles.ProfilePowerSaver); return true })
	waitHeadless(t, "the profile write", func() bool { return len(pp.sets()) == 1 })
	if got := pp.sets(); !slices.Equal(got, []string{powerprofiles.ProfilePowerSaver}) {
		t.Errorf("sets = %v", got)
	}
	waitHeadless(t, "the saver mark", func() bool {
		return v.profileButtons[powerprofiles.ProfilePowerSaver].HasState(widget.StateChecked) &&
			!v.profileButtons[powerprofiles.ProfileBalanced].HasState(widget.StateChecked)
	})

	// A daemon-side change follows while open - and marking the
	// toggles writes nothing back (the blocked signal).
	pp.mu.Lock()
	pp.snap.Active = powerprofiles.ProfileBalanced
	pp.mu.Unlock()
	pp.ticks <- struct{}{}
	waitHeadless(t, "the followed profile", func() bool {
		return v.profileButtons[powerprofiles.ProfileBalanced].HasState(widget.StateChecked)
	})
	if got := pp.sets(); len(got) != 1 {
		t.Errorf("the followed change wrote %v back", got)
	}

	// The segments are grouped toggle buttons: a click selects through
	// the toggle, releasing the other segment; a click on the active
	// one keeps it and writes nothing.
	saver := v.profileButtons[powerprofiles.ProfilePowerSaver]
	if st := widget.Describe(saver); st.Role != widget.RoleToggleButton {
		t.Errorf("segment role %s, want a toggle button", st.Role)
	}
	onHeadlessLoop(func() bool { saver.ClickAt(widget.Point{}); return true })
	waitHeadless(t, "the clicked write", func() bool { return len(pp.sets()) == 2 })
	if !onHeadlessLoop(func() bool {
		return saver.Active() && !v.profileButtons[powerprofiles.ProfileBalanced].Active()
	}) {
		t.Error("the click did not move the toggle")
	}
	onHeadlessLoop(func() bool { saver.ClickAt(widget.Point{}); return true })
	time.Sleep(20 * time.Millisecond)
	if got := pp.sets(); len(got) != 2 {
		t.Errorf("a click on the active segment wrote: %v", got)
	}
}

// The details row's values and labels get their full height inside the
// padded row (a CenterBox once measured without its padding and clipped
// them to nothing).
func TestBatteryDetailsShowTheirText(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	ctx.Battery = newFakeBattery(upower.Device{Percentage: 64, State: upower.StateDischarging, IsPresent: true, EnergyRate: 9.4, Capacity: 92})
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	waitHeadless(t, "the battery page", func() bool { return v.body.Visible() == "battery" })
	ctx.Theme.Attach(v)
	sz := widget.SizeWithin(v, widget.Size{W: 400, H: 2000})
	v.Arrange(render.Rect{W: 400, H: sz.H})
	for _, l := range treetest.All[*widget.Label](findByClass(v, "battery-details")) {
		if natural := widget.SizeWithinAsChild(l, widget.Size{W: 400, H: 400}).H; l.Bounds().H < natural {
			t.Errorf("detail %q is %dpx tall, want its %dpx", l.Text(), l.Bounds().H, natural)
		}
	}
}

// The profile segments fit the dropdown's width: none is cut off at
// the card's edge.
func TestBatteryProfileSegmentsFitTheDropdown(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	v := batteryDropdown(ctx).(*batteryView)
	defer v.dropdownClosed()
	w, _, _ := dropdownDims("battery", ctx.Config)
	batteryArrangeStyled(t, ctx, v, w)
	seg := treetest.WithClass(v, "profile-seg")[0]
	end := boundsOf(seg).X + boundsOf(seg).W
	for _, p := range batteryProfiles {
		b := v.profileButtons[p.name].Bounds()
		if b.X+b.W > end || widget.SizeWithinAsChild(v.profileButtons[p.name], widget.Size{W: 1 << 14, H: 100}).W > b.W {
			t.Errorf("%s spans %+v, cut off in its %d-wide row", p.name, b, end)
		}
	}
}
