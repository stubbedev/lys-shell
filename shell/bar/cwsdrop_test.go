package bar

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/styling"
)

func cwsDropContext(t *testing.T, location config.Location, scale float32) ModuleContext {
	t.Helper()
	cfg := config.Defaults()
	cfg.Bar = config.PlainBar()
	cfg.Bar.Location = location
	cfg.Bar.Scale = config.ScaleFactor(scale)
	ctx := styledContext(t, cfg)
	ctx.Connector = "DP-1"
	return ctx
}

func activeWS(id uint64) cwsWorkspace {
	ws := occupiedWS(id, int(id), "DP-1")
	ws.active = true
	return ws
}

func namedWS(id uint64, name string) cwsWorkspace {
	ws := occupiedWS(id, int(id), "DP-1")
	ws.name, ws.hasName = name, true
	return ws
}

func threeWorkspaces(active int) []cwsWorkspace {
	out := []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1"), occupiedWS(3, 3, "DP-1")}
	out[active].active = true
	return out
}

func cwsDropModule(t *testing.T, ctx ModuleContext, wcfg config.CompositorWorkspacesConfig, workspaces []cwsWorkspace) (*cwsModule, *fakeCws, *widget.Bar, widget.Size) {
	t.Helper()
	f := newFakeCws(workspaces, nil)
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, f)
	if err != nil {
		t.Fatal(err)
	}
	bar, size := cwsBar(t, ctx, m)
	return m, f, bar, size
}

func relayCwsBar(bar *widget.Bar, size widget.Size) {
	widget.SizeWithin(bar, availSize(size.W, size.H))
	bar.Arrange(render.Rect{W: size.W, H: size.H})
	widget.CollectDamage(bar)
	widget.SizeWithin(bar, availSize(size.W, size.H))
	bar.Arrange(render.Rect{W: size.W, H: size.H})
}

func paintCwsPixels(bar *widget.Bar, size widget.Size) []byte {
	relayCwsBar(bar, size)
	data := make([]byte, render.Stride(size.W)*size.H)
	bar.Paint(render.New(data, render.Stride(size.W), size.W, size.H))
	return data
}

func assertDropOn(t *testing.T, segs *widget.SegmentIndicator, i int) {
	t.Helper()
	if got, want := segs.EnableDrop().Bounds(), segs.Segment(i).Bounds(); got != want {
		t.Errorf("the indicator at %v, want segment %d's box %v", got, i, want)
	}
}

func assertDropAtRest(t *testing.T, segs *widget.SegmentIndicator) {
	t.Helper()
	if segs.EnableDrop().Moving() {
		t.Error("the indicator is animating")
	}
}

func TestCwsSegmentsAreCircles(t *testing.T) {
	iconMap := map[string]config.WorkspaceStyle{}
	icon := "ld-globe-symbolic"
	for _, key := range []string{"1", "10"} {
		iconMap[key] = config.WorkspaceStyle{Icon: &icon}
	}
	for _, tc := range []struct {
		name string
		edit func(*config.CompositorWorkspacesConfig)
	}{
		{"one and two digit labels", func(*config.CompositorWorkspacesConfig) {}},
		{"icons", func(c *config.CompositorWorkspacesConfig) {
			c.DisplayMode = config.DisplayModeIcon
			c.WorkspaceMap = iconMap
		}},
		{"nothing", func(c *config.CompositorWorkspacesConfig) { c.DisplayMode = config.DisplayModeNone }},
		{"large label", func(c *config.CompositorWorkspacesConfig) {
			c.LabelSize = config.Size{Value: 26, Unit: config.SizePixels}
		}},
		{"large padding", func(c *config.CompositorWorkspacesConfig) {
			c.WorkspacePad = config.Size{Value: 20, Unit: config.SizePixels}
		}},
	} {
		for _, location := range []config.Location{config.LocationTop, config.LocationLeft} {
			for _, scale := range []float32{1, 1.5} {
				t.Run(tc.name+"/"+string(location)+"/"+strconv.FormatFloat(float64(scale), 'f', 1, 32), func(t *testing.T) {
					ctx := cwsDropContext(t, location, scale)
					wcfg := config.DefaultsNiriWorkspaces().View()
					tc.edit(&wcfg)
					m, _, bar, size := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{activeWS(1), occupiedWS(10, 10, "DP-1")})
					relayCwsBar(bar, size)
					first := m.segs.Segment(0).Bounds()
					for i := range m.segs.Count() {
						r := m.segs.Segment(i).Bounds()
						if r.W != r.H || r.W == 0 {
							t.Errorf("segment %d is %dx%d, want a circle", i, r.W, r.H)
						}
						if r.W != first.W {
							t.Errorf("segment %d is %d across, the first is %d", i, r.W, first.W)
						}
					}
				})
			}
		}
	}
}

func TestCwsCircleContentIsCentered(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	m, _, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), []cwsWorkspace{activeWS(1), occupiedWS(10, 10, "DP-1")})
	relayCwsBar(bar, size)
	for i := range m.segs.Count() {
		seg := m.segs.Segment(i)
		kids := seg.Children()
		if len(kids) != 1 {
			t.Fatalf("segment %d has %d children", i, len(kids))
		}
		slot, ok := kids[0].(*widget.Box)
		if !ok || len(slot.Children()) != 1 {
			t.Fatalf("segment %d's slot is %T", i, kids[0])
		}
		b, c := seg.Bounds(), slot.Children()[0].(*widget.Box).Bounds()
		if dx := (c.X + c.W/2) - (b.X + b.W/2); dx < -1 || dx > 1 {
			t.Errorf("segment %d: content %v off the centre of %v", i, c, b)
		}
		if dy := (c.Y + c.H/2) - (b.Y + b.H/2); dy < -1 || dy > 1 {
			t.Errorf("segment %d: content %v off the middle of %v", i, c, b)
		}
	}
}

func TestCwsContentPastTheCircleIsAPill(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location config.Location
		edit     func(*config.CompositorWorkspacesConfig)
		ws       []cwsWorkspace
		tall     bool
	}{
		{"app icons", config.LocationTop, func(c *config.CompositorWorkspacesConfig) { c.AppIconsShow = true }, nil, false},
		{"app icons down a side bar", config.LocationLeft, func(c *config.CompositorWorkspacesConfig) { c.AppIconsShow = true }, nil, true},
		{"long name", config.LocationTop, func(c *config.CompositorWorkspacesConfig) { c.LabelStrategy = config.LabelNameOrIndex }, []cwsWorkspace{namedWS(1, "browser")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := cwsDropContext(t, tc.location, 1)
			wcfg := config.DefaultsNiriWorkspaces().View()
			tc.edit(&wcfg)
			ws := tc.ws
			if ws == nil {
				ws = []cwsWorkspace{activeWS(1)}
			}
			ws[0].active = true
			m, _, bar, size := cwsDropModule(t, ctx, wcfg, ws)
			relayCwsBar(bar, size)
			r := m.segs.Segment(0).Bounds()
			if tc.tall && r.H <= r.W || !tc.tall && r.W <= r.H {
				t.Errorf("segment %dx%d, want a pill along the bar", r.W, r.H)
			}
			assertDropOn(t, m.segs, 0)
			data := paintCwsPixels(bar, size)
			if got, center := pixelAt(data, size.W, r.X, r.Y), pixelAt(data, size.W, r.X+r.W/2, r.Y+1); got == center {
				t.Errorf("the pill's corner %#08x paints like its fill: it has no end radius", uint32(got))
			}
		})
	}
}

func TestCwsIndicatorPaintsTheCircleInTheActiveColor(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	palette := ctx.Theme.RenderPalette()
	accent, _ := styling.ResolveColor(wcfg.ActiveColor, palette)
	m, _, bar, size := cwsDropModule(t, ctx, wcfg, threeWorkspaces(1))
	data := paintCwsPixels(bar, size)
	seg := m.segs.Segment(1).Bounds()
	if got := pixelAt(data, size.W, seg.X+2, seg.Y+seg.H/2); got != accent {
		t.Errorf("inside the circle %#08x, want the active color %#08x", uint32(got), uint32(accent))
	}
	if got := pixelAt(data, size.W, seg.X, seg.Y); got == accent {
		t.Error("the circle's corner paints the active color")
	}
	other := m.segs.Segment(0).Bounds()
	if got := pixelAt(data, size.W, other.X+2, other.Y+other.H/2); got == accent {
		t.Error("an inactive segment paints the active color")
	}
}

func TestCwsIndicatorEndsOnTheActiveSegment(t *testing.T) {
	for _, location := range []config.Location{config.LocationTop, config.LocationLeft} {
		t.Run(string(location), func(t *testing.T) {
			ctx := cwsDropContext(t, location, 1)
			m, f, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(0))
			relayCwsBar(bar, size)
			assertDropOn(t, m.segs, 0)
			assertDropAtRest(t, m.segs)

			restore := widget.SetAnimationsInstant(true)
			defer restore()
			f.workspaces = threeWorkspaces(2)
			if err := renderOnce(m.read, m.apply); err != nil {
				t.Fatal(err)
			}
			relayCwsBar(bar, size)
			assertDropAtRest(t, m.segs)
			assertDropOn(t, m.segs, 2)
		})
	}
}

func TestCwsSwitchStartsTheIndicatorTravelling(t *testing.T) {
	for _, location := range []config.Location{config.LocationTop, config.LocationLeft} {
		t.Run(string(location), func(t *testing.T) {
			ctx := cwsDropContext(t, location, 1)
			m, f, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(0))
			relayCwsBar(bar, size)
			start := m.segs.EnableDrop().Bounds()
			f.workspaces = threeWorkspaces(2)
			if err := renderOnce(m.read, m.apply); err != nil {
				t.Fatal(err)
			}
			relayCwsBar(bar, size)
			if !m.segs.EnableDrop().Moving() {
				t.Error("a workspace switch did not start the indicator")
			}
			if got := m.segs.EnableDrop().Bounds(); got != start {
				t.Errorf("the indicator jumped from %v to %v on the frame the switch landed", start, got)
			}
			if !widget.HasClass(m.segs.Segment(2), "active") {
				t.Error("the target segment lacks its active class")
			}
		})
	}
}

func TestCwsNoIndicatorWhenNothingIsActive(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	accent, _ := styling.ResolveColor(wcfg.ActiveColor, ctx.Theme.RenderPalette())
	m, f, bar, size := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1")})
	assertNone := func(when string) {
		t.Helper()
		data := paintCwsPixels(bar, size)
		if !m.segs.EnableDrop().Bounds().Empty() || m.segs.Active() != -1 {
			t.Errorf("%s: indicator at %v, active %d", when, m.segs.EnableDrop().Bounds(), m.segs.Active())
		}
		assertDropAtRest(t, m.segs)
		for i := range data {
			if i%4 == 0 && render.ColorFromBytes(data[i:]) == accent {
				t.Fatalf("%s: the active color is painted", when)
			}
		}
	}
	assertNone("never active")
	f.workspaces = []cwsWorkspace{activeWS(1), occupiedWS(2, 2, "DP-1")}
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	relayCwsBar(bar, size)
	assertDropOn(t, m.segs, 0)
	assertDropAtRest(t, m.segs)
	f.workspaces = []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1")}
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	assertNone("active gone")
}

func TestCwsRebuildWithTheSameActiveDoesNotAnimate(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	m, _, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(1))
	relayCwsBar(bar, size)
	before := m.segs.EnableDrop().Bounds()
	for range 3 {
		if err := renderOnce(m.read, m.apply); err != nil {
			t.Fatal(err)
		}
		relayCwsBar(bar, size)
		assertDropAtRest(t, m.segs)
	}
	if got := m.segs.EnableDrop().Bounds(); got != before {
		t.Errorf("the indicator moved from %v to %v", before, got)
	}
}

func TestCwsWorkspaceRemovedBeforeTheActiveDoesNotAnimate(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	m, f, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(2))
	relayCwsBar(bar, size)
	assertDropOn(t, m.segs, 2)
	f.workspaces = f.workspaces[1:]
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	relayCwsBar(bar, size)
	assertDropAtRest(t, m.segs)
	assertDropOn(t, m.segs, 1)
}

func TestCwsWorkspaceRemovedMidFlightLandsOnTheRightSegment(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	m, f, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(0))
	relayCwsBar(bar, size)
	f.workspaces = threeWorkspaces(2)
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	relayCwsBar(bar, size)
	if !m.segs.EnableDrop().Moving() {
		t.Fatal("the switch did not start the indicator")
	}
	restore := widget.SetAnimationsInstant(true)
	defer restore()
	f.workspaces = f.workspaces[1:]
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	relayCwsBar(bar, size)
	assertDropAtRest(t, m.segs)
	assertDropOn(t, m.segs, 1)
	if !widget.HasClass(m.segs.Segment(1), "active") || m.segs.Count() != 2 {
		t.Errorf("active marks %v on %d segments", m.segs.Segment(1).Classes(), m.segs.Count())
	}
}

func TestCwsWorkspaceMapTintsTheIndicator(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	red, _ := config.ParseColorValue("#ff0000")
	wcfg.WorkspaceMap = map[string]config.WorkspaceStyle{"1": {Color: &red}}
	m, f, bar, size := cwsDropModule(t, ctx, wcfg, threeWorkspaces(0))
	data := paintCwsPixels(bar, size)
	seg := m.segs.Segment(0).Bounds()
	if got := pixelAt(data, size.W, seg.X+2, seg.Y+seg.H/2); got != render.RGB(0xff, 0, 0) {
		t.Errorf("the mapped active workspace's indicator paints %#08x, want its color", uint32(got))
	}
	restore := widget.SetAnimationsInstant(true)
	defer restore()
	f.workspaces = threeWorkspaces(1)
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	accent, _ := styling.ResolveColor(wcfg.ActiveColor, ctx.Theme.RenderPalette())
	data = paintCwsPixels(bar, size)
	seg = m.segs.Segment(1).Bounds()
	if got := pixelAt(data, size.W, seg.X+2, seg.Y+seg.H/2); got != accent {
		t.Errorf("an unmapped active workspace's indicator paints %#08x, want the active color %#08x", uint32(got), uint32(accent))
	}
}

func TestCwsUrgentPulseFadesTheActiveIndicator(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	accent, _ := styling.ResolveColor(wcfg.ActiveColor, ctx.Theme.RenderPalette())
	ws := activeWS(1)
	ws.urgent = true
	m, _, bar, size := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{ws})
	rest := func() render.Color {
		data := paintCwsPixels(bar, size)
		seg := m.segs.Segment(0).Bounds()
		return pixelAt(data, size.W, seg.X+2, seg.Y+seg.H/2)
	}
	if got := rest(); got == accent {
		t.Error("pulse on: the indicator paints at full strength")
	}
	m.blinkTick()
	if got := rest(); got != accent {
		t.Errorf("pulse off: the indicator paints %#08x, want %#08x", uint32(got), uint32(accent))
	}
	m.blinkTick()
	if got := rest(); got == accent {
		t.Error("pulse back on: the indicator paints at full strength")
	}
}

func TestCwsUrgentApplicationModeLeavesTheIndicatorWhole(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	wcfg.UrgentMode = config.UrgentApplication
	ws := activeWS(1)
	ws.urgent = true
	m, _, _, _ := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{ws})
	if widget.HasClass(m.segs.EnableDrop(), "urgent") {
		t.Error("urgent-mode application fades the indicator")
	}
}

func TestCwsUnderlineTravelsAsTheIndicator(t *testing.T) {
	for _, location := range []config.Location{config.LocationTop, config.LocationLeft} {
		t.Run(string(location), func(t *testing.T) {
			ctx := cwsDropContext(t, location, 1)
			wcfg := config.DefaultsNiriWorkspaces().View()
			wcfg.ActiveIndicator = config.ActiveUnderline
			accent, _ := styling.ResolveColor(wcfg.ActiveColor, ctx.Theme.RenderPalette())
			m, f, bar, size := cwsDropModule(t, ctx, wcfg, threeWorkspaces(0))
			mark := func(r render.Rect) (x, y int) {
				if location == config.LocationLeft {
					return r.X, r.Y + r.H/2
				}
				return r.X + r.W/2, r.Y + r.H - 1
			}
			data := paintCwsPixels(bar, size)
			seg := m.segs.Segment(0).Bounds()
			x, y := mark(seg)
			if got := pixelAt(data, size.W, x, y); got != accent {
				t.Errorf("the underline at (%d,%d) paints %#08x, want %#08x", x, y, uint32(got), uint32(accent))
			}
			if got := pixelAt(data, size.W, seg.X+seg.W/2+3, seg.Y+seg.H/2-5); got == accent {
				t.Error("the underlined workspace is filled")
			}
			f.workspaces = threeWorkspaces(1)
			if err := renderOnce(m.read, m.apply); err != nil {
				t.Fatal(err)
			}
			relayCwsBar(bar, size)
			if !m.segs.EnableDrop().Moving() {
				t.Error("the underline did not start travelling")
			}
			restore := widget.SetAnimationsInstant(true)
			defer restore()
			f.workspaces = threeWorkspaces(2)
			if err := renderOnce(m.read, m.apply); err != nil {
				t.Fatal(err)
			}
			data = paintCwsPixels(bar, size)
			assertDropOn(t, m.segs, 2)
			x, y = mark(m.segs.Segment(2).Bounds())
			if got := pixelAt(data, size.W, x, y); got != accent {
				t.Errorf("the settled underline at (%d,%d) paints %#08x, want %#08x", x, y, uint32(got), uint32(accent))
			}
		})
	}
}

func TestHyprlandIndicatorIgnoresAnotherMonitorsSwitch(t *testing.T) {
	_, m := newTestHypr(t, nil)
	row := widget.NewBox(widget.Row, 0, 0).Append(m.segs, false)
	t.Cleanup(widget.Host(row))
	layout := func() {
		widget.SizeWithin(row, widget.Size{W: 400, H: 40})
		row.Arrange(render.Rect{W: 400, H: 40})
	}
	layout()
	before, active := m.segs.EnableDrop().Bounds(), m.segs.Active()
	if before.Empty() || active < 0 {
		t.Fatalf("the bar's monitor shows no active workspace: indicator %v, active %d", before, active)
	}
	handleNow(t, m, hyprland.Event{Kind: hyprland.EventWorkspaceV2, ID: 3, Name: "3"})
	layout()
	assertDropAtRest(t, m.segs)
	if m.segs.Active() != active || m.segs.EnableDrop().Bounds() != before {
		t.Errorf("another monitor's switch moved active to %d, indicator to %v", m.segs.Active(), m.segs.EnableDrop().Bounds())
	}
}

func TestHyprlandIndicatorTravelsOnTheBarsMonitor(t *testing.T) {
	_, m := newTestHypr(t, nil)
	row := widget.NewBox(widget.Row, 0, 0).Append(m.segs, false)
	t.Cleanup(widget.Host(row))
	layout := func() {
		widget.SizeWithin(row, widget.Size{W: 400, H: 40})
		row.Arrange(render.Rect{W: 400, H: 40})
	}
	layout()
	if m.segs.Count() < 2 {
		t.Fatalf("%d segments, the test needs two", m.segs.Count())
	}
	target := 0
	if m.segs.Active() == 0 {
		target = 1
	}
	id := m.shown[target].ws.num
	m.monitors[0].ActiveWS.ID = id
	handleNow(t, m, hyprland.Event{Kind: hyprland.EventWorkspaceV2, ID: id})
	layout()
	if m.segs.Active() != target {
		t.Fatalf("active %d, want %d", m.segs.Active(), target)
	}
	if !m.segs.EnableDrop().Moving() {
		t.Error("the switch on the bar's monitor did not start the indicator")
	}
}

func assertSharedLook(t *testing.T, name string, segs *widget.SegmentIndicator) {
	t.Helper()
	if !strings.Contains(segs.InlineStyle(), "--ws-circle: ") {
		t.Errorf("%s: row style %q does not size the circle", name, segs.InlineStyle())
	}
	if !widget.HasClass(segs.EnableDrop(), "indicator-background") {
		t.Errorf("%s: indicator classes %v", name, segs.EnableDrop().Classes())
	}
}

func TestEveryWorkspacesModuleSharesTheLook(t *testing.T) {
	for _, kind := range []string{"sway", "niri", "lys-wm"} {
		ctx := cwsDropContext(t, config.LocationTop, 1)
		m, err := newCwsModule(ctx, kind, cwsWorkspaces, config.DefaultsNiriWorkspaces().View(), newFakeCws(threeWorkspaces(0), nil))
		if err != nil {
			t.Fatal(err)
		}
		assertSharedLook(t, kind, m.segs)
	}
	ctx := cwsDropContext(t, config.LocationTop, 1)
	tags, err := newCwsModule(ctx, "mango", cwsTags, config.DefaultsNiriWorkspaces().View(), newFakeCws(threeWorkspaces(0), nil))
	if err != nil {
		t.Fatal(err)
	}
	assertSharedLook(t, "mango", tags.segs)
	_, hypr := newTestHypr(t, nil)
	assertSharedLook(t, "hyprland", hypr.segs)
}

func TestHyprlandWorkspaceDestroyedBeforeTheActiveDoesNotAnimate(t *testing.T) {
	f, m := newTestHypr(t, nil)
	f.set("j/workspaces", `[{"id":1,"name":"1","monitor":"DP-1","windows":1},{"id":2,"name":"2","monitor":"DP-1","windows":1},{"id":4,"name":"4","monitor":"DP-1","windows":1}]`)
	f.set("j/monitors", `[{"id":0,"name":"DP-1","activeWorkspace":{"id":2,"name":"2"},"focused":true}]`)
	row := widget.NewBox(widget.Row, 0, 0).Append(m.segs, false)
	t.Cleanup(widget.Host(row))
	layout := func() {
		widget.SizeWithin(row, widget.Size{W: 400, H: 40})
		row.Arrange(render.Rect{W: 400, H: 40})
	}
	defer widget.SetAnimationsInstant(true)()
	handleNow(t, m, hyprland.Event{Kind: hyprland.EventCreateWspcV2, ID: 4, Name: "4"})
	layout()
	if m.segs.Active() != 1 {
		t.Fatalf("active %d of %d, want workspace 2 at index 1", m.segs.Active(), m.segs.Count())
	}
	widget.SetAnimationsInstant(false)
	f.set("j/workspaces", `[{"id":2,"name":"2","monitor":"DP-1","windows":1},{"id":4,"name":"4","monitor":"DP-1","windows":1}]`)
	handleNow(t, m, hyprland.Event{Kind: hyprland.EventDestroyWspcV2, ID: 1, Name: "1"})
	layout()
	assertDropAtRest(t, m.segs)
	assertDropOn(t, m.segs, 0)
}

func TestCwsLongNamePillTakesThePadding(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	wcfg := config.DefaultsNiriWorkspaces().View()
	wcfg.LabelStrategy = config.LabelNameOrIndex
	long := namedWS(1, "browser")
	long.active = true
	m, _, bar, size := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{long, occupiedWS(2, 2, "DP-1")})
	relayCwsBar(bar, size)
	want := m.measureLabel("browser").W + 2*m.paddingPx()
	if got := m.segs.Segment(0).Bounds().W; got < want {
		t.Errorf("the pill is %d wide, want at least the name and its padding, %d", got, want)
	}
	if r := m.segs.Segment(1).Bounds(); r.W != r.H {
		t.Errorf("the short label beside it is %dx%d, want a circle", r.W, r.H)
	}
}

func TestHyprlandOtherMonitorFillsItsOwnCircle(t *testing.T) {
	_, conn := startFakeHypr(t, map[string]string{
		"j/workspaces":      hyprWorkspacesJSON,
		"j/monitors":        hyprMonitorsJSON,
		"j/clients":         hyprClientsJSON,
		"j/workspacerules":  `[]`,
		"j/activeworkspace": `{"id":1,"name":"1","monitor":"DP-1","windows":2}`,
	})
	cfg := config.Defaults()
	cfg.Bar = config.PlainBar()
	cfg.HyprlandWorkspaces.MonitorSpecific = false
	teal, err := config.ParseColorValue("#008080")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HyprlandWorkspaces.ActiveOnOtherMonitorColor = teal
	ctx := styledContext(t, cfg)
	ctx.Hyprland = conn
	ctx.Connector = "DP-1"
	module, err := Create("hyprland-workspaces", ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := module.(*hyprlandWorkspaces)
	bar, size := cwsBar(t, ctx, m)
	data := paintCwsPixels(bar, size)
	other := -1
	for i, model := range m.shown {
		if model.ws.num == 3 {
			other = i
		}
	}
	if other < 0 {
		t.Fatal("workspace 3 is not shown")
	}
	if other == m.segs.Active() {
		t.Fatal("workspace 3 is the bar's own active workspace")
	}
	r := m.segs.Segment(other).Bounds()
	fill := render.RGB(0, 0x80, 0x80)
	if got := pixelAt(data, size.W, r.X+2, r.Y+r.H/2); got != fill {
		t.Errorf("inside the circle %#08x, want the other-monitor color %#08x", uint32(got), uint32(fill))
	}
	if got := pixelAt(data, size.W, r.X, r.Y); got == fill {
		t.Error("the other-monitor fill has a square corner")
	}
	assertDropOn(t, m.segs, m.segs.Active())
}

func TestCwsSecondRebuildBeforeTheNextFrameKeepsTheTravel(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	m, f, bar, size := cwsDropModule(t, ctx, config.DefaultsNiriWorkspaces().View(), threeWorkspaces(0))
	relayCwsBar(bar, size)
	f.workspaces = threeWorkspaces(2)
	for range 2 {
		if err := renderOnce(m.read, m.apply); err != nil {
			t.Fatal(err)
		}
	}
	relayCwsBar(bar, size)
	if !m.segs.EnableDrop().Moving() {
		t.Error("a second event for the same switch cancelled the travel")
	}
}

func TestCwsLeavingAnEmptyWorkspaceTravelsFromInsideTheRow(t *testing.T) {
	ctx := cwsDropContext(t, config.LocationTop, 1)
	last := emptyWS(3, 3, "DP-1")
	last.active = true
	wcfg := config.DefaultsNiriWorkspaces().View()
	wcfg.HideTrailingEmpty = false
	m, f, bar, size := cwsDropModule(t, ctx, wcfg, []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1"), last})
	relayCwsBar(bar, size)
	assertDropOn(t, m.segs, 2)
	first := activeWS(1)
	f.workspaces = []cwsWorkspace{first, occupiedWS(2, 2, "DP-1")}
	if err := renderOnce(m.read, m.apply); err != nil {
		t.Fatal(err)
	}
	relayCwsBar(bar, size)
	if !m.segs.EnableDrop().Moving() {
		t.Error("the switch did not start the indicator")
	}
	d, row := m.segs.EnableDrop().Bounds(), m.segs.Bounds()
	if d.Empty() || d.X < row.X || d.X+d.W > row.X+row.W {
		t.Errorf("the indicator at %v in the shrunken row %v", d, row)
	}
}
