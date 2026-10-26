package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
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
	layOutPicker(theme, c.picker.root)
	return k, c, f
}

// layOutPicker lays the open picker's root out at its natural size,
// styled as lys-ui styles a popover's content: the popover element
// with GtkPopover's background class, its border included.
func layOutPicker(theme *apptheme.Theme, root *widget.Box) {
	root.SetElement("popover")
	root.AddClass("background")
	theme.Attach(root)
	sz := widget.MeasureAsChild(root, widget.Constraints{Max: widget.Size{W: 1920, H: 1080}})
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
	px := paintPicker(p.root)
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

// The grid opens scrolled so the current icon shows whole; one in the
// first lines, or none, leaves it at its top.
func TestIconPickerOpensAtTheCurrentIcon(t *testing.T) {
	var names []string
	for i := range iconPickerColumns * (iconPickerRows + 4) {
		names = append(names, "icon-"+string(rune('a'+i/26))+string(rune('a'+i%26))+"-symbolic")
	}
	pickerIcons(t, names...)
	for _, tc := range []struct {
		current string
		atTop   bool
	}{
		{names[len(names)-1], false},
		{names[(iconPickerRows+1)*iconPickerColumns+3], false},
		{names[(iconPickerRows-1)*iconPickerColumns+3], true},
		{names[1], true},
		{"not-offered-symbolic", true},
	} {
		t.Run(tc.current, func(t *testing.T) {
			k := testKit(t, "")
			path := "modules.clock.icon-name"
			_ = k.store.set(path, tc.current)
			k.pickers = &fakePickers{}
			theme := apptheme.New(k.store.svc.Config())
			c := newIconEditor(k, pathSlot(k.store, path))
			theme.Attach(c.Box)
			sz := widget.MeasureAsChild(c.Box, widget.Constraints{Max: widget.Size{W: 1400, H: 200}})
			widget.ArrangeAsChild(c.Box, render.Rect{X: 300, Y: 40, W: sz.W, H: sz.H})
			c.btn.ClickAt(widget.Point{})
			p := c.picker
			layOutPicker(theme, p.root)
			layOutPicker(theme, p.root)
			view := p.scroll.Bounds()
			if _, y := p.scroll.Offset(); tc.atTop != (y == 0) {
				t.Errorf("the grid opens scrolled %dpx, want at its top %v", y, tc.atTop)
			}
			i := slices.Index(p.shown, tc.current)
			if i < 0 {
				return
			}
			if r := p.grid.ChildAt(i).Bounds(); r.Y < view.Y || r.Y+r.H > view.Y+view.H {
				t.Errorf("the current icon at %v opens outside the grid's view %v", r, view)
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
				nat := widget.MeasureAsChild(content, widget.Constraints{Max: widget.Size{W: 1920, H: 1080}})
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
	want := []string{"hyprland-workspaces", "mango-workspaces", "niri-workspaces", "sway-workspaces"}
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
