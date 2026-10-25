package bar

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/appicons"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// The filtering cases are niri/sway filtering.rs's tests.

func occupiedWS(id uint64, num int, output string) cwsWorkspace {
	return cwsWorkspace{id: id, num: num, output: output, hasOutput: true, hasWindows: true}
}

func emptyWS(id uint64, num int, output string) cwsWorkspace {
	return cwsWorkspace{id: id, num: num, output: output, hasOutput: true}
}

func wsIDs(list []cwsWorkspace) []uint64 {
	out := make([]uint64, len(list))
	for i, ws := range list {
		out[i] = ws.id
	}
	return out
}

func TestCwsShowsEmptyWorkspacesWithoutHideTrailing(t *testing.T) {
	named := emptyWS(3, 3, "DP-1")
	named.name, named.hasName = "web", true
	got := cwsCollectDisplayed([]cwsWorkspace{occupiedWS(1, 1, "DP-1"), emptyWS(2, 2, "DP-1"), named}, cwsFilter{})
	if !reflect.DeepEqual(wsIDs(got), []uint64{1, 2, 3}) {
		t.Errorf("ids = %v", wsIDs(got))
	}
}

func TestCwsHideTrailingEmptyDropsOnlyTheTail(t *testing.T) {
	all := []cwsWorkspace{occupiedWS(1, 1, "DP-1"), emptyWS(2, 2, "DP-1"), occupiedWS(3, 3, "DP-1"), emptyWS(4, 4, "DP-1"), emptyWS(5, 1, "HDMI-A-1")}
	got := cwsCollectDisplayed(all, cwsFilter{hideTrailingEmpty: true})
	// The inner empty 2 stays; each output's empty tail goes.
	if !reflect.DeepEqual(wsIDs(got), []uint64{1, 2, 3}) {
		t.Errorf("ids = %v, want the trailing empties of both outputs dropped", wsIDs(got))
	}
	// An occupied tail is kept.
	got = cwsCollectDisplayed([]cwsWorkspace{emptyWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1")}, cwsFilter{hideTrailingEmpty: true})
	if !reflect.DeepEqual(wsIDs(got), []uint64{1, 2}) {
		t.Errorf("ids = %v, want an occupied tail kept", wsIDs(got))
	}
}

func TestCwsIgnoreByNameNumberAndID(t *testing.T) {
	scratch := emptyWS(3, 3, "DP-1")
	scratch.name, scratch.hasName = "scratch", true
	all := []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1"), scratch, occupiedWS(40, 9, "DP-1")}
	if got := cwsCollectDisplayed(all, cwsFilter{ignore: []string{"scr*"}}); !reflect.DeepEqual(wsIDs(got), []uint64{1, 2, 40}) {
		t.Errorf("name glob: %v", wsIDs(got))
	}
	if got := cwsCollectDisplayed(all, cwsFilter{ignore: []string{"?"}}); !reflect.DeepEqual(wsIDs(got), []uint64{}) {
		t.Errorf("single-digit number glob: %v, want all ignored", wsIDs(got))
	}
	if got := cwsCollectDisplayed(all, cwsFilter{ignore: []string{"40"}}); !reflect.DeepEqual(wsIDs(got), []uint64{1, 2, 3}) {
		t.Errorf("stable id: %v", wsIDs(got))
	}
	// Case-sensitive, as the wildcard crate is.
	if got := cwsCollectDisplayed(all, cwsFilter{ignore: []string{"SCRATCH"}}); len(got) != 4 {
		t.Errorf("case-insensitive match: %v", wsIDs(got))
	}
}

func TestCwsOrdersByOutputThenNumber(t *testing.T) {
	unnumbered := occupiedWS(9, -1, "DP-1")
	all := []cwsWorkspace{occupiedWS(3, 2, "DP-2"), unnumbered, occupiedWS(1, 2, "DP-1"), occupiedWS(2, 1, "DP-2")}
	if got := cwsCollectDisplayed(all, cwsFilter{}); !reflect.DeepEqual(wsIDs(got), []uint64{1, 9, 2, 3}) {
		t.Errorf("ids = %v, want output order, number order, unnumbered last", wsIDs(got))
	}
}

func TestCwsMonitorSpecific(t *testing.T) {
	all := []cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 1, "DP-2"), {id: 3, num: 1}}
	if got := cwsCollectDisplayed(all, cwsFilter{monitorSpecific: true, barMonitor: "DP-1"}); !reflect.DeepEqual(wsIDs(got), []uint64{1}) {
		t.Errorf("scoped = %v, want DP-1's only (an outputless workspace is not on any bar)", wsIDs(got))
	}
	// Scoping off, or no bar monitor, shows everything.
	if got := cwsCollectDisplayed(all, cwsFilter{monitorSpecific: false, barMonitor: "DP-1"}); len(got) != 3 {
		t.Errorf("unscoped = %v", wsIDs(got))
	}
	if got := cwsCollectDisplayed(all, cwsFilter{monitorSpecific: true}); len(got) != 3 {
		t.Errorf("no bar monitor = %v", wsIDs(got))
	}
}

func TestCwsLabelStrategies(t *testing.T) {
	for _, tc := range []struct {
		num      int
		name     string
		hasName  bool
		strategy config.LabelStrategy
		want     string
		ok       bool
	}{
		{3, "web", true, config.LabelIndex, "3", true},
		{3, "web", true, config.LabelNameOrIndex, "web", true},
		{3, "", false, config.LabelNameOrIndex, "3", true},
		{3, "", false, config.LabelNameOnly, "", false},
		{3, "web", true, config.LabelIndexAndName, "3: web", true},
		{3, "", false, config.LabelIndexAndName, "3", true},
		// sway's unnumbered workspaces
		{-1, "chat", true, config.LabelIndex, "", false},
		{-1, "chat", true, config.LabelIndexAndName, "chat", true},
		{-1, "", false, config.LabelIndexAndName, "", false},
	} {
		got, ok := cwsLabel(tc.num, tc.name, tc.hasName, tc.strategy)
		if got != tc.want || ok != tc.ok {
			t.Errorf("label(%d, %q, %s) = %q %v, want %q %v", tc.num, tc.name, tc.strategy, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCwsStylePrefersNameThenID(t *testing.T) {
	m := map[string]config.WorkspaceStyle{
		"web": {Label: new("W")},
		"5":   {Label: new("five")},
	}
	named := cwsWorkspace{id: 5, name: "web", hasName: true}
	if s, ok := cwsStyleFor(named, m); !ok || *s.Label != "W" {
		t.Errorf("named = %+v %v, want the name entry", s, ok)
	}
	unmatched := cwsWorkspace{id: 5, name: "mail", hasName: true}
	if s, ok := cwsStyleFor(unmatched, m); !ok || *s.Label != "five" {
		t.Errorf("fallback = %+v %v, want the id entry", s, ok)
	}
	if _, ok := cwsStyleFor(cwsWorkspace{id: 6}, m); ok {
		t.Error("no entry matched but a style came back")
	}
}

func TestCwsNameClassSanitizes(t *testing.T) {
	if got := cwsNameClass("2:web ü"); got != "ws-name-2_web__" {
		t.Errorf("class = %q", got)
	}
	if got := cwsIDClass(5); got != "ws-id-5" {
		t.Errorf("id class = %q", got)
	}
}

func TestCwsOverrideColorCascade(t *testing.T) {
	red, _ := config.ParseColorValue("#ff0000")
	green, _ := config.ParseColorValue("#00ff00")
	m := map[string]config.WorkspaceStyle{
		"5":   {Color: &red},
		"web": {Color: &green},
		"x":   {Icon: new("no-color")},
	}
	// A numeric key reaches the id class; later keys win.
	if c, ok := cwsOverrideColor([]string{"ws-id-5"}, m); !ok || c != red {
		t.Errorf("id match = %v %v", c, ok)
	}
	if c, ok := cwsOverrideColor([]string{"ws-id-5", "ws-name-web"}, m); !ok || c != green {
		t.Errorf("both match = %v %v, want the later key (web)", c, ok)
	}
	// A numeric key also reaches a numeric name.
	if c, ok := cwsOverrideColor([]string{"ws-id-99", "ws-name-5"}, m); !ok || c != red {
		t.Errorf("numeric name = %v %v", c, ok)
	}
	if _, ok := cwsOverrideColor([]string{"ws-id-7", "ws-name-x"}, m); ok {
		t.Error("a colorless entry produced an override")
	}
}

func TestCwsAppIconsDedupeOrderAndUrgency(t *testing.T) {
	cfg := config.DefaultsNiriWorkspaces().View()
	windows := []cwsWindow{
		{id: 3, workspace: 1, hasWorkspace: true, app: appicons.Window{AppID: "firefox", HasAppID: true}, order: [2]int{2, 1}},
		{id: 1, workspace: 1, hasWorkspace: true, app: appicons.Window{AppID: "foot", HasAppID: true}, order: [2]int{1, 1}},
		{id: 2, workspace: 1, hasWorkspace: true, app: appicons.Window{AppID: "firefox", HasAppID: true}, order: [2]int{3, 1}, urgent: true},
		{id: 9, workspace: 2, hasWorkspace: true},
	}
	on := cwsWindowsOn(windows, 1)
	if len(on) != 3 || on[0].id != 1 || on[1].id != 3 || on[2].id != 2 {
		t.Fatalf("windows on 1 = %+v, want layout order", on)
	}
	icons := cwsCollectAppIcons(on, cfg, map[uint64]bool{2: true})
	if len(icons) != 2 || !reflect.DeepEqual(icons[1].windowIDs, []uint64{3, 2}) || !icons[1].urgent || icons[0].urgent {
		t.Fatalf("deduped = %+v", icons)
	}
	cfg.AppIconsDedupe = false
	if icons := cwsCollectAppIcons(on, cfg, nil); len(icons) != 3 {
		t.Fatalf("no dedupe = %+v, want one icon per window", icons)
	}
}

func TestCwsBuildModelsBlinkGatesUrgency(t *testing.T) {
	cfg := config.DefaultsSwayWorkspaces().View()
	urgent := occupiedWS(1, 1, "DP-1")
	urgent.urgent = true
	all := []cwsWorkspace{urgent}
	on := cwsBuildModels(cwsWorkspaces, all, nil, cfg, "DP-1", false, true)
	if !hasClass(on[0].classes, "urgent") {
		t.Errorf("blink on: classes %v miss urgent", on[0].classes)
	}
	off := cwsBuildModels(cwsWorkspaces, all, nil, cfg, "DP-1", false, false)
	if hasClass(off[0].classes, "urgent") {
		t.Errorf("blink off: classes %v carry urgent", off[0].classes)
	}
	cfg.UrgentShow = false
	if m := cwsBuildModels(cwsWorkspaces, all, nil, cfg, "DP-1", false, true); hasClass(m[0].classes, "urgent") {
		t.Error("urgent-show = false still marks the class")
	}
	cfg.UrgentShow, cfg.UrgentMode = true, config.UrgentApplication
	if m := cwsBuildModels(cwsWorkspaces, all, nil, cfg, "DP-1", false, true); !hasClass(m[0].classes, "urgent-application") {
		t.Error("urgent-mode application misses its class")
	}
}

func TestCwsClassesMatchComputeCSSClasses(t *testing.T) {
	cfg := config.DefaultsNiriWorkspaces().View()
	ws := occupiedWS(5, 2, "DP-1")
	ws.active, ws.focused, ws.name, ws.hasName = true, true, "web", true
	m := cwsBuildModels(cwsWorkspaces, []cwsWorkspace{ws}, nil, cfg, "", true, false)
	want := []string{"workspace", "active", "focused", "indicator-background", "vertical", "ws-id-5", "ws-name-web"}
	if !reflect.DeepEqual(m[0].classes, want) {
		t.Errorf("classes = %v, want %v", m[0].classes, want)
	}
}

func TestCwsShowRules(t *testing.T) {
	cfg := config.DefaultsNiriWorkspaces().View()
	labelled := cwsButtonModel{label: "1", hasLabel: true}
	if !labelled.showLabel(cfg.DisplayMode) || labelled.showIcon(cfg.DisplayMode) || labelled.showDivider(cfg) {
		t.Error("a labelled button without app icons: label only")
	}
	iconed := cwsButtonModel{label: "1", hasLabel: true, icon: "ld-globe-symbolic"}
	if iconed.showLabel(cfg.DisplayMode) || !iconed.showIcon(cfg.DisplayMode) {
		t.Error("a mapped icon replaces the label")
	}
	if iconed.showIcon(config.DisplayModeNone) || labelled.showLabel(config.DisplayModeNone) {
		t.Error("display-mode none shows something")
	}
	cfg.AppIconsShow = true
	if !labelled.showDivider(cfg) {
		t.Error("app icons on: the divider shows beside the label")
	}
	cfg.Divider = ""
	if labelled.showDivider(cfg) {
		t.Error("an empty divider shows")
	}
}

// fakeCws records the actions the bindings reach.
type fakeCws struct {
	workspaces []cwsWorkspace
	windows    []cwsWindow
	calls      chan string
	closed     bool
}

func newFakeCws(workspaces []cwsWorkspace, windows []cwsWindow) *fakeCws {
	return &fakeCws{workspaces: workspaces, windows: windows, calls: make(chan string, 16)}
}

func (f *fakeCws) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	return f.workspaces, f.windows, nil
}

func (f *fakeCws) focus(_ context.Context, ws cwsWorkspace) error {
	f.calls <- "focus:" + cwsIDClass(ws.id)
	return nil
}
func (f *fakeCws) focusNext(context.Context) error     { f.calls <- "next"; return nil }
func (f *fakeCws) focusPrevious(context.Context) error { f.calls <- "previous"; return nil }
func (f *fakeCws) focusLast(context.Context) error     { f.calls <- "last"; return nil }
func (f *fakeCws) subscribe() (<-chan struct{}, func(), error) {
	return make(chan struct{}), func() {}, nil
}
func (f *fakeCws) close() { f.closed = true }

func (f *fakeCws) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-f.calls:
		if got != want {
			t.Fatalf("call = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no call, want %q", want)
	}
}

func (f *fakeCws) expectNone(t *testing.T) {
	t.Helper()
	select {
	case got := <-f.calls:
		t.Fatalf("unexpected call %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func newTestCws(t *testing.T, cfg config.CompositorWorkspacesConfig, backend cwsBackend) *cwsModule {
	t.Helper()
	ctx := newTestContext(t, config.Defaults())
	ctx.Connector = "DP-1"
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, cfg, backend)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCwsModuleBuildsOneSegmentPerDisplayedWorkspace(t *testing.T) {
	active := occupiedWS(1, 1, "DP-1")
	active.active = true
	f := newFakeCws([]cwsWorkspace{active, occupiedWS(2, 2, "DP-1"), emptyWS(3, 3, "DP-1"), occupiedWS(4, 1, "HDMI-A-1")}, nil)
	m := newTestCws(t, config.DefaultsNiriWorkspaces().View(), f)
	// niri hides the trailing empty 3; 4 is another output's.
	if n := m.segs.Count(); n != 2 {
		t.Fatalf("segments = %d, want 2", n)
	}
	if !widget.HasClass(m.root, "workspaces") || !widget.HasClass(m.root, "niri") {
		t.Error("the row misses the workspaces/niri classes")
	}
	if m.segs.Active() != 0 || !widget.HasClass(m.segs.Segment(0), "active") || widget.HasClass(m.segs.Segment(1), "active") {
		t.Errorf("active segment = %d, want the first alone", m.segs.Active())
	}
	for _, c := range []string{"workspace", "occupied", "ws-id-2"} {
		if !widget.HasClass(m.segs.Segment(1), c) {
			t.Errorf("the second segment misses %q", c)
		}
	}
	m.Stop()
	if !f.closed {
		t.Error("Stop left the backend open")
	}
}

// A refresh moves the active segment and the state classes with the
// workspaces: a class the old state put on a segment does not stay.
func TestCwsRefreshMovesTheStates(t *testing.T) {
	one, two := occupiedWS(1, 1, "DP-1"), emptyWS(2, 2, "DP-1")
	one.active, one.focused = true, true
	f := newFakeCws([]cwsWorkspace{one, two, occupiedWS(3, 3, "DP-1")}, nil)
	m := newTestCws(t, config.DefaultsSwayWorkspaces().View(), f)
	one.active, one.focused, two.active, two.focused, two.hasWindows = false, false, true, true, true
	f.workspaces = []cwsWorkspace{one, two, emptyWS(5, 5, "DP-1")}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	first, second, third := m.segs.Segment(0), m.segs.Segment(1), m.segs.Segment(2)
	if m.segs.Active() != 1 || widget.HasClass(first, "active") || widget.HasClass(first, "focused") || !widget.HasClass(second, "focused") {
		t.Errorf("active = %d, first %v, second %v; want the second alone active and focused", m.segs.Active(), first.Classes(), second.Classes())
	}
	if widget.HasClass(third, "occupied") || widget.HasClass(third, "ws-id-3") || !widget.HasClass(third, "empty") || !widget.HasClass(third, "ws-id-5") {
		t.Errorf("the third segment, now workspace 5 and empty, carries %v", third.Classes())
	}
}

// cwsBar is a styled top bar holding m alone, settled at its natural
// size; it returns the bar and its size.
func cwsBar(t *testing.T, ctx ModuleContext, m Module) (*widget.Bar, widget.Size) {
	t.Helper()
	factories["test-cws"] = func(ModuleContext) (Module, error) { return m, nil }
	defer delete(factories, "test-cws")
	bar := buildRoot(ctx, config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "test-cws"}}}, "DP-1")
	size := settleBar(bar, widget.Size{W: 600, H: 200})
	return bar, size
}

// paintCwsBar paints bar at size and reports, per segment, whether it
// holds a pixel of exactly color c, and the color at its rest point
// (the left of its middle, clear of the content).
func paintCwsBar(m *cwsModule, bar *widget.Bar, size widget.Size) (has func(i int, c render.Color) bool, rest func(i int) render.Color) {
	bar.Measure(widgetConstraintsMax(size.W, size.H))
	bar.Arrange(render.Rect{W: size.W, H: size.H})
	widget.CollectDamage(bar)
	data := make([]byte, render.Stride(size.W)*size.H)
	bar.Paint(render.New(data, render.Stride(size.W), size.W, size.H))
	has = func(i int, c render.Color) bool {
		r := m.segs.Segment(i).Bounds()
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if pixelAt(data, size.W, x, y) == c {
					return true
				}
			}
		}
		return false
	}
	rest = func(i int) render.Color {
		r := m.segs.Segment(i).Bounds()
		return pixelAt(data, size.W, r.X+3, r.Y+r.H/2)
	}
	return has, rest
}

// workspaces.css colors the segments from the config: the active one
// filled in active-color under fg-on-accent ink, the occupied and
// empty ones in their colors on the container, and a workspace-map
// color in place of the state color.
func TestCwsPaintsTheConfiguredColors(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.Connector = "DP-1"
	palette := ctx.Theme.RenderPalette()
	resolve := func(cv config.ColorValue) render.Color { c, _ := styling.ResolveColor(cv, palette); return c }
	wcfg := config.DefaultsNiriWorkspaces().View()
	wcfg.HideTrailingEmpty = false
	wcfg.EmptyColor = mustToken(config.TokenYellow)
	red, _ := config.ParseColorValue("#ff0000")
	wcfg.WorkspaceMap = map[string]config.WorkspaceStyle{"4": {Color: &red}}
	active := occupiedWS(1, 1, "DP-1")
	active.active = true
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, newFakeCws([]cwsWorkspace{active, occupiedWS(2, 2, "DP-1"), emptyWS(3, 3, "DP-1"), occupiedWS(4, 4, "DP-1")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	bar, size := cwsBar(t, ctx, m)
	has, rest := paintCwsBar(m, bar, size)
	accent, onAccent := resolve(wcfg.ActiveColor), resolve(mustToken(config.TokenFgOnAccent))
	occupied, empty := resolve(wcfg.OccupiedColor), resolve(wcfg.EmptyColor)
	if got := rest(0); got != accent || !has(0, onAccent) {
		t.Errorf("active: filled %#08x with fg-on-accent ink %v, want the active color %#08x", uint32(got), has(0, onAccent), uint32(accent))
	}
	container := resolve(wcfg.ContainerBgColor)
	if got := rest(1); got != container || !has(1, occupied) {
		t.Errorf("occupied: rest %#08x, occupied ink %v; want the container %#08x and the occupied color", uint32(got), has(1, occupied), uint32(container))
	}
	if !has(2, empty) || has(2, occupied) {
		t.Errorf("empty: empty ink %v, occupied ink %v; want the empty color alone", has(2, empty), has(2, occupied))
	}
	if !has(3, render.RGB(0xff, 0, 0)) || has(3, occupied) {
		t.Errorf("mapped: red ink %v, occupied ink %v; want the workspace-map color in place of the occupied one", has(3, render.RGB(0xff, 0, 0)), has(3, occupied))
	}
}

// The underline indicator marks the active workspace with a line along
// its bottom in active-color, over no fill, its label in the color.
func TestCwsUnderlinesTheActiveWorkspace(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.Connector = "DP-1"
	palette := ctx.Theme.RenderPalette()
	wcfg := config.DefaultsNiriWorkspaces().View()
	wcfg.ActiveIndicator = config.ActiveUnderline
	active := occupiedWS(1, 1, "DP-1")
	active.active = true
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, newFakeCws([]cwsWorkspace{active}, nil))
	if err != nil {
		t.Fatal(err)
	}
	bar, size := cwsBar(t, ctx, m)
	accent, _ := styling.ResolveColor(wcfg.ActiveColor, palette)
	bar.Measure(widgetConstraintsMax(size.W, size.H))
	bar.Arrange(render.Rect{W: size.W, H: size.H})
	data := make([]byte, render.Stride(size.W)*size.H)
	bar.Paint(render.New(data, render.Stride(size.W), size.W, size.H))
	r := m.segs.Segment(0).Bounds()
	if got := pixelAt(data, size.W, r.X+r.W/2, r.Y+r.H-1); got != accent {
		t.Errorf("bottom edge %#08x, want the active color %#08x", uint32(got), uint32(accent))
	}
	if got := pixelAt(data, size.W, r.X+3, r.Y+r.H/2); got == accent {
		t.Error("the underlined workspace is filled")
	}
	if got := pixelAt(data, size.W, r.X+r.W/2, r.Y+1); got == accent {
		t.Error("the underline runs along the top too")
	}
}

// A segment shades in a little active-color under the pointer and
// returns to rest when it leaves; the active one keeps its fill.
func TestCwsSegmentShadesUnderThePointer(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	ctx.Connector = "DP-1"
	active := occupiedWS(1, 1, "DP-1")
	active.active = true
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, config.DefaultsNiriWorkspaces().View(), newFakeCws([]cwsWorkspace{active, occupiedWS(2, 2, "DP-1")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	bar, size := cwsBar(t, ctx, m)
	_, rest := paintCwsBar(m, bar, size)
	before, activeRest := rest(1), rest(0)
	router, _ := treetest.Pointer(bar, m.segs.Segment(1), size.W, size.H)
	if _, rest = paintCwsBar(m, bar, size); rest(1) == before {
		t.Errorf("the hovered segment paints %#08x, as at rest", uint32(rest(1)))
	}
	router.Move(widget.Point{X: m.segs.Segment(0).Bounds().X + 2, Y: size.H / 2})
	if _, rest = paintCwsBar(m, bar, size); rest(1) != before || rest(0) != activeRest {
		t.Errorf("after the pointer moved on: %#08x (rest %#08x), active %#08x (rest %#08x)", uint32(rest(1)), uint32(before), uint32(rest(0)), uint32(activeRest))
	}
}

// The segments are at least --bar-space-lg square, lys-ui's dots grown
// to hold a workspace, however small its content; a larger content
// grows them.
func TestCwsSegmentMinSize(t *testing.T) {
	for _, tc := range []struct {
		label, pad float32
		atLeast    int
	}{{4, 0, 24}, {4, 30, 60}} {
		ctx := styledContext(t, config.Defaults())
		ctx.Connector = "DP-1"
		wcfg := config.DefaultsNiriWorkspaces().View()
		wcfg.LabelSize = config.Size{Value: tc.label, Unit: config.SizePixels}
		wcfg.WorkspacePad = config.Size{Value: tc.pad, Unit: config.SizePixels}
		m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, newFakeCws([]cwsWorkspace{occupiedWS(1, 1, "DP-1")}, nil))
		if err != nil {
			t.Fatal(err)
		}
		cwsBar(t, ctx, m)
		if r := m.segs.Segment(0).Bounds(); r.W < tc.atLeast || r.H < 24 || (tc.atLeast == 24 && (r.W != 24 || r.H != 24)) {
			t.Errorf("label %vpx, padding %vpx: segment %v, want at least %dx24", tc.label, tc.pad, r, tc.atLeast)
		}
	}
}

// Down a side bar, as wide as its widest module, the segments stand
// centered across it and each workspace's label centered in its
// segment.
func TestCwsSegmentsCenterInASideBar(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.Location = config.LocationLeft
	ctx := styledContext(t, cfg)
	ctx.Connector = "DP-1"
	palette := ctx.Theme.RenderPalette()
	wcfg := config.DefaultsNiriWorkspaces().View()
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, newFakeCws([]cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	factories["test-cws"] = func(ModuleContext) (Module, error) { return m, nil }
	defer delete(factories, "test-cws")
	bar := buildRoot(ctx, config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "test-cws"}, {Module: "clock"}}}, "DP-1")
	size := settleBar(bar, widget.Size{W: 200, H: 400})
	bar.Measure(widgetConstraintsMax(size.W, size.H))
	bar.Arrange(render.Rect{W: size.W, H: size.H})
	data := make([]byte, render.Stride(size.W)*size.H)
	bar.Paint(render.New(data, render.Stride(size.W), size.W, size.H))
	box, seg := m.root.Bounds(), m.segs.Segment(1).Bounds()
	if mid, segMid := box.X+box.W/2, seg.X+seg.W/2; seg.W >= box.W || abs(mid-segMid) > 1 {
		t.Errorf("segment %v in the row %v: want it narrower and centered across the bar", seg, box)
	}
	occupied, _ := styling.ResolveColor(wcfg.OccupiedColor, palette)
	left, right := seg.X+seg.W, seg.X
	for y := seg.Y; y < seg.Y+seg.H; y++ {
		for x := seg.X; x < seg.X+seg.W; x++ {
			if pixelAt(data, size.W, x, y) == occupied {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if right < left || abs((left+right+1)/2-(seg.X+seg.W/2)) > 2 {
		t.Errorf("label ink spans x %d..%d in the segment %v, want it centered", left, right, seg)
	}
}

// classesIn lists the classes of every widget under w, in tree order.
func classesIn(w widget.Widget) []string {
	var out []string
	walkTree(w, func(w widget.Widget) bool {
		if c, ok := w.(interface{ Classes() []string }); ok {
			out = append(out, c.Classes()...)
		}
		return true
	})
	return out
}

// A segment holds the workspace's identity and, with app-icons-show,
// its app icons: the label with the divider after it, a mapped icon in
// the label's place (hyprland's as its custom icon), an urgent app's
// icon marked urgent, and the empty icon for a workspace without apps.
func TestCwsSegmentContent(t *testing.T) {
	cfg := config.DefaultsNiriWorkspaces().View()
	m := newTestCws(t, cfg, newFakeCws(nil, nil))
	labelled := cwsButtonModel{label: "1", hasLabel: true}
	if got := classesIn(m.segmentContent(labelled)); !slices.Contains(got, "workspace-label") || slices.Contains(got, "workspace-icons") || slices.Contains(got, "workspace-divider") {
		t.Errorf("app icons off: content classes %v, want the label alone", got)
	}
	m.cfg.AppIconsShow = true
	apps := cwsButtonModel{label: "1", hasLabel: true, appIcons: []cwsAppIcon{{name: "foot"}, {name: "firefox", urgent: true}}}
	got := classesIn(m.segmentContent(apps))
	for _, c := range []string{"workspace-label", "workspace-divider", "workspace-icons", "urgent"} {
		if !slices.Contains(got, c) {
			t.Errorf("app icons on: content classes %v miss %q", got, c)
		}
	}
	if slices.Contains(got, "workspace-icon-empty") || strings.Count(strings.Join(got, " "), "workspace-icon ") != 2 {
		t.Errorf("two apps: content classes %v, want their two icons and no empty one", got)
	}
	if got := classesIn(m.segmentContent(cwsButtonModel{label: "1", hasLabel: true})); !slices.Contains(got, "workspace-icon-empty") {
		t.Errorf("no apps: content classes %v, want the empty icon", got)
	}
	m.cfg.AppIconsShow = false
	mapped := cwsButtonModel{label: "1", hasLabel: true, icon: "ld-globe-symbolic"}
	if got := classesIn(m.segmentContent(mapped)); slices.Contains(got, "workspace-label") || !slices.Contains(got, "workspace-icon") {
		t.Errorf("mapped icon: content classes %v, want the icon in the label's place", got)
	}
	mapped.flavor = cwsHyprland
	if got := classesIn(m.segmentContent(mapped)); !slices.Contains(got, "workspace-custom-icon") || slices.Contains(got, "workspace-icon") {
		t.Errorf("hyprland mapped icon: content classes %v, want the custom icon", got)
	}
}

func TestCwsClicksRouteThroughTheRouter(t *testing.T) {
	f := newFakeCws([]cwsWorkspace{occupiedWS(1, 1, "DP-1"), occupiedWS(2, 2, "DP-1")}, nil)
	cfg := config.DefaultsSwayWorkspaces().View()
	cfg.Click.MiddleClick = config.ParseWorkspaceClickAction("focus:last")
	m := newTestCws(t, cfg, f)
	second := m.segs.Segment(1)
	router, p := treetest.Pointer(m.root, second, 500, 40)
	if !second.Hovered {
		t.Error("routed hover missed the segment")
	}
	router.Press(widget.BTNLeft, p, 0)
	router.Release(widget.BTNLeft, p)
	f.expect(t, "focus:ws-id-2")

	router.Press(widget.BTNMiddle, p, 0)
	f.expect(t, "last")
	// Right click defaults to none.
	router.Press(widget.BTNRight, p, 0)
	f.expectNone(t)

	router.Axis(0, 1, 0)
	f.expect(t, "next")
	router.Axis(0, -1, 0)
	f.expect(t, "previous")
}

func TestCwsScrollIgnoresFocusThis(t *testing.T) {
	f := newFakeCws([]cwsWorkspace{occupiedWS(1, 1, "DP-1")}, nil)
	cfg := config.DefaultsSwayWorkspaces().View()
	cfg.Click.ScrollDown = config.ParseWorkspaceClickAction("focus:this")
	cfg.Click.ScrollUp = config.ParseWorkspaceClickAction("")
	m := newTestCws(t, cfg, f)
	behind := widget.NewBox(widget.Row, 0, 0)
	behind.Append(m.root, false)
	reached := 0
	behind.SetOnScroll(func(widget.ScrollEvent) bool { reached++; return true })
	router, _ := treetest.Pointer(behind, m.segs.Segment(0), 500, 40)
	router.Axis(0, 1, 0)
	router.Axis(0, -1, 0)
	if reached != 0 {
		t.Errorf("%d scroll steps passed the segment (want Propagation::Stop with inert bindings)", reached)
	}
	f.expectNone(t)
}

func TestCwsBlinkRunsOnlyWhileUrgent(t *testing.T) {
	urgent := occupiedWS(1, 1, "DP-1")
	urgent.urgent = true
	f := newFakeCws([]cwsWorkspace{urgent}, nil)
	m := newTestCws(t, config.DefaultsNiriWorkspaces().View(), f)
	if m.blinkStop == nil || !m.blinkOn {
		t.Fatal("urgent workspace: blink not started on")
	}
	if seg := m.segs.Segment(0); !widget.HasClass(seg, "urgent") || !widget.HasClass(seg, "occupied") {
		t.Errorf("blink on: segment carries %v, want urgent and still occupied", seg.Classes())
	}
	m.blinkTick()
	if m.blinkOn || widget.HasClass(m.segs.Segment(0), "urgent") {
		t.Error("tick did not toggle the pulse off")
	}
	// Urgency clears: the blink stops and stays off.
	f.workspaces = []cwsWorkspace{occupiedWS(1, 1, "DP-1")}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if m.blinkStop != nil || m.blinkOn {
		t.Error("blink still running without urgency")
	}
	// An urgent window on a displayed workspace also starts it; one on
	// a hidden workspace does not.
	f.windows = []cwsWindow{{id: 7, workspace: 99, hasWorkspace: true, urgent: true}}
	_ = m.refresh()
	if m.blinkStop != nil {
		t.Error("an urgent window on an undisplayed workspace started the blink")
	}
	f.windows[0].workspace = 1
	_ = m.refresh()
	if m.blinkStop == nil {
		t.Error("an urgent window on a displayed workspace did not start the blink")
	}
}

// The urgent pulse fades the whole segment while it is on, and leaves
// it whole while it is off.
func TestCwsUrgentPulseFadesTheSegment(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	ctx.Connector = "DP-1"
	palette := ctx.Theme.RenderPalette()
	wcfg := config.DefaultsNiriWorkspaces().View()
	urgent := occupiedWS(1, 1, "DP-1")
	urgent.urgent = true
	m, err := newCwsModule(ctx, "niri", cwsWorkspaces, wcfg, newFakeCws([]cwsWorkspace{urgent}, nil))
	if err != nil {
		t.Fatal(err)
	}
	occupied, _ := styling.ResolveColor(wcfg.OccupiedColor, palette)
	bar, size := cwsBar(t, ctx, m)
	if has, _ := paintCwsBar(m, bar, size); has(0, occupied) {
		t.Error("pulse on: the label paints at full strength")
	}
	m.blinkTick()
	if has, _ := paintCwsBar(m, bar, size); !has(0, occupied) {
		t.Error("pulse off: the label is still faded")
	}
}

func TestCwsBorderShowUsesTheButtonBorder(t *testing.T) {
	cfg := config.DefaultsNiriWorkspaces().View()
	m := newTestCws(t, cfg, newFakeCws(nil, nil))
	if widget.HasClass(m.root, "border-all") || !strings.Contains(m.root.InlineStyle(), "--ws-border-width: 0px") {
		t.Errorf("border-show off: classes %v, style %q; want no border", m.root.Classes(), m.root.InlineStyle())
	}
	cfg.BorderShow = true
	m = newTestCws(t, cfg, newFakeCws(nil, nil))
	if !widget.HasClass(m.root, "border-all") || !strings.Contains(m.root.InlineStyle(), "--ws-border-width: 1px") {
		t.Errorf("border-show on: classes %v, style %q; want the button border at the default all location", m.root.Classes(), m.root.InlineStyle())
	}
}

func TestCwsModuleIsNotWrappedInButtonChrome(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	row := widget.NewBox(widget.Row, 0, 0)
	m := newTestCws(t, config.DefaultsNiriWorkspaces().View(), newFakeCws(nil, nil))
	factories["test-cws"] = func(ModuleContext) (Module, error) { return m, nil }
	defer delete(factories, "test-cws")
	if err := appendModule(row, config.BarItem{Module: "test-cws"}, ctx); err != nil {
		t.Fatal(err)
	}
	if got := row.Children()[0]; got != widget.Widget(m.root) {
		t.Errorf("appended %T, want the container itself", got)
	}
}

func TestCwsFactoriesRequireTheirCompositor(t *testing.T) {
	t.Setenv("SWAYSOCK", "")
	t.Setenv("NIRI_SOCKET", "")
	ctx := newTestContext(t, config.Defaults())
	for _, name := range []string{"sway-workspaces", "niri-workspaces"} {
		if _, err := Create(name, ctx); err == nil {
			t.Errorf("%s without its compositor: want an error", name)
		}
	}
}
