package bar

import (
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestDashboardBatteryHelpers(t *testing.T) {
	for _, tc := range []struct {
		pct      float64
		charging bool
		want     string
	}{
		{90, false, "ld-battery-full-symbolic"},
		{75, false, "ld-battery-medium-symbolic"},
		{51, false, "ld-battery-medium-symbolic"},
		{50, false, "ld-battery-low-symbolic"},
		{25, false, "ld-battery-warning-symbolic"},
		{10, true, "ld-battery-charging-symbolic"},
	} {
		if got := dashboardBatteryIcon(tc.pct, tc.charging); got != tc.want {
			t.Errorf("%v%% charging=%v = %q, want %q", tc.pct, tc.charging, got, tc.want)
		}
	}
	if got := dashboardBatteryTime(3900); got != i18n.T("dropdown-dashboard-battery-time-hm", i18n.Str("hours", "1"), i18n.Str("minutes", "5")) {
		t.Errorf("1h5m = %q", got)
	}
	if got := dashboardBatteryTime(300); got != i18n.T("dropdown-dashboard-battery-time-m", i18n.Str("minutes", "5")) {
		t.Errorf("5m = %q", got)
	}
	if dashboardBatteryTime(0) != "" {
		t.Error("no estimate rendered a time")
	}
	if label, icon := dashboardProfile(powerprofiles.ProfilePowerSaver); icon != "ld-leaf-symbolic" || label != i18n.T("dropdown-dashboard-battery-profile-saver") {
		t.Errorf("saver = %q %q", label, icon)
	}
	if _, icon := dashboardProfile("weird"); icon != "ld-scale-symbolic" {
		t.Errorf("unknown profile icon = %q, want balanced's", icon)
	}
}

func TestDashboardSpeedAndThresholds(t *testing.T) {
	if v, mega := dashboardSpeed(512); v != "0.5" || mega {
		t.Errorf("512 B/s = %q %v", v, mega)
	}
	if v, mega := dashboardSpeed(1536 * 1024); v != "1.5" || !mega {
		t.Errorf("1.5 MiB/s = %q %v", v, mega)
	}
	if dashboardThresholdClass(95, 70, 90) != "error" ||
		dashboardThresholdClass(70, 70, 90) != "warning" ||
		dashboardThresholdClass(69.9, 70, 90) != "success" {
		t.Error("threshold classes")
	}
}

// The dashboard paints from the stylesheet: the battery gauge and its
// status read threshold state classes instead of fills and tints, and
// the quick-action icons follow the cascade.
func TestDashboardPaintsFromTheStylesheet(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying})
	ctx.Battery = newFakeBattery(upower.Device{Percentage: 8, State: upower.StateDischarging, IsPresent: true, TimeToEmpty: 20 * time.Minute})
	v := dashboardDropdown(ctx).(*dashboardView)

	var gauge *widget.ProgressBar
	var percent *widget.Label
	walkTree(v, func(w widget.Widget) bool {
		switch w := w.(type) {
		case *widget.ProgressBar:
			if w.HasClass("progress-bar") {
				gauge = w
			}
		case *widget.Label:
			if w.HasClass("battery-percent") {
				percent = w
			}
		}
		return true
	})
	if gauge == nil || percent == nil {
		t.Fatalf("the battery card = gauge %v, percent %v; want a classed gauge and percent", gauge, percent)
	}
	if gauge.Fill != 0 {
		t.Errorf("the gauge carries a fill %#08x", uint32(gauge.Fill))
	}
	if !gauge.HasClass("error") || gauge.HasClass("success") || gauge.HasClass("warning") {
		t.Error("a critical battery did not put the gauge in the error state")
	}
	if got := percent.Color(); got != 0 {
		t.Errorf("the percent carries a programmatic color %#08x", uint32(got))
	}
	if !percent.HasClass("critical") {
		t.Error("a critical battery did not mark the percent")
	}

	// The quick-action icons have no tint: .quick-action-icon image
	// inks them, .active inverts to fg-on-accent.
	walkTree(v, func(w widget.Widget) bool {
		if b, ok := w.(*widget.Button); ok && b.HasClass("quick-action") {
			walkTree(b, func(c widget.Widget) bool {
				if icon, ok := c.(*widget.Icon); ok && icon.Tint() != 0 {
					t.Errorf("a quick-action icon carries a tint %#08x", uint32(icon.Tint()))
				}
				return true
			})
		}
		return true
	})

	// No network: the speeds read muted through the class, uncolored.
	walkTree(v, func(w widget.Widget) bool {
		if l, ok := w.(*widget.Label); ok && l.HasClass("speed-value") {
			if got := l.Color(); got != 0 || !l.HasClass("muted") {
				t.Errorf("a disconnected speed = color %#08x muted %v", uint32(l.Color()), l.HasClass("muted"))
			}
		}
		return true
	})
}

func TestDashboardQuickActionsDriveTheServices(t *testing.T) {
	// DND persists to the state dir; keep it out of the real one.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	ctx.Notifications = notifications.NewService()
	ctx.Notifications.SetDND(false)
	ctx.IdleInhibit = idleinhibit.NewState(0)
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying, CanGoNext: true})
	v := dashboardDropdown(ctx).(*dashboardView)
	if len(v.refreshers) == 0 {
		t.Fatal("no sections refresh")
	}
	// The quick actions in grid order: wifi, bluetooth, airplane, dnd,
	// idle inhibit, power saver.
	var actions []*widget.Button
	walkTree(v, func(w widget.Widget) bool {
		if b, ok := w.(*widget.Button); ok && b.HasClass("quick-action") {
			actions = append(actions, b)
		}
		return true
	})
	if len(actions) != 6 {
		t.Fatalf("quick actions = %d, want 6", len(actions))
	}
	wifi, dnd, idle := actions[0], actions[3], actions[4]
	dnd.OnClick()
	if !ctx.Notifications.DND() {
		t.Error("the DND toggle did not reach the service")
	}
	v.refreshAll()
	if !dnd.HasClass("active") {
		t.Error("DND on did not mark its button active")
	}
	idle.OnClick()
	if !ctx.IdleInhibit.Active() {
		t.Error("the idle toggle did not enable inhibition")
	}
	// No wifi device: the wifi toggle is insensitive.
	if wifi.Enabled() {
		t.Error("wifi without a device is clickable")
	}
}

// walkTree visits w and its descendants depth-first while visit
// returns true.
func walkTree(w widget.Widget, visit func(widget.Widget) bool) {
	if w == nil || !visit(w) {
		return
	}
	if p, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, c := range p.Children() {
			walkTree(c, visit)
		}
	}
}

func TestDashboardMediaAndBatterySections(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying})
	ctx.Battery = newFakeBattery(upower.Device{Percentage: 8, State: upower.StateDischarging, IsPresent: true, TimeToEmpty: 20 * time.Minute})
	v := dashboardDropdown(ctx).(*dashboardView)
	texts := map[string]bool{}
	walkTree(v, func(w widget.Widget) bool {
		if l, ok := w.(*widget.Label); ok {
			texts[l.Text()] = true
		}
		return true
	})
	for _, want := range []string{"Song", "Band", "8%", i18n.T("dropdown-dashboard-battery-time-m", i18n.Str("minutes", "20")), i18n.T("dropdown-dashboard-now-playing")} {
		if !texts[want] {
			t.Errorf("dashboard is missing %q", want)
		}
	}
	// Without a player the media card shows the empty state.
	ctx.Media = newFakeMedia()
	v = dashboardDropdown(ctx).(*dashboardView)
	found := false
	walkTree(v, func(w widget.Widget) bool {
		if s, ok := w.(*widget.Stack); ok && s.Visible() == "empty" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("no player did not show the empty state")
	}
}

// The Rust content box pads outside the scroll: .dropdown-content >
// .dashboard-scroll > .dashboard-content-box, with the cards in the
// inner box.
func TestDashboardContentPadsOutsideTheScroll(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying})
	v := dashboardDropdown(ctx).(*dashboardView)
	content, ok := findByClass(v, "dropdown-content").(*widget.Box)
	if !ok {
		t.Fatal("no dropdown-content")
	}
	// Negative: the padded box carries nothing but the scroll — the
	// cards ride inside it.
	if got := len(content.Children()); got != 1 {
		t.Fatalf("dropdown-content children = %d, want only the scroll", got)
	}
	scroll, isScroll := content.Children()[0].(*widget.Scroll)
	if !isScroll || !scroll.HasClass("dashboard-scroll") {
		t.Fatalf("dropdown-content's child = %T, want the dashboard-scroll", content.Children()[0])
	}
	kids := scroll.Children()
	inner, isBox := kids[0].(*widget.Box)
	if len(kids) != 1 || !isBox || !inner.HasClass("dashboard-content-box") {
		t.Fatalf("the scroll wraps %v, want the dashboard-content-box", kids)
	}
	// Negative: the inner box is not the padding box itself.
	if inner.HasClass("dropdown-content") {
		t.Error("the content box rode inside the scroll")
	}
	if findByClass(inner, "quick-actions") == nil {
		t.Error("the quick actions did not land in the inner content box")
	}
	// The cards sit the theme's spacing apart.
	if got := inner.Spacing(); got != widget.Current().Spacing {
		t.Errorf("dashboard-content-box spacing = %d, want the theme's %d", got, widget.Current().Spacing)
	}
}

// The volume and media cards are always in the tree; their no-device
// and empty states live inside (Rust builds them unconditionally).
func TestDashboardAlwaysShowsTheVolumeAndMediaCards(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg) // no Pulse, no Media
	v := dashboardDropdown(ctx).(*dashboardView)
	var volumeCard, mediaCard *widget.Box
	walkTree(v, func(w widget.Widget) bool {
		if b, ok := w.(*widget.Box); ok && b.HasClass("dashboard-card") {
			var volume, media bool
			walkTree(b, func(c widget.Widget) bool {
				if l, ok := c.(*widget.Label); ok {
					switch l.Text() {
					case i18n.T("dropdown-dashboard-volume"):
						volume = true
					case i18n.T("dropdown-dashboard-now-playing"):
						media = true
					}
				}
				return true
			})
			if volume {
				volumeCard = b
			}
			if media {
				mediaCard = b
			}
		}
		return true
	})
	if volumeCard == nil || mediaCard == nil {
		t.Fatalf("without services the cards are missing: volume %v, media %v", volumeCard, mediaCard)
	}
	// No device: the slider row is disabled over the no-device label.
	row := classedBox(t, volumeCard, "dashboard-slider-row")
	if row.Enabled() {
		t.Error("the volume row is live without a device")
	}
	if l := classedBox(t, volumeCard, "dashboard-controls").Children(); len(l) != 2 {
		t.Errorf("dashboard-controls children = %d, want the row and the device label", len(l))
	}
	// Negative: the slider row is wrapped, not a bare card child.
	for _, c := range volumeCard.Children() {
		if b, ok := c.(*widget.Box); ok && b.HasClass("dashboard-slider-row") {
			t.Error("the slider row hangs off the bare card")
		}
	}
	// No player: the media card shows the empty state.
	var stack *widget.Stack
	walkTree(mediaCard, func(w widget.Widget) bool {
		if s, ok := w.(*widget.Stack); ok {
			stack = s
		}
		return true
	})
	if stack == nil || stack.Visible() != "empty" {
		t.Errorf("no player: the media stack = %v, want the empty page", stack)
	}
	// Positive control: with services the cards drive them.
	ctx.Pulse = &fakePulseSource{dev: pulse.Device{Name: "out", Description: "Speakers", Volume: pct(42)}, ticks: make(chan struct{}, 2)}
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying})
	v = dashboardDropdown(ctx).(*dashboardView)
	walkTree(v, func(w widget.Widget) bool {
		if b, ok := w.(*widget.Box); ok && b.HasClass("dashboard-slider-row") && b.Enabled() {
			volumeCard = b
		}
		return true
	})
	if volumeCard == nil {
		t.Error("with a sink the volume row stayed disabled")
	}
}

// battery-detail carries the warning/critical classes like the icon
// and the percent.
func TestDashboardBatteryDetailCarriesTheThresholdClasses(t *testing.T) {
	build := func(t *testing.T, pct float64) *widget.Label {
		t.Helper()
		cfg := config.Defaults()
		ctx := newTestContext(t, cfg)
		ctx.Battery = newFakeBattery(upower.Device{Percentage: pct, State: upower.StateDischarging, IsPresent: true, TimeToEmpty: 20 * time.Minute})
		v := dashboardDropdown(ctx).(*dashboardView)
		var detail *widget.Label
		walkTree(v, func(w widget.Widget) bool {
			if l, ok := w.(*widget.Label); ok && l.HasClass("battery-detail") {
				detail = l
			}
			return true
		})
		if detail == nil {
			t.Fatal("no battery-detail label")
		}
		return detail
	}
	critical := build(t, 8)
	if !critical.HasClass("critical") {
		t.Error("a critical battery did not mark the battery-detail")
	}
	if critical.HasClass("warning") {
		t.Error("a critical battery also carries the warning class")
	}
	healthy := build(t, 80)
	if healthy.HasClass("warning") || healthy.HasClass("critical") {
		t.Error("a healthy battery carries a threshold class")
	}
}

// The media empty state centers in its card with the sm glyph, the
// art carries the per-instance class, and the track labels cap their
// natural width.
func TestDashboardMediaEmptyStateArtAndLabels(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	v := dashboardDropdown(ctx).(*dashboardView)
	empty, ok := findByClass(v, "empty-state").(*widget.StatusPage)
	if !ok {
		t.Fatal("no empty-state")
	}
	icon := treetest.First[*widget.Icon](t, empty)
	arrangeDropdown(t, v, 400, 600)
	card, isBox := empty.Parent().(*widget.Box)
	if !isBox {
		t.Fatalf("the empty state's page parent = %T, want a box", empty.Parent())
	}
	eb, cb := empty.Bounds(), card.Bounds()
	if center := eb.X + eb.W/2; abs(center-(cb.X+cb.W/2)) > 3 {
		t.Errorf("the empty state centers at %d, the card at %d", center, cb.X+cb.W/2)
	}
	_ = icon

	// With a player: the art carries the per-instance class and the
	// track labels cap at one character from the start.
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "A Very Long Song Title That Should Cap", Artist: "Band", State: mpris.StatePlaying})
	v = dashboardDropdown(ctx).(*dashboardView)
	var artClasses []string
	walkTree(v, func(w widget.Widget) bool {
		if h, ok := w.(interface {
			HasClass(string) bool
			Classes() []string
		}); ok && h.HasClass("dashboard-media-art") {
			artClasses = h.Classes()
		}
		return true
	})
	if artClasses == nil {
		t.Fatal("no dashboard-media-art box")
	}
	instanced := false
	for _, c := range artClasses {
		if strings.HasPrefix(c, "dashboard-media-art-instance-") {
			instanced = true
		}
	}
	if !instanced {
		t.Errorf("the art carries classes %v, want a dashboard-media-art-instance-N", artClasses)
	}
	var track, artist, aTime *widget.Label
	walkTree(v, func(w widget.Widget) bool {
		if l, ok := w.(*widget.Label); ok {
			switch {
			case l.HasClass("media-track"):
				track = l
			case l.HasClass("media-artist"):
				artist = l
			case l.HasClass("media-time"):
				aTime = l
			}
		}
		return true
	})
	for _, tc := range []struct {
		what  string
		label *widget.Label
	}{
		{"media-track", track},
		{"media-artist", artist},
	} {
		if tc.label == nil {
			t.Fatalf("no %s label", tc.what)
		}
		if tc.label.MaxWidthChars() != 1 {
			t.Errorf("%s max width chars = %d, want the 1-char cap", tc.what, tc.label.MaxWidthChars())
		}
		if tc.label.Alignment() != render.AlignStart {
			t.Errorf("%s alignment = %v, want xalign 0", tc.what, tc.label.Alignment())
		}
	}
	// Negative: only the track labels cap — the times keep natural
	// width.
	if aTime != nil && aTime.MaxWidthChars() != 0 {
		t.Error("a media-time label carries the width cap")
	}
}

// A quick action's tile is its icon on a square tile over the label,
// and the grid sits its tiles the theme's spacing apart.
func TestDashboardQuickActionTiles(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	v := dashboardDropdown(ctx).(*dashboardView)
	tile := quickActionTile(widget.NewThemeIcon("ld-wifi-symbolic", 16), widget.NewLabel(ctx.Font, 12, "Wifi", 0))
	if got := len(tile.Children()); got != 2 {
		t.Fatalf("the tile children = %d, want the icon tile over the label", got)
	}
	square, ok := tile.Children()[0].(*widget.Box)
	if !ok || !square.HasClass("quick-action-icon") || square.SizeRequest() != (widget.Size{W: quickActionTilePx, H: quickActionTilePx}) {
		t.Errorf("the tile's first child = %T, want the square quick-action-icon tile", tile.Children()[0])
	}
	th := widget.Current()
	grid, _ := findByClass(v, "quick-actions").(*widget.Grid)
	if grid == nil || grid.ColumnSpacing() != th.Spacing || grid.RowSpacing() != th.Spacing {
		t.Errorf("quick-actions grid spacing = %v, want the theme's %d", grid, th.Spacing)
	}
}

// The battery card shows only for a present battery (the Rust
// dashboard builds it only then): a desktop's UPower display device
// reports none, and the card follows a battery arriving or leaving.
func TestDashboardBatteryCardFollowsPresence(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	bat := newFakeBattery(upower.Device{Percentage: 50, IsPresent: false})
	ctx.Battery = bat
	v := dashboardDropdown(ctx).(*dashboardView)
	card := func() *widget.Box {
		for _, c := range findByClass(v, "dashboard-info-row").(*widget.Box).Children() {
			if b, ok := c.(*widget.Box); ok && len(treetest.WithClass(b, "battery-status")) > 0 {
				return b
			}
		}
		t.Fatal("no battery card in the info row")
		return nil
	}
	if card().Visible() {
		t.Error("the battery card shows with no battery present")
	}
	bat.update(func(d *upower.Device) { d.IsPresent = true })
	v.refreshAll()
	if !card().Visible() {
		t.Error("the battery card stays hidden once a battery is present")
	}
}

// A quick action's tile and label center in the cell at their natural
// size: the colored icon tile stays a square, it never stretches.
func TestDashboardQuickActionTileCenters(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	icon := widget.NewThemeIcon("ld-wifi-symbolic", 16)
	label := widget.NewLabel(ctx.Font, 12, "WiFi", 0)
	col := quickActionTile(icon, label)
	b := dropdownButton(col, func() {}, "quick-action")
	ctx.Theme.Attach(b)
	b.Measure(widgetConstraintsMax(200, 120))
	b.Arrange(render.Rect{W: 200, H: 120})
	tile := col.Children()[0]
	nat := widget.MeasureAsChild(tile, widgetConstraintsMax(200, 120))
	tb := boundsOf(tile)
	if tb.W != nat.W {
		t.Errorf("the icon tile spans %dpx, want its natural %dpx", tb.W, nat.W)
	}
	if d := midX(tb) - 100; d < -1 || d > 1 {
		t.Errorf("the icon tile sits %dpx off the cell's center", d)
	}
	if d := midX(boundsOf(label)) - 100; d < -1 || d > 1 {
		t.Errorf("the label sits %dpx off the cell's center", d)
	}
}

// The dashboard grows to fit its sections: the scroll over them asks
// for their height, so a dropdown without a configured height does not
// collapse to its header.
func TestDashboardGrowsToItsSections(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	v := dashboardDropdown(ctx).(*dashboardView)
	w, h, _ := dropdownDims("dashboard", ctx.Config)
	card, _ := dropdownTree(dropdownPanel("dashboard", ctx.Config, v), ctx.Config)
	ctx.Theme.Attach(card)
	sz := widget.MeasureAsChild(card, widget.Constraints{Max: widget.Size{W: 2000, H: 4000}})
	sections := widget.MeasureAsChild(treetest.WithClass(v, "dashboard-scroll")[0].(*widget.Scroll).Children()[0], widget.Constraints{Max: widget.Size{W: w, H: 4000}})
	if h > 0 || sz.H < sections.H {
		t.Errorf("the dashboard (height %d) measures %dpx over %dpx of sections, want it grown to fit them", h, sz.H, sections.H)
	}
}
