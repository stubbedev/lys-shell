package filechooser

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// The output the layout tests lay the overlay out on.
const outW, outH = 1280, 720

// popoverHost is the overlay's surface host in a test: it records the
// popovers the chooser's widgets open and closes them on cue.
type popoverHost struct {
	opened []widget.Widget
	closed int
}

func (h *popoverHost) OpenMenu(widget.Boundser, render.Font, float64, []widget.MenuItem, widget.MenuOptions) {
}

func (h *popoverHost) OpenPopover(_ widget.Boundser, content widget.Widget, opts widget.PopoverOptions) func() {
	h.opened = append(h.opened, content)
	return func() {
		h.closed++
		if opts.OnClosed != nil {
			opts.OnClosed()
		}
	}
}

func (h *popoverHost) Focus(widget.Widget) error { return nil }

// panelBorder is the panel's border, lys-ui's card surface's 1px.
const panelBorder = 1

// within is the panel at r inside its border, where its content lies.
func within(r render.Rect) render.Rect {
	return render.Rect{X: r.X + panelBorder, Y: r.Y + panelBorder, W: r.W - 2*panelBorder, H: r.H - 2*panelBorder}
}

// laidOut lays the overlay out on the test output and returns the
// panel's rect inside its border.
func (hs *harness) laidOut(t *testing.T) render.Rect {
	t.Helper()
	lay(hs.cfg.Root, outW, outH)
	return within(hs.panel(t).PanelRect())
}

// classed is the one widget carrying class.
func classed(t *testing.T, root widget.Widget, class string) widget.Boundser {
	t.Helper()
	ws := treetest.WithClass(root, class)
	if len(ws) != 1 {
		t.Fatalf("%d widgets carry %s, want one", len(ws), class)
	}
	return ws[0].(widget.Boundser)
}

// inside reports whether r lies within outer.
func inside(r, outer render.Rect) bool {
	return r.X >= outer.X && r.Y >= outer.Y && r.X+r.W <= outer.X+outer.W && r.Y+r.H <= outer.Y+outer.H
}

// crowded is a folder of n files, more than the panel shows.
func crowded(t *testing.T, root string, n int) string {
	t.Helper()
	dir := filepath.Join(root, "crowded")
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("crowded/file-%02d.txt", i)
	}
	touch(t, root, names...)
	return dir
}

// The footer stays in the panel whatever is above it: a folder of
// more files than fit, the new folder row and the replace notice take
// their room from the file list, which scrolls the rest. A short
// folder leaves the footer at the panel's foot all the same.
func TestFooterStaysInThePanel(t *testing.T) {
	cases := []struct {
		name  string
		files int
		show  func(hs *harness)
	}{
		{name: "few files", files: 0},
		{name: "fifty files", files: 50},
		{name: "new folder row", files: 14, show: func(hs *harness) { hs.do(hs.c.showNewFolder) }},
		{name: "replace notice", files: 14, show: func(hs *harness) {
			hs.do(func() { hs.c.ui.name.SetText("file-00.txt") })
			hs.do(hs.c.confirm)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, "")
			dir := hs.root
			if tc.files > 0 {
				dir = crowded(t, hs.root, tc.files)
			}
			hs.ask(t, func() []string {
				return hs.c.Save(portaldialogs.Caller{Handle: "/r/footer", Modal: true}, SaveRequest{CurrentFolder: dir, CurrentName: "new.txt"}).URIs
			})
			if tc.show != nil {
				tc.show(hs)
			}
			hs.run(func() {
				panel := hs.laidOut(t)
				footer := classed(t, hs.cfg.Root, "file-chooser-footer").Bounds()
				if !inside(footer, panel) || footer.Y+footer.H != panel.Y+panel.H {
					t.Errorf("footer at %v, want it at the foot of the panel %v", footer, panel)
				}
				for _, class := range []string{"file-chooser-confirm", "file-chooser-cancel"} {
					if b := classed(t, hs.cfg.Root, class).Bounds(); !inside(b, footer) {
						t.Errorf("%s at %v, outside the footer %v", class, b, footer)
					}
				}
				list := hs.c.ui.list.Bounds()
				if list.Y+list.H > footer.Y {
					t.Errorf("the file list at %v runs into the footer at %v", list, footer)
				}
				rows := len(hs.c.s.entries)
				if fits := list.H/listRowH >= rows; tc.files >= 50 && fits {
					t.Errorf("the list is %dpx, tall enough for all %d rows: it was never short of room", list.H, rows)
				}
			})
		})
	}
}

// The filter is a dropdown like the dialog's choices: closed, it is a
// face in the footer and nothing pops over the panel; a click opens
// its list in a popover whose every filter is a row with room to
// read, and a pick applies the filter and closes it.
func TestFilterOpensAPopoverOfReadableRows(t *testing.T) {
	hs := newHarness(t, "")
	filters := []Filter{{"Images", []Rule{{RuleGlob, "*.png"}}}, {"Text", []Rule{{RuleGlob, "*.txt"}}}}
	hs.open(t, OpenRequest{Filters: filters})
	host := &popoverHost{}
	hs.run(func() { widget.SetSurfaceHost(hs.cfg.Root, host) })
	if len(host.opened) != 0 {
		t.Fatalf("%d popovers open before any click", len(host.opened))
	}
	hs.do(func() {
		hs.laidOut(t)
		r, p := treetest.Pointer(hs.cfg.Root, hs.c.ui.filter, outW, outH)
		r.Press(widget.BTNLeft, p, 0)
		r.Release(widget.BTNLeft, p)
	})
	if len(host.opened) != 1 {
		t.Fatalf("a click on the filter opened %d popovers, want its list", len(host.opened))
	}
	var rows []string
	var text *widget.Label
	hs.run(func() {
		content := host.opened[0]
		lay(content, 400, 400)
		for _, l := range treetest.All[*widget.Label](content) {
			if !widget.IsVisible(l) || l.Text() == "" {
				continue
			}
			if b := l.Bounds(); b.H < 10 || b.W < 10 {
				t.Errorf("filter row %q is %v, too small to read", l.Text(), b)
			}
			rows = append(rows, l.Text())
			if l.Text() == "Text" {
				text = l
			}
		}
		if b := content.(widget.Boundser).Bounds(); b.H <= 2*listRowH {
			t.Errorf("the open filter list is %v, too short for its %d filters", b, len(filters)+1)
		}
	})
	if want := []string{"Images", "Text", "All Files"}; strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Errorf("the open list reads %q, want %q", rows, want)
	}
	if text == nil {
		t.Fatal("no Text row to pick")
	}
	hs.do(func() {
		r, p := treetest.Pointer(host.opened[0], text, 400, 400)
		r.Press(widget.BTNLeft, p, 0)
		r.Release(widget.BTNLeft, p)
	})
	if got := hs.shown(); strings.Join(got, "|") != "docs|pics|b.txt" || host.closed != 1 {
		t.Errorf("picking Text lists %v and closed %d popovers, want the text files and the list closed", got, host.closed)
	}
}

// A request without filters shows no filter at all.
func TestNoFiltersNoFilterDropdown(t *testing.T) {
	hs := newHarness(t, "")
	hs.open(t, OpenRequest{})
	if widget.IsVisible(hs.c.ui.filter) {
		t.Error("a request without filters shows the filter dropdown")
	}
}

// The sidebar keeps its width: a long place name ellipsizes inside it
// instead of widening it, and a short one is neither cut nor narrows
// it.
func TestSidebarKeepsItsWidth(t *testing.T) {
	for _, tc := range []struct{ name, label string }{
		{"short", "Projects"},
		{"long", strings.Repeat("a long bookmark name ", 10)},
	} {
		label := tc.label
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, "")
			hs.c.d.Places = func() []Place { return []Place{{label, hs.root, "folder-symbolic"}} }
			hs.open(t, OpenRequest{})
			hs.run(func() {
				hs.laidOut(t)
				scroll := classed(t, hs.cfg.Root, "file-chooser-sidebar-scroll").Bounds()
				if scroll.W != sidebarW {
					t.Errorf("sidebar is %dpx wide, want %d", scroll.W, sidebarW)
				}
				place := treetest.Button(t, hs.cfg.Root, label)
				b := place.Bounds()
				if b.X+b.W > scroll.X+scroll.W {
					t.Errorf("place %v runs out of the sidebar %v", b, scroll)
				}
				natural := widget.SizeWithin(place, widget.Size{W: 1 << 20, H: 1 << 20}).W
				if cut := b.W < natural; cut != (natural > sidebarW) {
					t.Errorf("%q is %dpx of its natural %d: cut %v", label, b.W, natural, cut)
				}
			})
		})
	}
}

// The replace notice names the folder, not its path, as GTK does; a
// long file name wraps the message inside the panel beside Replace, and
// a short one keeps it on one line.
func TestReplaceNoticeWrapsInsideThePanel(t *testing.T) {
	long := strings.Repeat("a-long-file-name ", 8) + ".txt"
	for _, tc := range []struct{ case_, name string }{{"short", "b.txt"}, {"long", long}} {
		name := tc.name
		t.Run(tc.case_, func(t *testing.T) {
			hs := newHarness(t, "")
			touch(t, hs.root, name)
			hs.ask(t, func() []string {
				return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root, CurrentName: name}).URIs
			})
			hs.do(hs.c.confirm)
			hs.run(func() {
				want := "“" + name + "” already exists in “" + filepath.Base(hs.root) + "”. Replace it?"
				if got := hs.c.ui.noticeLabel.Text(); got != want {
					t.Errorf("notice %q, want %q", got, want)
				}
				panel := hs.laidOut(t)
				notice := hs.c.ui.notice.Bounds()
				for what, b := range map[string]render.Rect{"notice": notice, "message": hs.c.ui.noticeLabel.Bounds(), "Replace": hs.c.ui.replace.Bounds()} {
					if !inside(b, panel) {
						t.Errorf("%s at %v, outside the panel %v", what, b, panel)
					}
				}
				msg := hs.c.ui.noticeLabel
				at := msg.Bounds()
				if rb := hs.c.ui.replace.Bounds(); rb.X < at.X+at.W || rb.Y >= at.Y+at.H || rb.Y+rb.H <= at.Y {
					t.Errorf("Replace at %v, not beside the message %v", rb, at)
				}
				line := widget.SizeWithin(msg, widget.Size{W: 1 << 20, H: 1 << 20})
				lines := widget.SizeWithin(msg, widget.Size{W: at.W, H: 1 << 20})
				if wrapped := lines.H > line.H; wrapped != (name == long) || at.H < lines.H {
					t.Errorf("the message for %q is %v, %dpx at its width and one line %d: wrapped %v", name, at, lines.H, line.H, wrapped)
				}
			})
		})
	}
}

// The save name sits a padding clear of the separator above it and
// inset like the other bands; an open dialog has no name row at all,
// the footer right under the body's separator. No widget in the panel
// spaces itself with a margin: containers pad and gap their children.
func TestSaveNameIsPaddedFromTheSeparator(t *testing.T) {
	for _, save := range []bool{true, false} {
		t.Run(strconv.FormatBool(save), func(t *testing.T) {
			hs := newHarness(t, "")
			if save {
				hs.ask(t, func() []string {
					return hs.c.Save(testCaller, SaveRequest{CurrentFolder: hs.root, CurrentName: "x.txt"}).URIs
				})
			} else {
				hs.open(t, OpenRequest{})
			}
			hs.run(func() {
				panel := hs.laidOut(t)
				pad := widget.Current().Padding
				body := classed(t, hs.cfg.Root, "file-chooser-body").Bounds()
				footer := classed(t, hs.cfg.Root, "file-chooser-footer").Bounds()
				name := hs.c.ui.name.Bounds()
				if save {
					if gap := name.Y - (body.Y + body.H); gap <= pad {
						t.Errorf("name entry %dpx below the body, want it clear of the separator by more than %d", gap, pad)
					}
					if name.X-panel.X != 2*pad || panel.X+panel.W-(name.X+name.W) != 2*pad || footer.Y-(name.Y+name.H) != pad {
						t.Errorf("name entry %v in the panel %v over the footer %v, want it inset %d aside and %d below", name, panel, footer, 2*pad, pad)
					}
				} else if gap := footer.Y - (body.Y + body.H); gap > 1 {
					t.Errorf("footer %dpx below the body in an open dialog, want only the separator", gap)
				}
				treetest.Walk(hs.cfg.Root, func(w widget.Widget) {
					m, ok := w.(interface{ Margin() render.Insets })
					if ok && m.Margin() != (render.Insets{}) {
						t.Errorf("%T %v spaces itself with margin %v", w, w.(widget.Boundser).Bounds(), m.Margin())
					}
				})
			})
		})
	}
}

// longName is a file name with no break the grid's cell width fits.
const longName = "screenshot-2026-10-26-at-14.32.05-from-the-second-display.png"

// gridded opens a chooser on the tree and a long-named file, switched
// to the grid.
func gridded(t *testing.T) *harness {
	t.Helper()
	hs := newHarness(t, "")
	touch(t, hs.root, longName)
	hs.open(t, OpenRequest{})
	hs.click(t, "Toggle grid view")
	return hs
}

// gridCells paints the surface on the test output and returns the
// grid's realized cells' rects in model order, and the frame's pixels.
func (hs *harness) gridCells(t *testing.T) ([]render.Rect, func(x, y int) render.Color) {
	t.Helper()
	at := hs.frame(outW, outH)
	var cells []render.Rect
	var entries int
	hs.run(func() {
		entries = len(hs.c.s.entries)
		for _, cell := range hs.c.ui.grid.Children() {
			cells = append(cells, cell.(widget.Boundser).Bounds())
		}
	})
	if len(cells) != entries {
		t.Fatalf("%d grid cells in view, want all %d entries", len(cells), entries)
	}
	return cells, at
}

// The grid's cells sit a gap apart in both directions, and a point in
// a gap hits no cell; the grid sits the views' one inset in from the
// sidebar, the separators above and below and the panel's edge.
func TestGridCellsAreGappedAndInset(t *testing.T) {
	hs := gridded(t)
	cells, _ := hs.gridCells(t)
	hs.run(func() {
		gap, pad := widget.Current().Spacing, viewsInset()
		cols := hs.c.ui.grid.Columns()
		if cols < 2 || len(cells) <= cols {
			t.Fatalf("%d columns over %d cells, want at least two of each axis", cols, len(cells))
		}
		first, next, below := cells[0], cells[1], cells[cols]
		if got := next.X - (first.X + first.W); got != gap || next.Y != first.Y {
			t.Errorf("cells %v and %v are %dpx apart in a line, want %d", first, next, got, gap)
		}
		if got := below.Y - (first.Y + first.H); got != gap || below.X != first.X {
			t.Errorf("cell %v and the cell below %v are %dpx apart, want %d", first, below, got, gap)
		}
		grid := &hs.c.ui.grid.List
		for _, gp := range []widget.Point{
			{X: first.X + first.W + gap/2, Y: first.Y + first.H/2},
			{X: first.X + first.W/2, Y: first.Y + first.H + gap/2},
		} {
			if hit := grid.HitTest(gp); hit != widget.Widget(grid) {
				t.Errorf("the gap at %v hits %T, want no cell", gp, hit)
			}
		}
		sidebar := classed(t, hs.cfg.Root, "file-chooser-sidebar-scroll").Bounds()
		body := classed(t, hs.cfg.Root, "file-chooser-body").Bounds()
		g := grid.Bounds()
		if first.X-(sidebar.X+sidebar.W) != pad || first.Y-body.Y != pad {
			t.Errorf("first cell %v beside the sidebar %v under the body's top %d, want it inset %d", first, sidebar, body.Y, pad)
		}
		if g.X+g.W != body.X+body.W-pad || g.Y+g.H != body.Y+body.H-pad {
			t.Errorf("grid %v in the body %v, want it inset %d from its right and bottom", g, body, pad)
		}
	})
}

// The grid's cells paint the list's fills with its rounding: the
// accent behind the selected cell, the hover tint under the pointer,
// neither in a cell's corner nor on the cells between.
func TestGridCellsPaintRoundedFills(t *testing.T) {
	hs := gridded(t)
	hs.pick(t, "pics")
	cells, at := hs.gridCells(t)
	var blank render.Color
	var sel, hov, plain render.Rect
	hs.run(func() {
		g := hs.c.ui.grid.Bounds()
		blank = at(g.X+g.W-2, g.Y+g.H-2)
		ns := names(hs.c.s.entries)
		sel, hov, plain = cells[slices.Index(ns, "pics")], cells[slices.Index(ns, "a.png")], cells[slices.Index(ns, "docs")]
	})
	edge := func(r render.Rect) (int, int) { return r.X + 2, r.Y + r.H/2 }
	accent, hover := hs.token(t, config.TokenAccent), hs.token(t, config.TokenBgHover)
	if got := at(edge(sel)); got != accent {
		t.Errorf("selected cell paints %v, want the accent %v", got, accent)
	}
	if got := at(sel.X, sel.Y); got != blank {
		t.Errorf("selected cell's corner paints %v, want it rounded off to the view's %v", got, blank)
	}
	hs.run(func() {
		r := &widget.Router{Root: hs.cfg.Root}
		r.Move(widget.Point{X: hov.X + hov.W/2, Y: hov.Y + hov.H/2})
	})
	at = hs.frame(outW, outH)
	if got := at(edge(hov)); got != hover {
		t.Errorf("hovered cell paints %v, want the hover tint %v", got, hover)
	}
	if got := at(hov.X, hov.Y+hov.H-1); got != blank {
		t.Errorf("hovered cell's corner paints %v, want it rounded off to the view's %v", got, blank)
	}
	if got := at(edge(sel)); got != accent {
		t.Errorf("selected cell paints %v with the pointer elsewhere, want the accent %v", got, accent)
	}
	if got := at(edge(plain)); got != blank {
		t.Errorf("a cell neither selected nor hovered paints %v, want the view's %v", got, blank)
	}
}

// A name too long for a cell breaks inside its words onto lines inside
// the cell, the gaps beside and below it left blank; a short name sits
// centred under the icon.
func TestGridNamesStayInTheirCells(t *testing.T) {
	hs := gridded(t)
	cells, at := hs.gridCells(t)
	var blank render.Color
	var long, short render.Rect
	var gap, nameTop int
	hs.run(func() {
		g := hs.c.ui.grid.Bounds()
		blank = at(g.X+g.W-2, g.Y+g.H-2)
		ns := names(hs.c.s.entries)
		long, short = cells[slices.Index(ns, longName)], cells[slices.Index(ns, "b.txt")]
		th := widget.Current()
		gap, nameTop = th.Spacing, th.Padding/2+gridIconPx
	})
	spill := 0
	for y := long.Y; y < long.Y+long.H+gap; y++ {
		for x := long.X; x < long.X+long.W+gap; x++ {
			if (x >= long.X+long.W || y >= long.Y+long.H) && at(x, y) != blank {
				spill++
			}
		}
	}
	if spill > 0 {
		t.Errorf("the long name paints %d pixels in the gaps beside and below its cell %v", spill, long)
	}
	lo, hi := short.X+short.W, short.X
	for y := short.Y + nameTop; y < short.Y+short.H; y++ {
		for x := short.X; x < short.X+short.W; x++ {
			if at(x, y) != blank {
				lo, hi = min(lo, x), max(hi, x)
			}
		}
	}
	if mid, want := (lo+hi)/2, short.X+short.W/2; hi < lo || mid < want-2 || mid > want+2 {
		t.Errorf("the short name spans %d..%d, want it centred on %d", lo, hi, want)
	}
}
