package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// Every picker opens with its search entry, inside the popover, taking
// the keys: typed text lands in the entry, not in the list or grid it
// filters.
func TestPickersFocusTheirSearchEntry(t *testing.T) {
	pickerIcons(t, "ld-battery-symbolic", "tb-cpu-symbolic")
	open := map[string]func(t *testing.T) (*fakePickers, *widget.Entry, widget.Widget){
		"icon": func(t *testing.T) (*fakePickers, *widget.Entry, widget.Widget) {
			_, c, f := openIconPicker(t, "modules.clock.icon-name")
			return f, c.picker.search, c.picker.grid
		},
		"theme preset": func(t *testing.T) (*fakePickers, *widget.Entry, widget.Widget) {
			k := testKit(t, "")
			f := &fakePickers{}
			k.pickers = f
			c := newThemeSelector(k)
			c.btn.ClickAt(widget.Point{})
			return f, c.picker.search, c.picker.list
		},
		"module": func(t *testing.T) (*fakePickers, *widget.Entry, widget.Widget) {
			k, c := newTestLayout(t)
			p := c.pickModule(nil, func(config.BarModule) {})
			return k.pickers.(*fakePickers), p.search, p.list
		},
	}
	for name, opener := range open {
		t.Run(name, func(t *testing.T) {
			f, search, list := opener(t)
			if f.focus == nil || f.focus == list {
				t.Fatalf("the popover focuses %T, want the search entry", f.focus)
			}
			inside := false
			treetest.Walk(f.content, func(w widget.Widget) { inside = inside || w == f.focus })
			if !inside {
				t.Fatalf("the focused %T is not inside the popover", f.focus)
			}
			r := &widget.Router{Root: f.content}
			r.SetFocus(f.focus)
			for _, ch := range "cpu" {
				r.Type(ch)
			}
			if search.Text() != "cpu" {
				t.Errorf("typing into the open picker left its search %q", search.Text())
			}
		})
	}
}

// pickerIcons stands in for the installed icons for the test.
func pickerIcons(t *testing.T, names ...string) {
	t.Helper()
	prev := iconNames
	iconNames = func() []string { return names }
	t.Cleanup(func() { iconNames = prev })
}

// openIconPicker lays a styled icon editor for path out at its natural
// size, clicks its trigger and lays the picker it opened out at its
// own natural size, as the popover does.
func openIconPicker(t *testing.T, path string) (*kit, *iconEditor, *fakePickers) {
	t.Helper()
	k := testKit(t, "")
	f := &fakePickers{}
	k.pickers = f
	theme := apptheme.New(k.store.svc.Config())
	c := newIconEditor(k, pathSlot(k.store, path))
	theme.Attach(c.Box)
	sz := widget.MeasureAsChild(c.Box, widget.Constraints{Max: widget.Size{W: 1400, H: 200}})
	widget.ArrangeAsChild(c.Box, render.Rect{X: 300, Y: 40, W: sz.W, H: sz.H})
	c.btn.ClickAt(widget.Point{})
	if c.picker == nil || f.content != widget.Widget(c.picker.root) || f.anchor != widget.Widget(c.btn) {
		t.Fatal("the trigger opened no picker beside itself")
	}
	layOutPicker(theme, c.picker)
	return k, c, f
}

// layOutPicker lays the open picker out at its natural size, styled
// as lys-ui styles a popover's content: the popover element with
// GtkPopover's background class, its border included.
func layOutPicker(theme *apptheme.Theme, p *iconPicker) {
	p.root.SetElement("popover")
	p.root.AddClass("background")
	theme.Attach(p.root)
	sz := widget.MeasureAsChild(p.root, widget.Constraints{Max: widget.Size{W: 1920, H: 1080}})
	widget.ArrangeAsChild(p.root, render.Rect{X: 300, Y: 100, W: sz.W, H: sz.H})
}

// cellRects are the grid's cells as laid out.
func cellRects(p *iconPicker) []render.Rect {
	out := make([]render.Rect, p.grid.Len())
	for i := range out {
		out[i] = p.grid.ChildAt(i).Bounds()
	}
	return out
}

// The picker is as wide as its trigger, so it hangs flush beneath it;
// the theme's padding insets its search row and grid on every side,
// the entry and the clear button share one row the theme's spacing
// apart, and the icons lie in lines of the full column count, flush
// with that row, the cell gap apart across and down.
func TestIconPickerLaysOutUnderItsTrigger(t *testing.T) {
	names := []string{"a-symbolic", "b-symbolic", "c-symbolic", "d-symbolic", "e-symbolic", "f-symbolic", "g-symbolic", "h-symbolic", "i-symbolic", "j-symbolic"}
	pickerIcons(t, names...)
	_, c, _ := openIconPicker(t, "modules.clock.icon-name")
	p, th := c.picker, widget.Current()
	box, trigger := p.contents.Bounds(), c.btn.Bounds()
	if trigger.W != iconPickerWidth() {
		t.Errorf("the trigger is %dpx wide, want the picker's %dpx", trigger.W, iconPickerWidth())
	}
	if box.W != trigger.W {
		t.Errorf("the picker's contents are %dpx wide under its %dpx trigger", box.W, trigger.W)
	}
	if root := p.root.Bounds(); box.X-root.X != root.X+root.W-(box.X+box.W) {
		t.Errorf("the popover %v frames its contents %v unevenly", root, box)
	}
	entry, clear, scroll := p.search.Bounds(), p.clear.Bounds(), p.scroll.Bounds()
	insets := map[string]int{
		"left":   entry.X - box.X,
		"top":    min(entry.Y, clear.Y) - box.Y,
		"right":  box.X + box.W - (clear.X + clear.W),
		"bottom": box.Y + box.H - (scroll.Y + scroll.H),
	}
	for side, got := range insets {
		if got != th.Padding {
			t.Errorf("the %s inset is %dpx, want the theme's %dpx padding", side, got, th.Padding)
		}
	}
	if gap := clear.X - (entry.X + entry.W); gap != th.Spacing {
		t.Errorf("the clear button sits %dpx after the entry, want the theme's %dpx", gap, th.Spacing)
	}
	if entry.Y+entry.H/2 != clear.Y+clear.H/2 {
		t.Errorf("the entry's centre y=%d is off the clear button's y=%d", entry.Y+entry.H/2, clear.Y+clear.H/2)
	}
	if gap := scroll.Y - max(entry.Y+entry.H, clear.Y+clear.H); gap != th.Spacing {
		t.Errorf("the grid sits %dpx below the search row, want the theme's %dpx", gap, th.Spacing)
	}
	cells := cellRects(p)
	if len(cells) != len(names) {
		t.Fatalf("%d cells for %d icons", len(cells), len(names))
	}
	gap := iconCellGap()
	for i, r := range cells {
		col, line := i%iconPickerColumns, i/iconPickerColumns
		if r.W != iconCellPx() || r.H != iconCellPx() {
			t.Errorf("cell %d is %dx%d, want %dpx square", i, r.W, r.H, iconCellPx())
		}
		if want := entry.X + col*(iconCellPx()+gap); r.X != want {
			t.Errorf("cell %d starts at x=%d, want %d", i, r.X, want)
		}
		if want := scroll.Y + line*(iconCellPx()+gap); r.Y != want {
			t.Errorf("cell %d starts at y=%d, want %d", i, r.Y, want)
		}
	}
	if last := cells[iconPickerColumns-1]; last.X+last.W != clear.X+clear.W {
		t.Errorf("a full line ends at x=%d, not flush with the clear button's %d", last.X+last.W, clear.X+clear.W)
	}
	if scroll.H != 2*iconCellPx()+gap {
		t.Errorf("two lines of icons scroll in %dpx, want them shown whole in %d", scroll.H, 2*iconCellPx()+gap)
	}
}

// More icons than the grid shows at once scroll inside its height: the
// picker stays as wide as its trigger and the grid as tall as its rows.
func TestIconPickerScrollsAFullGrid(t *testing.T) {
	var names []string
	for i := range iconPickerColumns * (iconPickerRows + 3) {
		names = append(names, "icon-"+string(rune('a'+i/26))+string(rune('a'+i%26))+"-symbolic")
	}
	pickerIcons(t, names...)
	_, c, _ := openIconPicker(t, "modules.clock.icon-name")
	p := c.picker
	if got := p.contents.Bounds().W; got != c.btn.Bounds().W {
		t.Errorf("a full picker is %dpx wide under its %dpx trigger", got, c.btn.Bounds().W)
	}
	if got, want := p.scroll.Bounds().H, iconGridExtent(iconPickerRows); got != want {
		t.Errorf("the grid shows %dpx, want %d rows' %dpx", got, iconPickerRows, want)
	}
	if p.grid.Len() != len(names) {
		t.Errorf("%d cells for %d icons", p.grid.Len(), len(names))
	}
	first, next := p.grid.ChildAt(0).Bounds(), p.grid.ChildAt(iconPickerColumns).Bounds()
	if next.X != first.X || next.Y <= first.Y {
		t.Errorf("scrolling, the grid's second line starts at cell %d's %v, want a line of %d", iconPickerColumns, next, iconPickerColumns)
	}
	if last := p.grid.ChildAt(iconPickerColumns - 1).Bounds(); last.X+last.W != p.clear.Bounds().X+p.clear.Bounds().W {
		t.Errorf("scrolling, a full line ends at x=%d, not flush with the clear button", last.X+last.W)
	}
}

// Between the icons shows the popover's own background: the grid
// paints no band of its own.
func TestIconPickerGridPaintsNoBackground(t *testing.T) {
	pickerIcons(t, "a-symbolic", "b-symbolic", "c-symbolic")
	_, c, _ := openIconPicker(t, "modules.clock.icon-name")
	p := c.picker
	root := p.root.Bounds()
	w, h := root.X+root.W+1, root.Y+root.H+1
	data := make([]byte, render.Stride(w)*h)
	cv := render.NewScaled(data, render.Stride(w), w, h, 1, 1)
	p.root.Paint(cv)
	px := func(x, y int) [4]byte {
		i := y*render.Stride(w) + 4*x
		return [4]byte(data[i : i+4])
	}
	a, b := p.grid.ChildAt(0).Bounds(), p.grid.ChildAt(1).Bounds()
	midY := a.Y + a.H/2
	padding := px(p.contents.Bounds().X+widget.Current().Padding/2, midY)
	if gap := px((a.X+a.W+b.X)/2, midY); gap != padding {
		t.Errorf("between the icons is %v, the padding beside them %v", gap, padding)
	}
	if after := px(p.grid.ChildAt(2).Bounds().X+iconCellPx()+iconCellGap()+1, midY); after != padding {
		t.Errorf("past the last icon is %v, the padding beside them %v", after, padding)
	}
}

// Every icon matching the query shows and only those; the current
// one is selected and no other, and none when it does not show.
func TestIconPickerFiltersAndMarksTheCurrent(t *testing.T) {
	pickerIcons(t, "ld-battery-symbolic", "ld-bell-symbolic", "tb-cpu-symbolic")
	k := testKit(t, "")
	path := "modules.clock.icon-name"
	_ = k.store.set(path, "ld-bell-symbolic")
	f := &fakePickers{}
	k.pickers = f
	c := newIconEditor(k, pathSlot(k.store, path))
	c.btn.ClickAt(widget.Point{})
	p := c.picker
	selected := func() []string {
		var out []string
		for i := range p.grid.Len() {
			if p.grid.ChildAt(i).HasState(widget.StateSelected) {
				out = append(out, p.shown[i])
			}
		}
		return out
	}
	if p.grid.Len() != 3 || !slices.Equal(selected(), []string{"ld-bell-symbolic"}) {
		t.Fatalf("%d cells, selected %v; want all three, the current one selected", p.grid.Len(), selected())
	}
	p.search.Insert("LD-B")
	if !slices.Equal(p.shown, []string{"ld-battery-symbolic", "ld-bell-symbolic"}) || p.grid.Len() != 2 {
		t.Errorf("the filter shows %v in %d cells, want the two ld-b icons", p.shown, p.grid.Len())
	}
	if !slices.Equal(selected(), []string{"ld-bell-symbolic"}) {
		t.Errorf("filtered, %v selected, want the current icon", selected())
	}
	p.search.SelectAll()
	p.search.Insert("cpu")
	if !slices.Equal(p.shown, []string{"tb-cpu-symbolic"}) || len(selected()) != 0 {
		t.Errorf("the filter shows %v with %v selected, want the cpu icon alone and unselected", p.shown, selected())
	}
	p.search.SelectAll()
	p.search.Insert("zzz")
	if p.grid.Len() != 0 {
		t.Errorf("a query matching nothing shows %d cells", p.grid.Len())
	}
	if f.closed != 0 || k.store.value(path) != "ld-bell-symbolic" {
		t.Errorf("filtering wrote %v (closed %d)", k.store.value(path), f.closed)
	}
}

// The picker offers every icon the icon theme's search path holds in
// lys-shell's directory, the packaged ones under a data dir's icons
// included; icons elsewhere in a theme are not offered.
func TestIconNamesListTheIconSearchPath(t *testing.T) {
	user, packaged := t.TempDir(), t.TempDir()
	for root, name := range map[string]string{user: "si-gmail-symbolic", packaged: "ld-activity-symbolic"} {
		dir := filepath.Join(root, "hicolor", "scalable", "actions")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".svg"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	apps := filepath.Join(packaged, "hicolor", "scalable", "apps")
	if err := os.MkdirAll(apps, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apps, "firefox.svg"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	widget.SetIconSearchPaths([]string{user, packaged})
	t.Cleanup(func() { widget.SetIconSearchPaths(nil) })
	if got, want := iconNames(), []string{"ld-activity-symbolic", "si-gmail-symbolic"}; !slices.Equal(got, want) {
		t.Errorf("the picker offers %v, want %v", got, want)
	}
}
