package bar

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
)

// fakeGeometry is lys-wm's window geometry as the test sets it.
type fakeGeometry struct{ windows []lyswm.Window }

func (g *fakeGeometry) Windows() []lyswm.Window { return g.windows }

type dockRig struct {
	d        *dock
	src      *fakeToplevels
	geom     *fakeGeometry
	timers   []*pendingTimer
	launched []string
}

func newDockRig(t *testing.T, cfg config.DockConfig, windows ...*fakeWindow) *dockRig {
	t.Helper()
	ctx := newTestContext(t, config.Defaults())
	r := &dockRig{src: &fakeToplevels{list: windows}, geom: &fakeGeometry{}}
	ctx.Tasks = newTaskHub(r.src, func(fn func()) { fn() })
	ctx.Tasks.apps.goOff = func(func()) {}
	ctx.Tasks.apps.known["firefox"] = taskApp{found: true, name: "Firefox", icon: "firefox"}
	ctx.Tasks.geom = r.geom
	out := &app.Output{Name: "DP-1", LogicalW: 1000, LogicalH: 800}
	r.d = newDock(ctx, cfg, out, func(d time.Duration, fn func()) func() {
		t := &pendingTimer{d: d, fn: fn}
		r.timers = append(r.timers, t)
		return func() { t.stopped = true }
	})
	r.d.launch = func(a taskApp) { r.launched = append(r.launched, a.name) }
	r.d.attach(namedHost{name: "dock"})
	t.Cleanup(r.d.close)
	return r
}

func (r *dockRig) apps() []string {
	out := make([]string, len(r.d.shown))
	for i, a := range r.d.shown {
		out[i] = a.appID
	}
	return out
}

// The dock lists the pinned apps first, in their order, then each other
// app with a window on its output in the order its first window
// appeared; a window on another output is not its.
func TestTheDockListsPinnedAppsThenOpenOnes(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.Pinned = []string{"firefox", "org.gnome.Nautilus", "firefox"}
	foot := &fakeWindow{appID: "foot", outputs: []string{"DP-1"}}
	away := &fakeWindow{appID: "mpv", outputs: []string{"HDMI-A-1"}}
	fox := &fakeWindow{appID: "firefox", outputs: []string{"DP-1"}}
	r := newDockRig(t, cfg, foot, away, fox)
	if got, want := r.apps(), []string{"firefox", "org.gnome.Nautilus", "foot"}; !slices.Equal(got, want) {
		t.Fatalf("apps = %v, want %v", got, want)
	}
	if e := r.d.entries["firefox"]; !e.HasClass("running") || e.icon != "firefox" || e.TooltipText() != "Firefox" {
		t.Errorf("the open pinned app shows running %v, icon %q, tooltip %q", e.HasClass("running"), e.icon, e.TooltipText())
	}
	if e := r.d.entries["org.gnome.Nautilus"]; e.HasClass("running") || e.indicator.Visible() {
		t.Error("a closed pinned app shows running")
	}
}

// A click on a closed pinned app launches it; on an open app it brings
// up the first window shown, then cycles through the app's windows; with
// every window minimized it brings the first back and marks the entry
// minimized meanwhile.
func TestADockClickLaunchesFocusesCyclesAndRestores(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.Pinned = []string{"firefox"}
	a := &fakeWindow{appID: "foot", outputs: []string{"DP-1"}, minimized: true}
	b := &fakeWindow{appID: "foot", outputs: []string{"DP-1"}}
	r := newDockRig(t, cfg, a, b)
	r.d.entries["firefox"].ClickAt(widget.Point{})
	if !slices.Equal(r.launched, []string{"Firefox"}) {
		t.Fatalf("launched %v, want Firefox once", r.launched)
	}
	foot := r.d.entries["foot"]
	foot.ClickAt(widget.Point{})
	if b.activations != 1 || a.activations != 0 {
		t.Fatalf("activations a %d b %d, want b, the one shown", a.activations, b.activations)
	}
	b.activated = true
	r.src.set(a, b)
	r.d.entries["foot"].ClickAt(widget.Point{})
	if a.activations != 1 {
		t.Fatal("a second click did not cycle to the next window")
	}
	a.minimized, b.minimized, b.activated = true, true, false
	r.src.set(a, b)
	if !foot.HasClass("minimized") {
		t.Fatal("an app with every window minimized is not marked")
	}
	foot.ClickAt(widget.Point{})
	if a.activations != 2 || len(r.launched) != 1 {
		t.Fatalf("activations %d, launched %v: want the first window back and nothing launched", a.activations, r.launched)
	}
}

// Every window of an app names its app's entry as its rectangle, over
// the bar module's.
func TestEveryWindowMinimizesIntoItsAppsDockEntry(t *testing.T) {
	a := &fakeWindow{appID: "foot", outputs: []string{"DP-1"}}
	b := &fakeWindow{appID: "foot", outputs: []string{"DP-1"}}
	r := newDockRig(t, config.DefaultsDock(), a, b)
	module := r.d.hub.windows.owner()
	module.set(map[taskWindow]rectClaim{a: {priority: rectFromModule, output: "DP-1", host: namedHost{name: "bar"}, rect: render.Rect{W: 4, H: 4}}})
	r.d.panel.Measure(widget.Row, widget.Unknown)
	r.d.panel.Arrange(render.Rect{X: 400, Y: 740, W: 60, H: 60})
	entry := r.d.entries["foot"].Bounds()
	for _, w := range []*fakeWindow{a, b} {
		if last := w.rects[len(w.rects)-1]; last != (sentRect{namedHost{name: "dock"}, entry}) {
			t.Errorf("rectangle %+v, want the dock entry %+v", last, entry)
		}
	}
}

// An auto-hiding dock hides while a window shown on its output covers
// it, reveals under the pointer and hides again once the pointer leaves,
// and reveals when the window goes; each change is one transition, and a
// geometry change that changes nothing makes none. A dock that does not
// auto-hide never hides.
func TestAnAutoHidingDockHidesUnderAWindowAndRevealsAtTheEdge(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.AutoHide = true
	r := newDockRig(t, cfg, &fakeWindow{appID: "foot", outputs: []string{"DP-1"}})
	if r.d.hide.hidden {
		t.Fatal("the dock hides with nothing covering it")
	}
	r.d.surface.Measure(widget.Column, widget.Unknown)
	r.d.surface.Arrange(render.Rect{Y: 700, W: 1000, H: 100})
	top := 0
	r.geom.windows = []lyswm.Window{
		{ID: 1, Output: "DP-1", Rect: lyswm.Rect{Width: 1000, Height: 800}, Stack: &top},
		{ID: 2, Output: "HDMI-A-1", Rect: lyswm.Rect{Width: 1000, Height: 800}, Stack: &top},
	}
	r.d.hub.geometryChanged()
	r.d.hub.geometryChanged()
	if r.d.hide.hidden {
		t.Fatal("the dock hid as soon as a window covered it, before its delay")
	}
	r.fire()
	if !r.d.hide.hidden || r.d.panel.Visible() || !r.d.reveal.Visible() || r.d.hide.hides != 1 {
		t.Fatalf("covered: hidden %v, hides %d", r.d.hide.hidden, r.d.hide.hides)
	}
	r.d.surface.Measure(widget.Column, widget.Unknown)
	r.d.surface.Arrange(render.Rect{Y: 798, W: 1000, H: 2})
	strip := r.d.reveal.Bounds()
	pointer := &widget.Router{Root: r.d.surface}
	pointer.Move(widget.Point{X: strip.X + strip.W/2, Y: strip.Y + strip.H/2})
	if r.d.hide.hidden || r.d.hide.reveals != 1 {
		t.Fatalf("hovered: hidden %v, reveals %d", r.d.hide.hidden, r.d.hide.reveals)
	}
	pointer.Leave()
	if r.d.hide.hidden {
		t.Fatal("the dock hid the moment the pointer left it")
	}
	pointer.Move(widget.Point{X: strip.X + strip.W/2, Y: strip.Y + strip.H/2})
	r.fire()
	if r.d.hide.hidden || r.d.hide.hides != 1 {
		t.Fatalf("the pointer back within the delay: hidden %v, hides %d", r.d.hide.hidden, r.d.hide.hides)
	}
	pointer.Leave()
	r.fire()
	if !r.d.hide.hidden || r.d.hide.hides != 2 {
		t.Fatalf("left: hidden %v, hides %d", r.d.hide.hidden, r.d.hide.hides)
	}
	r.geom.windows[0].Minimized, r.geom.windows[0].Stack = true, nil
	r.d.hub.geometryChanged()
	if r.d.hide.hidden || r.d.hide.reveals != 2 || r.d.hide.hides != 2 {
		t.Fatalf("uncovered: hidden %v, hides %d reveals %d", r.d.hide.hidden, r.d.hide.hides, r.d.hide.reveals)
	}

	fixed := newDockRig(t, config.DefaultsDock())
	fixed.geom.windows = r.geom.windows[:1]
	fixed.geom.windows[0].Minimized, fixed.geom.windows[0].Stack = false, &top
	fixed.d.hub.geometryChanged()
	if fixed.d.hide.hidden || fixed.d.hide.hides != 0 {
		t.Fatal("a dock that does not auto-hide hid")
	}
}

// Without lys-wm's geometry an auto-hiding dock counts as covered and
// shows only under the pointer.
func TestAnAutoHidingDockWithoutGeometryShowsOnlyUnderThePointer(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.AutoHide = true
	r := newDockRig(t, cfg)
	r.d.hub.geom = nil
	r.d.hub.geometryChanged()
	r.fire()
	if !r.d.hide.hidden {
		t.Fatal("without geometry the dock stays shown")
	}
}

// A bottom dock is centered on its output's bottom edge, a side dock on
// its side; only an overlapping shown window covers it.
func TestTheDockAreaAndWhatCoversIt(t *testing.T) {
	out := lyswm.Rect{X: 1000, Width: 1000, Height: 800}
	size := widget.Size{W: 200, H: 60}
	if got := dockArea(config.DockEdgeBottom, out, size); got != (lyswm.Rect{X: 1400, Y: 740, Width: 200, Height: 60}) {
		t.Errorf("bottom = %+v", got)
	}
	if got := dockArea(config.DockEdgeLeft, out, size); got != (lyswm.Rect{X: 1000, Y: 370, Width: 200, Height: 60}) {
		t.Errorf("left = %+v", got)
	}
	if got := dockArea(config.DockEdgeRight, out, size); got != (lyswm.Rect{X: 1800, Y: 370, Width: 200, Height: 60}) {
		t.Errorf("right = %+v", got)
	}
	area := dockArea(config.DockEdgeBottom, out, size)
	top := 0
	above := lyswm.Window{Output: "DP-1", Rect: lyswm.Rect{X: 1000, Width: 1000, Height: 740}, Stack: &top}
	if covered([]lyswm.Window{above}, "DP-1", area) {
		t.Error("a window ending above the dock covers it")
	}
	above.Rect.Height = 741
	if !covered([]lyswm.Window{above}, "DP-1", area) {
		t.Error("a window reaching into the dock does not cover it")
	}
	hidden := above
	hidden.Stack = nil
	if covered([]lyswm.Window{hidden}, "DP-1", area) {
		t.Error("a window no output shows covers the dock")
	}
}

// A pinned app is found through its desktop entry off the loop, takes
// the entry's name and icon, and a click on it runs the entry's Exec.
// The private session bus keeps the launched process away from the
// user's systemd.
func TestADockLaunchesAPinnedAppFromItsDesktopEntry(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", dbustest.Start(t).Address)
	data := t.TempDir()
	marker := filepath.Join(data, "launched")
	entry := "[Desktop Entry]\nType=Application\nName=Touch Probe\nIcon=touch-probe\nExec=sh -c 'echo once >> " + marker + "'\n"
	if err := os.MkdirAll(filepath.Join(data, "applications"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "applications", "org.example.TouchProbe.desktop"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	cfg := config.DefaultsDock()
	cfg.Pinned = []string{"org.example.TouchProbe"}
	ctx := newTestContext(t, config.Defaults())
	ctx.Tasks = newTaskHub(&fakeToplevels{}, ctx.Invoke)
	var d *dock
	onHeadlessLoop(func() bool {
		d = newDock(ctx, cfg, &app.Output{Name: "DP-1"}, func(time.Duration, func()) func() {
			t.Error("a dock that does not auto-hide started a hide timer")
			return func() {}
		})
		return true
	})
	t.Cleanup(func() { onHeadlessLoop(func() bool { d.close(); return true }) })
	waitHeadless(t, "the desktop entry's name and icon", func() bool {
		e := d.entries["org.example.TouchProbe"]
		return e.TooltipText() == "Touch Probe" && e.icon == "touch-probe"
	})
	onHeadlessLoop(func() bool { d.entries["org.example.TouchProbe"].ClickAt(widget.Point{}); return true })
	waitHeadless(t, "the entry's Exec run once", func() bool {
		got, err := os.ReadFile(marker)
		return err == nil && string(got) == "once\n"
	})
}

// fire runs the rig's hide timers that are still live.
func (r *dockRig) fire() {
	timers := r.timers
	r.timers = nil
	for _, t := range timers {
		if !t.stopped {
			t.fn()
		}
	}
}

// Hovering an entry grows its icon to the zoom size and its neighbors
// halfway, leaving the rest; leaving the dock shrinks them all back.
// With hover-zoom off, or a zoom size no larger than the icons, nothing
// grows.
func TestHoveringADockEntryZoomsItAndItsNeighbors(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.Pinned = []string{"a", "b", "c", "d"}
	cfg.IconSize = config.Px(40)
	cfg.HoverZoomSize = config.Px(80)
	r := newDockRig(t, cfg)
	sizes := func() []int {
		out := make([]int, len(r.d.shown))
		for i, a := range r.d.shown {
			out[i] = r.d.entries[a.appID].px
		}
		return out
	}
	r.d.hoverEntry("b", true)
	if got := sizes(); !slices.Equal(got, []int{60, 80, 60, 40}) {
		t.Fatalf("hovering b sized the icons %v", got)
	}
	r.d.hoverEntry("b", false)
	r.d.hoverEntry("d", true)
	if got := sizes(); !slices.Equal(got, []int{40, 40, 60, 80}) {
		t.Fatalf("moving to d sized the icons %v", got)
	}
	r.d.hoverEntry("b", false)
	if got := sizes(); !slices.Equal(got, []int{40, 40, 60, 80}) {
		t.Fatalf("a late leave of b undid d's zoom: %v", got)
	}
	r.d.hoverEntry("d", false)
	if got := sizes(); !slices.Equal(got, []int{40, 40, 40, 40}) {
		t.Fatalf("leaving the dock left the icons %v", got)
	}

	for name, tweak := range map[string]func(*config.DockConfig){
		"hover-zoom off":       func(c *config.DockConfig) { c.HoverZoom = false },
		"zoom size not larger": func(c *config.DockConfig) { c.HoverZoomSize = config.Px(40) },
	} {
		off := cfg
		tweak(&off)
		r := newDockRig(t, off)
		r.d.hoverEntry("b", true)
		for _, a := range r.d.shown {
			if px := r.d.entries[a.appID].px; px != 40 {
				t.Fatalf("%s: hovering b sized %s to %d", name, a.appID, px)
			}
		}
		if along, across := r.d.zoom.growth(); along != 0 || across != 0 {
			t.Fatalf("%s: the dock keeps %dx%d headroom", name, along, across)
		}
	}
}

// A shown zooming dock keeps headroom for its zoomed entries and takes
// input only on its panel, so the headroom passes clicks through; a
// hidden one is its strip, input everywhere on it, as wide as the
// panel it last showed.
func TestAZoomingDockKeepsHeadroomAndTakesInputOnItsPanel(t *testing.T) {
	cfg := config.DefaultsDock()
	cfg.AutoHide = true
	cfg.Pinned = []string{"a", "b"}
	cfg.IconSize = config.Px(40)
	cfg.HoverZoomSize = config.Px(80)
	r := newDockRig(t, cfg)
	r.d.surface.Measure(widget.Column, widget.Unknown)
	r.d.surface.Arrange(render.Rect{W: 1000, H: 200})
	rest := r.d.size
	if rest.W == 0 || rest.H == 0 {
		t.Fatalf("the laid-out dock measured %v", rest)
	}
	along, across := r.d.zoom.growth()
	if got := r.d.surface.SizeRequest(); got != (widget.Size{W: rest.W + along, H: rest.H + across}) {
		t.Fatalf("the shown dock requests %v, want its %v rest plus %dx%d headroom", got, rest, along, across)
	}
	if got := r.d.surface.InputRegion(); len(got) != 1 || got[0] != r.d.panel.Bounds() {
		t.Fatalf("the shown dock takes input on %v, want only its panel %v", got, r.d.panel.Bounds())
	}
	if got := r.d.panel.Bounds(); got.Y+got.H != 200 || got.X+got.W/2 != 500 {
		t.Fatalf("the panel sits at %v, want centered on the bottom edge of 1000x200", got)
	}

	r.d.hoverEntry("a", true)
	r.d.surface.Measure(widget.Column, widget.Unknown)
	r.d.surface.Arrange(render.Rect{W: 1000, H: 200})
	if r.d.size != rest {
		t.Fatalf("zooming changed the resting size to %v from %v", r.d.size, rest)
	}
	r.d.hoverEntry("a", false)

	r.d.hide.setHovered(false)
	r.d.hub.geom = nil
	r.d.hub.geometryChanged()
	r.fire()
	if !r.d.hide.hidden {
		t.Fatal("the dock did not hide")
	}
	if got := r.d.surface.SizeRequest(); got != (widget.Size{}) {
		t.Fatalf("the hidden dock still requests %v", got)
	}
	if got := r.d.surface.InputRegion(); got != nil {
		t.Fatalf("the hidden dock limits input to %v", got)
	}
	if w := widget.SizeWithin(r.d.reveal, widget.Size{W: 1000, H: 200}).W; w != rest.W {
		t.Fatalf("the strip is %d wide, want the panel's %d", w, rest.W)
	}
}
