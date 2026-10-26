package filechooser

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

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
