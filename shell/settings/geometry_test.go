package settings

import (
	"fmt"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// geometryYAML gives every list and card editor items to lay out.
const geometryYAML = `
bar:
  layout:
    - monitor: "*"
      show: true
      left: [clock, {name: sys, modules: [cpu, ram]}]
      right: [battery]
osd:
  presets:
    - id: saved
      label: Saved
modules:
  battery:
    thresholds:
      - below: 20
        icon-color: red
wallpaper:
  monitors:
    - name: DP-1
      wallpaper: /tmp/a.png
`

// laidOutPages shows every page of a styled window in turn, laid out
// tall enough to hold all of it, and hands each to visit.
func laidOutPages(t *testing.T, visit func(id string, page widget.Widget)) {
	t.Helper()
	k := testKit(t, geometryYAML)
	cfg := k.store.svc.Config()
	w := newWindow(k, cfg, func(_ time.Duration, fn func()) { fn() })
	w.stack.SetTransition(widget.StackNone, 0)
	apptheme.New(cfg).Attach(w.root)
	const W, H = 1400, 6000
	for _, sec := range w.sections {
		for _, p := range sec.pages {
			w.show(p.id)
			w.root.Measure(widget.Constraints{Min: widget.Size{W: W, H: H}, Max: widget.Size{W: W, H: H}})
			w.root.Arrange(render.Rect{W: W, H: H})
			visit(p.id, w.page)
		}
	}
}

// walkOwn visits the window's own widgets under w: a chip's inside is
// lys-ui's.
func walkOwn(w widget.Widget, visit func(widget.Widget)) {
	visit(w)
	if _, chip := w.(*widget.Chip); chip {
		return
	}
	if c, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, k := range c.Children() {
			walkOwn(k, visit)
		}
	}
}

// role is a widget's accessible role, false for one without.
func role(w widget.Widget) (widget.Role, bool) {
	r, ok := w.(interface{ Role() widget.Role })
	if !ok {
		return 0, false
	}
	return r.Role(), true
}

// fieldSized reports a control of the field height: an entry or a
// spin, a dropdown, a button (a color button among them).
func fieldSized(w widget.Widget) bool {
	switch w.(type) {
	case *widget.Button, *widget.ColorButton:
		return true
	}
	r, ok := role(w)
	return ok && (r == widget.RoleEntry || r == widget.RoleComboBox)
}

// shown is a widget laid out and visible.
func shown(w widget.Widget) bool {
	v, ok := w.(interface{ Visible() bool })
	b, hasBounds := w.(widget.Boundser)
	return ok && v.Visible() && hasBounds && b.Bounds().W > 0 && b.Bounds().H > 0
}

// rowBox is a box laying its children out side by side.
type rowBox interface {
	Axis() widget.Axis
	Children() []widget.Widget
}

// Every row of controls on every page lines them up the same way: the
// controls share the row's centre line, the field controls (entries,
// spins, dropdowns, buttons) one height, and neighbouring controls sit
// the theme's spacing apart.
func TestControlRowsShareHeightCentreAndGap(t *testing.T) {
	gap := widget.Current().Spacing
	rows := 0
	laidOutPages(t, func(id string, page widget.Widget) {
		walkOwn(page, func(x widget.Widget) {
			row, ok := x.(rowBox)
			if !ok || row.Axis() != widget.Row || !shown(x) {
				return
			}
			var prev widget.Boundser
			var fieldH, centre, controls int
			for _, kid := range row.Children() {
				if _, ok := role(kid); !ok || !shown(kid) {
					prev = nil
					continue
				}
				controls++
				b := kid.(widget.Boundser).Bounds()
				c := b.Y + b.H/2
				if controls == 1 {
					centre = c
				} else if d := c - centre; d < -1 || d > 1 {
					t.Errorf("%s: %T at %v is off its row's centre line y=%d", id, kid, b, centre)
				}
				if fieldSized(kid) {
					if fieldH == 0 {
						fieldH = b.H
					} else if b.H != fieldH {
						t.Errorf("%s: %T is %dpx tall beside a %dpx control", id, kid, b.H, fieldH)
					}
				}
				if prev != nil {
					if p := prev.Bounds(); b.X-(p.X+p.W) != gap {
						t.Errorf("%s: %T sits %dpx after its neighbour, want the theme's %dpx", id, kid, b.X-(p.X+p.W), gap)
					}
				}
				prev = kid.(widget.Boundser)
			}
			if controls > 1 {
				rows++
			}
		})
	})
	if rows == 0 {
		t.Fatal("no page laid out a row of several controls")
	}
}

// No widget on a page carries a margin of its own: spacing comes from
// the boxes' gaps and lys-ui's own padding.
func TestPagesCarryNoMargins(t *testing.T) {
	laidOutPages(t, func(id string, page widget.Widget) {
		walkOwn(page, func(x widget.Widget) {
			if m, ok := x.(interface{ Margin() render.Insets }); ok && m.Margin() != (render.Insets{}) {
				t.Errorf("%s: %T carries the margin %+v", id, x, m.Margin())
			}
		})
	})
}

// A card's fields line up: the controls that take a field's spare width
// start at one x, its identity's too, and its fields sit the theme's
// spacing apart. The layout card's zones start their chips at one x.
func TestCardFieldsLineUp(t *testing.T) {
	gap := widget.Current().Spacing
	sameX := func(id, what string, ws []widget.Widget) {
		t.Helper()
		if len(ws) < 2 {
			t.Fatalf("%s: %d %s, want several to compare", id, len(ws), what)
		}
		x0 := ws[0].(widget.Boundser).Bounds().X
		for _, w := range ws[1:] {
			if x := w.(widget.Boundser).Bounds().X; x != x0 {
				t.Errorf("%s: one of the %s starts at x=%d, another at %d", id, what, x, x0)
			}
		}
	}
	checked := map[string]bool{}
	laidOutPages(t, func(id string, page widget.Widget) {
		switch id {
		case "battery":
			body := treetest.WithClass(page, "card-form-body")[0].(*widget.Box)
			rows := body.Children()
			for i := 1; i < len(rows); i++ {
				a, b := rows[i-1].(widget.Boundser).Bounds(), rows[i].(widget.Boundser).Bounds()
				if b.Y-(a.Y+a.H) != gap {
					t.Errorf("%s: card fields %d and %d sit %dpx apart, want %d", id, i-1, i, b.Y-(a.Y+a.H), gap)
				}
			}
			checked[id] = true
		case "wallpaper":
			card := treetest.WithClass(page, "monitor-card")[0]
			sameX(id, "monitor card's fields", []widget.Widget{
				treetest.All[*liveText](treetest.WithClass(card, "monitor-card-header")[0])[0],
				treetest.WithClass(card, "monitor-wallpaper-entry")[0],
			})
			checked[id] = true
		case "bar-general":
			sameX(id, "layout zones", treetest.WithClass(page, "layout-zone-items"))
			checked[id] = true
		}
	})
	if len(checked) != 3 {
		t.Errorf("checked %v, want the battery, wallpaper and bar-general pages", checked)
	}
}

// A custom color shows its color button beside the token dropdown at
// the dropdown's height, on the row's centre line, the theme's spacing
// apart: the button is lys-ui's outline icon button, not a bare swatch.
func TestCustomColorButtonMatchesItsDropdown(t *testing.T) {
	k := testKit(t, "modules:\n  clock:\n    icon-color: \"#123456\"\n")
	c := fieldControl(k, field("modules.clock.icon-color")).(*colorValueEditor)
	apptheme.New(k.store.svc.Config()).Attach(c.Box)
	widget.MeasureAsChild(c.Box, widget.Constraints{Max: widget.Size{W: 600, H: 200}})
	widget.ArrangeAsChild(c.Box, render.Rect{W: 600, H: 200})
	if !c.swatch.Visible() {
		t.Fatal("a custom color hides its color button")
	}
	b, d := c.swatch.Bounds(), c.drop.Bounds()
	if b.H != d.H {
		t.Errorf("the color button is %dpx tall beside the %dpx dropdown", b.H, d.H)
	}
	if b.Y+b.H/2 != d.Y+d.H/2 {
		t.Errorf("the color button's centre y=%d is off the dropdown's y=%d", b.Y+b.H/2, d.Y+d.H/2)
	}
	if gap := d.X - (b.X + b.W); gap != widget.Current().Spacing {
		t.Errorf("the dropdown sits %dpx after the color button, want the theme's %dpx", gap, widget.Current().Spacing)
	}
}

// longPath is a wallpaper path far longer than a path field, under
// longPathHead.
const (
	longPathHead = "/home/"
	longPath     = longPathHead + "someone/Pictures/wallpapers/landscapes/mountains-at-dawn-ultrawide.png"
)

// pathPagesYAML sets every path row of the wallpaper and lock pages to
// a long path.
const pathPagesYAML = `
wallpaper:
  wallpaper: ` + longPath + `
  cycling-directory: /home/someone/Pictures/wallpapers/a-very-long-directory-name
lock:
  background-image: ` + longPath + `
greeter:
  background-image: ` + longPath + `
`

// narrowWindowWidth is the narrowest window every settings page fits.
const narrowWindowWidth = 760

// pathPages are the pages holding a path row.
var pathPages = []string{"wallpaper", "lock", "greeter"}

// laidOutPage shows the page id of a styled window w pixels wide, laid
// out the way the window lays it out, and returns the window.
func laidOutPage(t *testing.T, configYAML, id string, w int) *window {
	t.Helper()
	k := testKit(t, configYAML)
	cfg := k.store.svc.Config()
	win := newWindow(k, cfg, func(_ time.Duration, fn func()) { fn() })
	win.stack.SetTransition(widget.StackNone, 0)
	apptheme.New(cfg).Attach(win.root)
	win.show(id)
	layOut(win, w)
	return win
}

// layOut lays win out w pixels wide the way the window lays it out.
func layOut(win *window, w int) {
	con := widget.Constraints{Min: widget.Size{W: w, H: windowHeight}, Max: widget.Size{W: w, H: windowHeight}}
	for range 2 {
		win.root.Measure(con)
		win.root.Arrange(render.Rect{W: w, H: windowHeight})
	}
}

// pathFieldWidths are the widths of the path entries on page id of a
// window w pixels wide.
func pathFieldWidths(t *testing.T, id string, w int) []int {
	t.Helper()
	var widths []int
	for _, c := range treetest.All[*fileEditor](laidOutPage(t, pathPagesYAML, id, w).page) {
		widths = append(widths, c.entry.Bounds().W)
	}
	if len(widths) == 0 {
		t.Fatalf("%s holds no path row", id)
	}
	return widths
}

// Every path field is one width, whatever its row's description and
// however narrow the window: the description gives way, not the field.
func TestPathFieldsShareOneWidth(t *testing.T) {
	want := pathFieldWidths(t, "wallpaper", windowWidth)[0]
	if want <= 0 {
		t.Fatalf("the path field is %dpx wide", want)
	}
	for _, id := range pathPages {
		for _, w := range []int{windowWidth, narrowWindowWidth} {
			for i, got := range pathFieldWidths(t, id, w) {
				if got != want {
					t.Errorf("%s at %dpx: path field %d is %dpx wide, want the %dpx every path field takes", id, w, i, got, want)
				}
			}
		}
	}
}

// overflow lays page id out in a narrow window, its path fields chars
// wide, and returns the controls reaching past the window's right edge.
func overflow(t *testing.T, id string, chars int) []string {
	t.Helper()
	win := laidOutPage(t, pathPagesYAML, id, narrowWindowWidth)
	for _, c := range treetest.All[*fileEditor](win.page) {
		c.entry.SetWidthChars(chars, chars)
	}
	layOut(win, narrowWindowWidth)
	var past []string
	controls := 0
	walkOwn(win.page, func(x widget.Widget) {
		if _, ok := role(x); !ok || !shown(x) {
			return
		}
		controls++
		if b := x.(widget.Boundser).Bounds(); b.X+b.W > narrowWindowWidth {
			past = append(past, fmt.Sprintf("%T at %v", x, b))
		}
	})
	if controls == 0 {
		t.Fatalf("the %s page laid out no control", id)
	}
	return past
}

// Every page with a path row, the row and all, fits a narrow window: no
// control reaches past its right edge.
func TestPathPagesFitANarrowWindow(t *testing.T) {
	for _, id := range pathPages {
		t.Run(id, func(t *testing.T) {
			if past := overflow(t, id, pathEntryChars); len(past) > 0 {
				t.Errorf("at %dpx these reach past the window: %v", narrowWindowWidth, past)
			}
		})
	}
}

// The path field is as wide as a narrow window allows: a character more
// pushes some path page past the window's edge.
func TestPathFieldIsTheWidestThatFits(t *testing.T) {
	for _, id := range pathPages {
		if len(overflow(t, id, pathEntryChars+1)) > 0 {
			return
		}
	}
	t.Errorf("path fields %d characters wide still fit every path page at %dpx; widen them", pathEntryChars+1, narrowWindowWidth)
}
