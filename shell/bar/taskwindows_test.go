package bar

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// fakeWindow is a taskWindow that records its requests.
type fakeWindow struct {
	title, appID string
	minimized    bool
	activated    bool
	outputs      []string
	activations  int
	closes       int
	rects        []sentRect
}

func (w *fakeWindow) Title() string             { return w.title }
func (w *fakeWindow) AppID() string             { return w.appID }
func (w *fakeWindow) Minimized() bool           { return w.minimized }
func (w *fakeWindow) Activated() bool           { return w.activated }
func (w *fakeWindow) OnOutput(name string) bool { return slices.Contains(w.outputs, name) }
func (w *fakeWindow) Activate()                 { w.activations++; w.minimized = false }
func (w *fakeWindow) Close()                    { w.closes++ }
func (w *fakeWindow) SetRectangle(host app.Host, r render.Rect) {
	w.rects = append(w.rects, sentRect{host: host, rect: r})
}

// fakeToplevels is a toplevelSource the test changes by hand.
type fakeToplevels struct {
	list    []*fakeWindow
	changed func()
}

func (s *fakeToplevels) windows() []taskWindow {
	out := make([]taskWindow, len(s.list))
	for i, w := range s.list {
		out[i] = w
	}
	return out
}

func (s *fakeToplevels) onChanged(fn func()) func() {
	s.changed = fn
	return func() { s.changed = nil }
}

func (s *fakeToplevels) set(list ...*fakeWindow) {
	s.list = list
	s.changed()
}

// Minimized windows list in the order they were minimized, not the order
// they appeared, filtered to an output unless every output is asked
// for; a window restored and minimized again goes to the end.
func TestMinimizedWindowsKeepTheOrderTheyWereMinimizedIn(t *testing.T) {
	a := &fakeWindow{title: "a", outputs: []string{"DP-1"}}
	b := &fakeWindow{title: "b", outputs: []string{"DP-1"}}
	c := &fakeWindow{title: "c", outputs: []string{"HDMI-A-1"}}
	src := &fakeToplevels{list: []*fakeWindow{a, b, c}}
	tw := newTaskWindows(src)
	b.minimized = true
	src.set(a, b, c)
	a.minimized, c.minimized = true, true
	src.set(a, b, c)
	if got := tw.minimized("DP-1", false); !slices.Equal(got, []taskWindow{b, a}) {
		t.Fatalf("DP-1 = %v, want b then a", got)
	}
	if got := tw.minimized("DP-1", true); !slices.Equal(got, []taskWindow{b, a, c}) {
		t.Fatalf("everywhere = %v, want b, a, c", got)
	}
	b.minimized = false
	src.set(a, b, c)
	b.minimized = true
	src.set(a, b, c)
	if got := tw.minimized("DP-1", false); !slices.Equal(got, []taskWindow{a, b}) {
		t.Fatalf("after b came back and went again = %v, want a then b", got)
	}
	src.set(b)
	if got := tw.minimized("DP-1", true); !slices.Equal(got, []taskWindow{b}) {
		t.Fatalf("after a and c closed = %v", got)
	}
}

// A window takes the dock's rectangle over the module's, whichever came
// first; an unchanged claim sends nothing; the dock letting go hands it
// back to the module's, and nothing left claiming it withdraws it.
func TestTheDockRectangleWinsAndOnlyChangesAreSent(t *testing.T) {
	w := &fakeWindow{title: "w", outputs: []string{"DP-1"}}
	src := &fakeToplevels{list: []*fakeWindow{w}}
	tw := newTaskWindows(src)
	bar, dock := namedHost{name: "bar"}, namedHost{name: "dock"}
	module, docked := tw.owner(), tw.owner()
	inBar := rectClaim{priority: rectFromModule, output: "DP-1", host: bar, rect: render.Rect{X: 10, W: 20, H: 20}}
	inDock := rectClaim{priority: rectFromDock, output: "DP-1", host: dock, rect: render.Rect{X: 300, Y: 700, W: 48, H: 48}}
	module.set(map[taskWindow]rectClaim{w: inBar})
	docked.set(map[taskWindow]rectClaim{w: inDock})
	module.set(map[taskWindow]rectClaim{w: inBar})
	docked.set(map[taskWindow]rectClaim{w: inDock})
	want := []sentRect{{bar, inBar.rect}, {dock, inDock.rect}}
	if !slices.Equal(w.rects, want) {
		t.Fatalf("sent %v, want the module's then the dock's, once each", w.rects)
	}
	docked.release()
	module.release()
	want = append(want, sentRect{bar, inBar.rect}, sentRect{bar, render.Rect{}})
	if !slices.Equal(w.rects, want) {
		t.Fatalf("sent %v, want the module's back, then a withdrawal", w.rects)
	}
}

// Of two bars' modules, the one on the window's output names its
// rectangle.
func TestTheEntryOnTheWindowsOutputWinsATie(t *testing.T) {
	w := &fakeWindow{title: "w", outputs: []string{"HDMI-A-1"}}
	src := &fakeToplevels{list: []*fakeWindow{w}}
	tw := newTaskWindows(src)
	left, right := namedHost{name: "left"}, namedHost{name: "right"}
	first, second := tw.owner(), tw.owner()
	first.set(map[taskWindow]rectClaim{w: {priority: rectFromModule, output: "DP-1", host: left, rect: render.Rect{W: 5, H: 5}}})
	second.set(map[taskWindow]rectClaim{w: {priority: rectFromModule, output: "HDMI-A-1", host: right, rect: render.Rect{W: 6, H: 6}}})
	if last := w.rects[len(w.rects)-1]; last.host != right {
		t.Fatalf("the rectangle is on %v, want the HDMI-A-1 bar's", last.host)
	}
}

func minimizedModule(t *testing.T, cfg *config.Config, src *fakeToplevels) (*minimizedWindowsModule, namedHost) {
	t.Helper()
	ctx := newTestContext(t, cfg)
	ctx.Connector = "DP-1"
	ctx.Tasks = newTaskHub(src, func(fn func()) { fn() })
	ctx.Tasks.apps.goOff = func(func()) {}
	m, err := newMinimizedWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host := namedHost{name: "bar"}
	mod := m.(*minimizedWindowsModule)
	mod.Attach(host)
	return mod, host
}

func taskEntries(m *minimizedWindowsModule) []*taskButton {
	var out []*taskButton
	for _, c := range m.root.Children() {
		out = append(out, c.(*taskButton))
	}
	return out
}

// The module shows an entry per minimized window on its output with the
// title as tooltip, hides while nothing is minimized, and a click
// activates, a middle click closes; each entry is its window's
// rectangle, and a window still open takes the slot after the last.
func TestTheMinimizedWindowsModuleListsActsAndNamesRectangles(t *testing.T) {
	cfg := config.Defaults()
	open := &fakeWindow{title: "open", appID: "foot", outputs: []string{"DP-1"}}
	gone := &fakeWindow{title: "gone", appID: "foot", outputs: []string{"DP-1"}, minimized: true}
	away := &fakeWindow{title: "away", appID: "foot", outputs: []string{"HDMI-A-1"}, minimized: true}
	src := &fakeToplevels{list: []*fakeWindow{open}}
	m, host := minimizedModule(t, cfg, src)
	if m.root.Visible() {
		t.Fatal("the module shows with nothing minimized")
	}
	src.set(open, gone, away)
	entries := taskEntries(m)
	if len(entries) != 1 || entries[0].TooltipText() != "gone" || !m.root.Visible() {
		t.Fatalf("entries %d, visible %v", len(entries), m.root.Visible())
	}
	m.root.Measure(widget.Row, widget.Unknown)
	m.root.Arrange(render.Rect{X: 100, Y: 4, W: 200, H: 32})
	slot := entries[0].Bounds()
	if got := gone.rects; len(got) != 1 || got[0] != (sentRect{host, slot}) {
		t.Fatalf("the minimized window's rectangle = %v, want its entry %v", got, slot)
	}
	if got := open.rects; len(got) != 1 || got[0].rect != nextSlot(slot, widget.Row, m.root.Spacing()) {
		t.Fatalf("the open window's rectangle = %v, want the next slot", got)
	}
	if len(away.rects) != 0 {
		t.Fatalf("a window on another output got %v", away.rects)
	}
	entries[0].ClickAt(widget.Point{})
	if gone.activations != 1 {
		t.Fatal("a click did not activate the window")
	}
	src.set(open, gone, away)
	if len(taskEntries(m)) != 0 || m.root.Visible() {
		t.Fatal("the restored window kept its entry")
	}
}

// show-when-empty keeps the module's own icon on the bar.
func TestShowWhenEmptyKeepsTheModuleIcon(t *testing.T) {
	cfg := config.Defaults()
	cfg.MinimizedWindows.ShowWhenEmpty = true
	m, _ := minimizedModule(t, cfg, &fakeToplevels{})
	entries := taskEntries(m)
	if !m.root.Visible() || len(entries) != 1 || entries[0].icon != cfg.MinimizedWindows.Icon {
		t.Fatalf("visible %v, entries %d", m.root.Visible(), len(entries))
	}
}
