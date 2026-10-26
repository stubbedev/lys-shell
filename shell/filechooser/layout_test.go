package filechooser

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/portalreq"
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

// laidOut lays the overlay out on the test output and returns the
// panel's rect.
func (hs *harness) laidOut(t *testing.T) render.Rect {
	t.Helper()
	lay(hs.cfg.Root, outW, outH)
	return hs.panel(t).PanelRect()
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
				return hs.c.Save(portalreq.Caller{Handle: "/r/footer", Modal: true}, SaveRequest{CurrentFolder: dir, CurrentName: "new.txt"}).URIs
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
				natural := place.Measure(widget.Constraints{Max: widget.Size{W: 1 << 20, H: 1 << 20}}).W
				if cut := b.W < natural; cut != (natural > sidebarW) {
					t.Errorf("%q is %dpx of its natural %d: cut %v", label, b.W, natural, cut)
				}
			})
		})
	}
}

// The replace notice names the folder, not its path, as GTK does; a
// long file name wraps the message inside the panel over Replace, and
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
				line := msg.Measure(widget.Constraints{Max: widget.Size{W: 1 << 20, H: 1 << 20}})
				lines := msg.Measure(widget.Constraints{Max: widget.Size{W: at.W, H: 1 << 20}})
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
