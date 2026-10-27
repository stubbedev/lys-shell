package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
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
			_, c, f := openIconPicker(t, "")
			return f, c.picker.search, c.picker.grid
		},
	}
	for name, opener := range searchPickerOpeners {
		open[name] = func(t *testing.T) (*fakePickers, *widget.Entry, widget.Widget) {
			k, p := opener(t)
			return k.pickers.(*fakePickers), p.search, p.rows
		}
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

// manyIcons are n distinct icon names, in name order.
func manyIcons(n int) []string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, "icon-"+string(rune('a'+i/26/26))+string(rune('a'+i/26%26))+string(rune('a'+i%26))+"-symbolic")
	}
	return names
}

// builtCells records, in order, the icon of every grid cell the picker
// builds while the test runs.
func builtCells(t *testing.T) *[]string {
	t.Helper()
	prev := iconCell
	var built []string
	iconCell = func(name string) widget.Widget {
		built = append(built, name)
		return prev(name)
	}
	t.Cleanup(func() { iconCell = prev })
	return &built
}

// iconPickerPath is the field the icon picker tests edit.
const iconPickerPath = "modules.clock.icon-name"

// openIconPicker lays a styled icon editor holding current out at its
// natural size, clicks its trigger and lays the picker it opened out at
// its own natural size, as the popover does.
func openIconPicker(t *testing.T, current string) (*kit, *iconEditor, *fakePickers) {
	t.Helper()
	k := testKit(t, "")
	if current != "" {
		if err := k.store.set(iconPickerPath, current); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakePickers{}
	k.pickers = f
	theme := apptheme.New(k.store.svc.Config())
	c := newIconEditor(k, pathSlot(k.store, iconPickerPath))
	theme.Attach(c.Box)
	sz := widget.SizeWithinAsChild(c.Box, widget.Size{W: 1400, H: 200})
	widget.ArrangeAsChild(c.Box, render.Rect{X: 300, Y: 40, W: sz.W, H: sz.H})
	c.btn.ClickAt(widget.Point{})
	if c.picker == nil || f.content != widget.Widget(c.picker.root) || f.anchor != widget.Widget(c.btn) {
		t.Fatal("the trigger opened no picker beside itself")
	}
	layOutPicker(theme, c.picker.root)
	paintPicker(c.picker.root)
	return k, c, f
}

// layOutPicker lays the open picker's root out at its natural size,
// styled as lys-ui styles a popover's content: the popover element
// with GtkPopover's background class, its border included.
func layOutPicker(theme *apptheme.Theme, root *widget.Box) {
	root.SetElement("popover")
	root.AddClass("background")
	theme.Attach(root)
	arrangePicker(root)
}

// arrangePicker lays the styled picker's root out at its natural size.
func arrangePicker(root *widget.Box) {
	sz := widget.SizeWithinAsChild(root, widget.Size{W: 1920, H: 1080})
	widget.ArrangeAsChild(root, render.Rect{X: 300, Y: 100, W: sz.W, H: sz.H})
}

// paintPicker paints the laid out picker's root and returns its
// pixels by position.
func paintPicker(root *widget.Box) func(x, y int) [4]byte {
	b := root.Bounds()
	w, h := b.X+b.W+1, b.Y+b.H+1
	data := make([]byte, render.Stride(w)*h)
	cv := render.NewScaled(data, render.Stride(w), w, h, 1, 1)
	root.Paint(cv)
	return func(x, y int) [4]byte {
		i := y*render.Stride(w) + 4*x
		return [4]byte(data[i : i+4])
	}
}

// relayOut lays the open icon picker out at its natural size and
// paints it, as the popover does after every change: the grid places
// the cells in view as it paints.
func relayOut(p *iconPicker) func(x, y int) [4]byte {
	arrangePicker(p.root)
	return paintPicker(p.root)
}

// scrollToEnd scrolls the grid as far down as it goes.
func scrollToEnd(p *iconPicker) { p.grid.ScrollBy(0, iconGridExtent(len(p.shown))) }

// iconCellView is a grid cell as the grid realized it.
type iconCellView struct {
	name     string
	rect     render.Rect
	selected bool
}

// cellsInView are the grid's realized cells that reach into its box,
// in model order.
func cellsInView(p *iconPicker) []iconCellView {
	box := p.grid.Bounds()
	var out []iconCellView
	for _, k := range p.grid.Children() {
		cell := k.(interface {
			TooltipText() string
			Bounds() render.Rect
			HasState(widget.StateFlags) bool
		})
		if r := cell.Bounds(); r.Intersect(box).Empty() {
			continue
		}
		out = append(out, iconCellView{name: cell.TooltipText(), rect: cell.Bounds(), selected: cell.HasState(widget.StateSelected)})
	}
	return out
}

// names are the cells' icons.
func names(cells []iconCellView) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.name
	}
	return out
}

// selectedCells are the icons of the selected cells.
func selectedCells(cells []iconCellView) []string {
	var out []string
	for _, c := range cells {
		if c.selected {
			out = append(out, c.name)
		}
	}
	return out
}

// clickAt clicks the open picker at the centre of r.
func clickAt(p *iconPicker, r render.Rect) {
	router := &widget.Router{Root: p.root}
	pt := widget.Point{X: r.X + r.W/2, Y: r.Y + r.H/2}
	router.Press(widget.BTNLeft, pt, 0)
	router.Release(widget.BTNLeft, pt)
}

// clickIcon lays the open picker out and clicks the cell of the icon
// name, which must be in view.
func clickIcon(t *testing.T, p *iconPicker, name string) {
	t.Helper()
	relayOut(p)
	cells := cellsInView(p)
	i := slices.IndexFunc(cells, func(v iconCellView) bool { return v.name == name })
	if i < 0 {
		t.Fatalf("the icon %s is not in view: %v", name, names(cells))
	}
	clickAt(p, cells[i].rect)
}

// The picker is as wide as its trigger, so it hangs flush beneath it;
// the theme's padding insets its search row and grid on every side,
// the entry and the clear button share one row the theme's spacing
// apart, and the icons lie in lines of the full column count, flush
// with that row, the cell gap apart across and down.
func TestIconPickerLaysOutUnderItsTrigger(t *testing.T) {
	icons := []string{"a-symbolic", "b-symbolic", "c-symbolic", "d-symbolic", "e-symbolic", "f-symbolic", "g-symbolic", "h-symbolic", "i-symbolic", "j-symbolic"}
	pickerIcons(t, icons...)
	_, c, _ := openIconPicker(t, "")
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
	entry, clear, scroll := p.search.Bounds(), p.clear.Bounds(), p.grid.Bounds()
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
	cells := cellsInView(p)
	if !slices.Equal(names(cells), icons) {
		t.Fatalf("the grid shows %v, want %v", names(cells), icons)
	}
	gap := iconCellGap()
	for i, cell := range cells {
		r := cell.rect
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
	if last := cells[iconPickerColumns-1].rect; last.X+last.W != clear.X+clear.W {
		t.Errorf("a full line ends at x=%d, not flush with the clear button's %d", last.X+last.W, clear.X+clear.W)
	}
	if scroll.H != 2*iconCellPx()+gap {
		t.Errorf("two lines of icons scroll in %dpx, want them shown whole in %d", scroll.H, 2*iconCellPx()+gap)
	}
}

// More icons than the grid shows at once scroll inside its height: the
// picker stays as wide as its trigger and the grid as tall as its rows,
// showing exactly its columns by rows of icons, the next ones a scroll
// away.
func TestIconPickerScrollsAFullGrid(t *testing.T) {
	icons := manyIcons(iconPickerColumns * (iconPickerRows + 3))
	pickerIcons(t, icons...)
	_, c, _ := openIconPicker(t, "")
	p := c.picker
	if got := p.contents.Bounds().W; got != c.btn.Bounds().W {
		t.Errorf("a full picker is %dpx wide under its %dpx trigger", got, c.btn.Bounds().W)
	}
	if got, want := p.grid.Bounds().H, iconGridExtent(iconPickerRows); got != want {
		t.Errorf("the grid shows %dpx, want %d rows' %dpx", got, iconPickerRows, want)
	}
	cells := cellsInView(p)
	if want := icons[:iconPickerColumns*iconPickerRows]; !slices.Equal(names(cells), want) {
		t.Fatalf("the grid shows %v, want the first %d icons", names(cells), len(want))
	}
	first, next := cells[0].rect, cells[iconPickerColumns].rect
	if next.X != first.X || next.Y <= first.Y {
		t.Errorf("scrolling, the grid's second line starts at cell %d's %v, want a line of %d", iconPickerColumns, next, iconPickerColumns)
	}
	if last := cells[iconPickerColumns-1].rect; last.X+last.W != p.clear.Bounds().X+p.clear.Bounds().W {
		t.Errorf("scrolling, a full line ends at x=%d, not flush with the clear button", last.X+last.W)
	}
	if bottom := cells[len(cells)-1].rect; bottom.Y+bottom.H != p.grid.Bounds().Y+p.grid.Bounds().H {
		t.Errorf("the last line in view ends at y=%d, not at the grid's bottom %d", bottom.Y+bottom.H, p.grid.Bounds().Y+p.grid.Bounds().H)
	}
	scrollToEnd(p)
	relayOut(p)
	cells = cellsInView(p)
	if got := names(cells); got[len(got)-1] != icons[len(icons)-1] || slices.Contains(got, icons[0]) {
		t.Errorf("scrolled to its end, the grid shows %v, want the last icons and not the first", got)
	}
	if bottom := cells[len(cells)-1].rect; bottom.Y+bottom.H != p.grid.Bounds().Y+p.grid.Bounds().H {
		t.Errorf("scrolled to its end, the last line ends at y=%d, not at the grid's bottom %d", bottom.Y+bottom.H, p.grid.Bounds().Y+p.grid.Bounds().H)
	}
}

// Only the cells in view exist: opening a picker over many icons, or
// filtering it, builds the cells of the lines it shows and none of the
// lines further down; scrolling builds the lines it brings into view.
func TestIconPickerBuildsOnlyTheCellsInView(t *testing.T) {
	icons := manyIcons(iconPickerColumns * 40)
	pickerIcons(t, icons...)
	built := builtCells(t)
	_, c, _ := openIconPicker(t, "")
	p := c.picker
	most := iconPickerColumns * (iconPickerRows + 1)
	if len(*built) > most {
		t.Errorf("opening built %d cells for %d icons, want at most the %d of the lines in view", len(*built), len(icons), most)
	}
	if slices.Contains(*built, icons[most]) || slices.Contains(*built, icons[len(icons)-1]) {
		t.Errorf("opening built cells below the view")
	}
	inView := names(cellsInView(p))
	if len(inView) != iconPickerColumns*iconPickerRows {
		t.Fatalf("the grid shows %d icons, want %d", len(inView), iconPickerColumns*iconPickerRows)
	}
	for _, name := range inView {
		if !slices.Contains(*built, name) {
			t.Errorf("the icon %s in view was never built", name)
		}
	}

	*built = nil
	p.search.Insert("-a")
	relayOut(p)
	matches := matching(icons, "-a")
	if got := names(cellsInView(p)); !slices.Equal(got, matches[:iconPickerColumns*iconPickerRows]) {
		t.Errorf("filtered, the grid shows %v, want the first matches", got)
	}
	if len(*built) > most || slices.Contains(*built, matches[len(matches)-1]) {
		t.Errorf("filtering built %d cells (%v), want only the lines in view", len(*built), *built)
	}

	*built = nil
	scrollToEnd(p)
	relayOut(p)
	if !slices.Contains(*built, matches[len(matches)-1]) {
		t.Errorf("scrolling to the end did not build the last match %s", matches[len(matches)-1])
	}
	if slices.Contains(*built, matches[0]) {
		t.Errorf("scrolling to the end rebuilt the first match %s, out of view", matches[0])
	}
}

// Between the icons shows the popover's own background: the grid
// paints no band of its own.
func TestIconPickerGridPaintsNoBackground(t *testing.T) {
	pickerIcons(t, "a-symbolic", "b-symbolic", "c-symbolic")
	_, c, _ := openIconPicker(t, "")
	p := c.picker
	px := relayOut(p)
	cells := cellsInView(p)
	if len(cells) != 3 {
		t.Fatalf("the grid shows %v, want the three icons", names(cells))
	}
	a, b := cells[0].rect, cells[1].rect
	midY := a.Y + a.H/2
	padding := px(p.contents.Bounds().X+widget.Current().Padding/2, midY)
	if gap := px((a.X+a.W+b.X)/2, midY); gap != padding {
		t.Errorf("between the icons is %v, the padding beside them %v", gap, padding)
	}
	if after := px(cells[2].rect.X+iconCellPx()+iconCellGap()+1, midY); after != padding {
		t.Errorf("past the last icon is %v, the padding beside them %v", after, padding)
	}
}

// Every icon matching the query shows and only those; the current
// one is selected and no other, and none when it does not show.
// Filtering writes nothing.
func TestIconPickerFiltersAndMarksTheCurrent(t *testing.T) {
	pickerIcons(t, "ld-battery-symbolic", "ld-bell-symbolic", "tb-cpu-symbolic")
	k, c, f := openIconPicker(t, "ld-bell-symbolic")
	p := c.picker
	cells := cellsInView(p)
	if len(cells) != 3 || !slices.Equal(selectedCells(cells), []string{"ld-bell-symbolic"}) {
		t.Fatalf("the grid shows %v, selected %v; want all three, the current one selected", names(cells), selectedCells(cells))
	}
	p.search.Insert("LD-B")
	relayOut(p)
	cells = cellsInView(p)
	if want := []string{"ld-battery-symbolic", "ld-bell-symbolic"}; !slices.Equal(names(cells), want) {
		t.Errorf("the filter shows %v, want the two ld-b icons", names(cells))
	}
	if !slices.Equal(selectedCells(cells), []string{"ld-bell-symbolic"}) {
		t.Errorf("filtered, %v selected, want the current icon", selectedCells(cells))
	}
	p.search.SelectAll()
	p.search.Insert("cpu")
	relayOut(p)
	cells = cellsInView(p)
	if !slices.Equal(names(cells), []string{"tb-cpu-symbolic"}) || len(selectedCells(cells)) != 0 {
		t.Errorf("the filter shows %v with %v selected, want the cpu icon alone and unselected", names(cells), selectedCells(cells))
	}
	p.search.SelectAll()
	p.search.Insert("zzz")
	relayOut(p)
	if cells := cellsInView(p); len(cells) != 0 || len(p.shown) != 0 {
		t.Errorf("a query matching nothing shows %v", names(cells))
	}
	if f.closed != 0 || k.store.value(iconPickerPath) != "ld-bell-symbolic" {
		t.Errorf("filtering wrote %v (closed %d)", k.store.value(iconPickerPath), f.closed)
	}
}

// A click on an icon picks that one, filtered or not: it is written,
// the trigger shows it and the popover closes. A click on the grid
// between the icons or past the last one picks nothing.
func TestIconPickerClickPicksTheIcon(t *testing.T) {
	icons := manyIcons(iconPickerColumns*2 + 3)
	pickerIcons(t, icons...)

	k, c, f := openIconPicker(t, icons[0])
	cells := cellsInView(c.picker)
	a := cells[1].rect
	past := cells[len(cells)-1].rect
	past.X += iconCellPx() + iconCellGap()
	for what, r := range map[string]render.Rect{
		"past the last icon": past,
		"between two icons":  {X: a.X + a.W, Y: a.Y, W: iconCellGap(), H: a.H},
		"between two lines":  {X: a.X, Y: a.Y + a.H, W: a.W, H: iconCellGap()},
	} {
		clickAt(c.picker, r)
		if f.closed != 0 || k.store.value(iconPickerPath) != icons[0] {
			t.Errorf("a click %s wrote %v (closed %d)", what, k.store.value(iconPickerPath), f.closed)
		}
	}
	clickIcon(t, c.picker, icons[iconPickerColumns+1])
	if want := icons[iconPickerColumns+1]; k.store.value(iconPickerPath) != want || f.closed != 1 || c.label.Text() != want {
		t.Errorf("the click wrote %v, shows %q (closed %d), want %s", k.store.value(iconPickerPath), c.label.Text(), f.closed, want)
	}

	k, c, f = openIconPicker(t, icons[0])
	c.picker.search.Insert(icons[len(icons)-1])
	clickIcon(t, c.picker, icons[len(icons)-1])
	if want := icons[len(icons)-1]; k.store.value(iconPickerPath) != want || f.closed != 1 {
		t.Errorf("a click on the filtered icon wrote %v (closed %d), want %s", k.store.value(iconPickerPath), f.closed, want)
	}
}

// The pointer over an icon hovers that cell; over the gap between two
// icons it hovers neither.
func TestIconPickerHoversOnlyTheCellUnderThePointer(t *testing.T) {
	icons := manyIcons(iconPickerColumns * 2)
	pickerIcons(t, icons...)
	_, c, _ := openIconPicker(t, "")
	p := c.picker
	cells := cellsInView(p)
	a := cells[1].rect
	r := &widget.Router{Root: p.root}
	hovered := func() string {
		if tip, ok := r.Hovered().(interface{ TooltipText() string }); ok {
			return tip.TooltipText()
		}
		return ""
	}
	r.Move(widget.Point{X: a.X + a.W/2, Y: a.Y + a.H/2})
	if got := hovered(); got != icons[1] {
		t.Errorf("the pointer over %s hovers %q", icons[1], got)
	}
	r.Move(widget.Point{X: a.X + a.W + iconCellGap()/2, Y: a.Y + a.H/2})
	if got := hovered(); got != "" {
		t.Errorf("the pointer between two icons hovers %q", got)
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

// The grid opens scrolled so the current icon shows whole; one in the
// first lines, or none, leaves it at its top.
func TestIconPickerOpensAtTheCurrentIcon(t *testing.T) {
	icons := manyIcons(iconPickerColumns * (iconPickerRows + 4))
	pickerIcons(t, icons...)
	for _, tc := range []struct {
		current string
		atTop   bool
	}{
		{icons[len(icons)-1], false},
		{icons[(iconPickerRows+1)*iconPickerColumns+3], false},
		{icons[(iconPickerRows-1)*iconPickerColumns+3], true},
		{icons[1], true},
		{"not-offered-symbolic", true},
	} {
		t.Run(tc.current, func(t *testing.T) {
			_, c, _ := openIconPicker(t, tc.current)
			p := c.picker
			relayOut(p)
			view := p.grid.Bounds()
			cells := cellsInView(p)
			top := cells[0].name == icons[0] && cells[0].rect.Y == view.Y
			if tc.atTop != top {
				t.Errorf("the grid opens showing %s at y=%d first, want at its top %v", cells[0].name, cells[0].rect.Y, tc.atTop)
			}
			if !slices.Contains(icons, tc.current) {
				return
			}
			i := slices.IndexFunc(cells, func(v iconCellView) bool { return v.name == tc.current })
			if i < 0 {
				t.Fatalf("the current icon is not in view: %v", names(cells))
			}
			if r := cells[i].rect; r.Y < view.Y || r.Y+r.H > view.Y+view.H {
				t.Errorf("the current icon at %v opens outside the grid's view %v", r, view)
			}
			if !slices.Equal(selectedCells(cells), []string{tc.current}) {
				t.Errorf("the grid opens with %v selected, want the current icon", selectedCells(cells))
			}
		})
	}
}

// searchPickerOpeners open each list picker on fake pickers.
var searchPickerOpeners = map[string]func(t *testing.T) (*kit, *searchPicker){
	"theme preset": func(t *testing.T) (*kit, *searchPicker) {
		k := testKit(t, "")
		k.pickers = &fakePickers{}
		c := newThemeSelector(k)
		c.btn.ClickAt(widget.Point{})
		return k, c.picker
	},
	"module": func(t *testing.T) (*kit, *searchPicker) {
		k, c := newTestLayout(t)
		return k, c.pickModule(nil, func(config.BarModule) {})
	},
}

// styledSearchPicker opens a list picker, styled and laid out as its
// popover shows it.
func styledSearchPicker(t *testing.T, open func(t *testing.T) (*kit, *searchPicker)) *searchPicker {
	t.Helper()
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	k, p := open(t)
	layOutPicker(apptheme.New(k.store.svc.Config()), p.root)
	return p
}

// pickerContentsBox is the laid out picker's contents: the popover
// inside its border, the search field and the list inside its padding.
func pickerContentsBox(p *searchPicker) render.Rect {
	return p.root.Children()[0].(widget.Boundser).Bounds()
}

// pickerRowBand is a laid out row's content and the band the list
// draws the row in.
type pickerRowBand struct{ content, band render.Rect }

// rowBands are the bands of the rows shown whole in the list's view,
// top to bottom.
func rowBands(t *testing.T, p *searchPicker) []pickerRowBand {
	t.Helper()
	view := p.scroll.Bounds()
	var out []pickerRowBand
	for i := range p.rows.Len() {
		child := p.rows.ChildAt(i)
		band := child.Bounds()
		if !widget.IsVisible(child) || band.Y+band.H > view.Y+view.H {
			continue
		}
		content := child.Child().(*widget.Box).Children()[0].(widget.Boundser).Bounds()
		out = append(out, pickerRowBand{content: content, band: band})
	}
	if len(out) < 3 {
		t.Fatalf("the picker shows %d whole rows, want at least 3", len(out))
	}
	return out
}

// A row sets its content in from the band it highlights by the theme's
// padding across and half its spacing above and below, as a dropdown's
// rows; the band, not the content, spans the list.
func TestSearchPickerRowsInsetTheirContent(t *testing.T) {
	for name, open := range searchPickerOpeners {
		t.Run(name, func(t *testing.T) {
			p := styledSearchPicker(t, open)
			paintPicker(p.root)
			th, list := widget.Current(), p.scroll.Bounds()
			want := map[string]int{"left": th.Padding, "right": th.Padding, "top": th.Spacing / 2, "bottom": th.Spacing / 2}
			for i, r := range rowBands(t, p) {
				c, b := r.content, r.band
				got := map[string]int{
					"left":   c.X - b.X,
					"right":  b.X + b.W - (c.X + c.W),
					"top":    c.Y - b.Y,
					"bottom": b.Y + b.H - (c.Y + c.H),
				}
				for side, w := range want {
					if got[side] != w {
						t.Errorf("row %d's content sits %dpx in from its band's %s, want %dpx", i, got[side], side, w)
					}
				}
				if b.X != list.X || b.W != list.W {
					t.Errorf("row %d's band %v does not span the list %v", i, b, list)
				}
			}
		})
	}
}

// The list lies on the popover's own background, flush with the search
// field and the popover's padding around it on every side: no inset
// block of another color behind the rows, which show no band of their
// own away from the pointer.
func TestSearchPickerListShowsThePopoverBackground(t *testing.T) {
	for name, open := range searchPickerOpeners {
		t.Run(name, func(t *testing.T) {
			p := styledSearchPicker(t, open)
			px := paintPicker(p.root)
			th := widget.Current()
			list, box := p.scroll.Bounds(), pickerContentsBox(p)
			field := p.field.(widget.Boundser).Bounds()
			if list.X != field.X || list.X+list.W != field.X+field.W {
				t.Errorf("the list %v is not flush with the search field %v", list, field)
			}
			insets := map[string]int{
				"left":   list.X - box.X,
				"right":  box.X + box.W - (list.X + list.W),
				"top":    field.Y - box.Y,
				"bottom": box.Y + box.H - (list.Y + list.H),
			}
			for side, got := range insets {
				if got != th.Padding {
					t.Errorf("the %s inset is %dpx, want the theme's %dpx padding", side, got, th.Padding)
				}
			}
			popover := px(box.X+th.Padding/2, list.Y+list.H/2)
			for i, r := range rowBands(t, p) {
				b, mid := r.band, r.band.Y+r.band.H/2
				for side, x := range map[string]int{"left": b.X + 1, "right": b.X + b.W - 2} {
					if got := px(x, mid); got != popover {
						t.Errorf("row %d's %s edge is %v, the popover around the list %v", i, side, got, popover)
					}
				}
			}
			if got := px(list.X+1, list.Y+list.H-1); got != popover {
				t.Errorf("the list's bottom edge is %v, the popover around it %v", got, popover)
			}
		})
	}
}

// The row under the pointer tints a band rounded at its corners, as a
// dropdown's row does; the rows around it stay untinted.
func TestSearchPickerHighlightsTheHoveredRow(t *testing.T) {
	for name, open := range searchPickerOpeners {
		t.Run(name, func(t *testing.T) {
			p := styledSearchPicker(t, open)
			paintPicker(p.root)
			rows := rowBands(t, p)
			hovered := rows[1].band
			r := &widget.Router{Root: p.root}
			r.Move(widget.Point{X: hovered.X + hovered.W/2, Y: hovered.Y + hovered.H/2})
			widget.ArrangeAsChild(p.root, p.root.Bounds())
			px := paintPicker(p.root)
			box := pickerContentsBox(p)
			popover := px(box.X+widget.Current().Padding/2, hovered.Y+hovered.H/2)
			inside := func(b render.Rect) [4]byte { return px(b.X+b.W/2, b.Y+1) }
			if got := inside(hovered); got == popover {
				t.Errorf("the hovered row %v shows the popover's %v, want a band", hovered, got)
			}
			for corner, at := range map[string][2]int{
				"top-left":     {hovered.X, hovered.Y},
				"top-right":    {hovered.X + hovered.W - 1, hovered.Y},
				"bottom-left":  {hovered.X, hovered.Y + hovered.H - 1},
				"bottom-right": {hovered.X + hovered.W - 1, hovered.Y + hovered.H - 1},
			} {
				if got := px(at[0], at[1]); got != popover {
					t.Errorf("the hovered band's %s corner is %v, want it rounded off to the popover's %v", corner, got, popover)
				}
			}
			for _, i := range []int{0, 2} {
				if got := inside(rows[i].band); got != popover {
					t.Errorf("row %d away from the pointer shows %v, want the popover's %v", i, got, popover)
				}
			}
		})
	}
}

// The popover grows to its widest row, so every row's content shows
// at its natural width; held narrower, a theme's name ellipsizes
// inside its band instead of running over its edge.
func TestSearchPickerFitsItsWidestRow(t *testing.T) {
	for name, open := range searchPickerOpeners {
		t.Run(name, func(t *testing.T) {
			p := styledSearchPicker(t, open)
			paintPicker(p.root)
			for i := range p.rows.Len() {
				content := p.rows.ChildAt(i).Child().(*widget.Box).Children()[0]
				nat := widget.SizeWithinAsChild(content, widget.Size{W: 1920, H: 1080})
				if got := content.(widget.Boundser).Bounds().W; got < nat.W {
					t.Errorf("row %d's content gets %dpx of its natural %dpx", i, got, nat.W)
				}
			}
		})
	}
	p := styledSearchPicker(t, searchPickerOpeners["theme preset"])
	natural := p.root.Bounds()
	widget.ArrangeAsChild(p.root, render.Rect{X: natural.X, Y: natural.Y, W: natural.W / 2, H: natural.H})
	px := paintPicker(p.root)
	popover := px(pickerContentsBox(p).X+widget.Current().Padding/2, p.scroll.Bounds().Y+p.scroll.Bounds().H/2)
	for i, r := range rowBands(t, p) {
		b := r.band
		for x := r.content.X + r.content.W; x < b.X+b.W; x++ {
			if got := px(x, b.Y+b.H/2); got != popover {
				t.Errorf("held to half its width, row %d inks %v at x=%d, past its content's right edge %d", i, got, x, r.content.X+r.content.W)
				break
			}
		}
	}
}

// Typing narrows the rows to the names containing the query, ignoring
// case, in their order, and a click on one picks that name; a query
// matching nothing shows no row, a blank one every row.
func TestSearchPickerFiltersAndPicks(t *testing.T) {
	k, c := newTestLayout(t)
	var picked []config.BarModule
	p := c.pickModule(nil, func(m config.BarModule) { picked = append(picked, m) })
	shown := func() []string {
		var out []string
		for i, name := range p.all {
			if widget.IsVisible(p.rows.ChildAt(i)) {
				out = append(out, name)
			}
		}
		return out
	}
	p.filter("  WORKspaces ")
	want := []string{"hyprland-workspaces", "lys-wm-workspaces", "mango-workspaces", "niri-workspaces", "sway-workspaces"}
	if got := shown(); !slices.Equal(got, want) {
		t.Fatalf("the filter shows %v, want %v", got, want)
	}
	p.rows.ChildAt(slices.Index(p.all, "niri-workspaces")).ClickAt(widget.Point{})
	if !slices.Equal(picked, []config.BarModule{config.ModuleNiriWorkspaces}) || k.pickers.(*fakePickers).closed != 1 {
		t.Errorf("a click picked %v (closed %d), want niri-workspaces once", picked, k.pickers.(*fakePickers).closed)
	}
	p.filter("zzz")
	if got := shown(); len(got) != 0 {
		t.Errorf("a query matching nothing shows %v", got)
	}
	p.filter("")
	if got := shown(); !slices.Equal(got, p.all) {
		t.Errorf("a blank query shows %v, want every module %v", got, p.all)
	}
}

// paintedEntry paints e alone and returns its pixels.
func paintedEntry(e *widget.Entry) []byte {
	b := e.Bounds()
	w, h := b.X+b.W, b.Y+b.H
	data := make([]byte, render.Stride(w)*h)
	e.Paint(render.NewScaled(data, render.Stride(w), w, h, 1, 1))
	return data
}

// restingLine is the middle-ellipsized line of path that e paints, found
// by painting each candidate the path's middle ellipsis yields in twin, a
// field laid out like e that ellipsizes nothing; false when e paints
// none of them.
func restingLine(t *testing.T, e, twin *widget.Entry, path string) (string, bool) {
	t.Helper()
	want := paintedEntry(e)
	twin.SetEllipsize(widget.EllipsizeNone)
	font, px := treetest.Font(t), widget.Current().TextSize
	tried := map[string]bool{}
	for avail := 1; avail <= e.Bounds().W; avail++ {
		line := render.EllipsizeText(font, path, widget.EllipsizeMiddle, float64(avail), px)
		if tried[line] || !strings.Contains(line, render.Ellipsis) {
			continue
		}
		tried[line] = true
		twin.SetText(line)
		twin.MoveHome()
		if bytes.Equal(paintedEntry(twin), want) {
			return line, true
		}
	}
	return "", false
}

// readsHeadAndFile reports whether line is path around one ellipsis:
// its start from longPathHead on, and the end of its file name with the
// extension.
func readsHeadAndFile(line, path string) bool {
	head, tail, ok := strings.Cut(line, render.Ellipsis)
	return ok && strings.HasPrefix(head, longPathHead) && strings.HasPrefix(path, head) &&
		strings.HasSuffix(path, tail) && strings.HasSuffix(tail, filepath.Ext(path)) && !strings.Contains(tail, "/")
}

// lockPathField is the lock page's path field in a window w pixels
// wide, every path set to a long one, with its router.
func lockPathField(t *testing.T, w int) (*fileEditor, *widget.Router) {
	t.Helper()
	win := laidOutPage(t, pathPagesYAML, "lock", w)
	return treetest.All[*fileEditor](win.page)[0], &widget.Router{Root: win.root}
}

// A long path at rest shows ellipsized in the middle, its start and its
// file name, the whole path in the tooltip; focused, the field edits and
// shows the whole path as a field that ellipsizes nothing does, and an
// unsaved edit is what shows once focus leaves.
func TestPathFieldRestsOnItsFileName(t *testing.T) {
	c, r := lockPathField(t, windowWidth)
	twin, _ := lockPathField(t, windowWidth)
	line, ok := restingLine(t, c.entry.Entry, twin.entry.Entry, longPath)
	if !ok {
		t.Fatalf("at rest the field paints no middle ellipsis of %q", longPath)
	}
	if !readsHeadAndFile(line, longPath) {
		t.Errorf("at rest the field reads %q, want the start and the file name of %q", line, longPath)
	}
	if tip := c.entry.TooltipText(); tip != longPath {
		t.Errorf("the tooltip reads %q, want the whole path", tip)
	}

	if err := r.SetFocus(c.entry); err != nil {
		t.Fatal(err)
	}
	plain, plainRouter := lockPathField(t, windowWidth)
	plain.entry.SetEllipsize(widget.EllipsizeNone)
	if err := plainRouter.SetFocus(plain.entry); err != nil {
		t.Fatal(err)
	}
	if c.entry.Text() != longPath || !bytes.Equal(paintedEntry(c.entry.Entry), paintedEntry(plain.entry.Entry)) {
		t.Errorf("focused, the field holds %q and paints other than a field showing the whole path", c.entry.Text())
	}

	c.entry.Insert("x")
	if err := r.SetFocus(nil); err != nil {
		t.Fatal(err)
	}
	line, ok = restingLine(t, c.entry.Entry, twin.entry.Entry, longPath+"x")
	if !ok || !readsHeadAndFile(line, longPath+"x") {
		t.Errorf("after an unsaved edit the field reads %q (%v), want the edited path ellipsized", line, ok)
	}
	if tip := c.entry.TooltipText(); tip != longPath+"x" {
		t.Errorf("after an unsaved edit the tooltip reads %q, want the edited path", tip)
	}
}

// A path that fits its field shows whole at rest, with no ellipsis.
func TestShortPathFieldShowsWhole(t *testing.T) {
	c, _ := lockPathField(t, windowWidth)
	twin, _ := lockPathField(t, windowWidth)
	c.entry.SetText("/a.png")
	c.entry.MoveHome()
	if line, ok := restingLine(t, c.entry.Entry, twin.entry.Entry, "/a.png"); ok {
		t.Errorf("a short path reads %q at rest, want it whole", line)
	}
	twin.entry.SetText("/a.png")
	twin.entry.MoveHome()
	if !bytes.Equal(paintedEntry(c.entry.Entry), paintedEntry(twin.entry.Entry)) {
		t.Error("a short path paints other than a field that ellipsizes nothing")
	}
}

// An empty path field shows its placeholder and has no tooltip.
func TestEmptyPathFieldShowsItsPlaceholder(t *testing.T) {
	k := testKit(t, "")
	c := newFileEditor(k, pathSlot(k.store, "login.background-image"))
	if want := i18n.Settings().Get("settings-file-path-placeholder"); c.entry.Placeholder() != want || c.entry.TooltipText() != "" {
		t.Errorf("an empty field shows %q with tooltip %q, want %q and none", c.entry.Placeholder(), c.entry.TooltipText(), want)
	}
}
