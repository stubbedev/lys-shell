package bar

import (
	"reflect"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

// widgetPkg is lys-ui's widget package path, whose components lay out
// their own insides.
var widgetPkg = reflect.TypeFor[widget.Box]().PkgPath()

// layoutOnly reports whether w only places its children: anything that
// paints no background of its own and is a box, a stack, a scroll, a
// revealer, a clamp, or one of the shell's own containers around them.
// A spacer is layout too, with nothing to place.
func layoutOnly(w widget.Widget) bool {
	if widget.StylesBackground(w) {
		return false
	}
	switch w.(type) {
	case *widget.Box, *widget.Stack, *widget.Scroll, *widget.Revealer, *widget.Clamp, *widget.Spacer:
		return true
	}
	t := reflect.TypeOf(w)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() != widgetPkg
}

// shownExtent is the union of what shows under root outside skip: the
// bounds of every visible lys-ui component (a card, an item, a label,
// a status page) clipped to the scrolls around it, looking through the
// containers that only place them.
func shownExtent(root, skip widget.Widget, clip render.Rect) render.Rect {
	var ink render.Rect
	var visit func(w widget.Widget, clip render.Rect)
	visit = func(w widget.Widget, clip render.Rect) {
		if w == skip {
			return
		}
		if v, ok := w.(interface{ Visible() bool }); ok && !v.Visible() {
			return
		}
		b := w.(widget.Boundser).Bounds().Intersect(clip)
		if b.Empty() {
			return
		}
		if !layoutOnly(w) {
			if ink.Empty() {
				ink = b
			} else {
				ink = ink.Union(b)
			}
			return
		}
		if _, ok := w.(*widget.Scroll); ok {
			clip = b
		}
		if kids, ok := w.(interface{ Children() []widget.Widget }); ok {
			for _, k := range kids.Children() {
				visit(k, clip)
			}
		}
	}
	visit(root, clip)
	return ink
}

// contentInsets is how far a dropdown's body - everything under its
// header strip, or all of it on a page without one - shows from the
// card's inner edges: from the header's foot on top, from the card's
// border on the other sides.
func contentInsets(t *testing.T, root *widget.Box) render.Insets {
	t.Helper()
	inner := root.Children()[0].(widget.Boundser).Bounds()
	top := inner.Y
	var header widget.Widget
	if hs := treetest.WithClass(root, "dropdown-header"); len(hs) > 0 && hs[0].(interface{ Visible() bool }).Visible() {
		header = hs[0]
		hb := header.(widget.Boundser).Bounds()
		top = hb.Y + hb.H
	}
	ink := shownExtent(root.Children()[0], header, inner)
	if ink.Empty() {
		t.Fatal("the dropdown's body shows nothing")
	}
	return render.Insets{
		Top:    ink.Y - top,
		Left:   ink.X - inner.X,
		Right:  inner.X + inner.W - (ink.X + ink.W),
		Bottom: inner.Y + inner.H - (ink.Y + ink.H),
	}
}

// uniformInset is every side's inset when insets are one value.
func uniformInset(in render.Insets) (int, bool) {
	return in.Top, in.Top == in.Bottom && in.Top == in.Left && in.Top == in.Right
}

// Every dropdown, on every page and prompt, shows its content the same
// distance from all four edges of its card: the content column's
// padding, with the space between sections its gap. Nothing pads the
// card's foot - no configured height left empty below the content, no
// hidden page's height, no trailing margin.
func TestDropdownContentInsetsAreUniform(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	want := 2 * widget.Current().Padding
	for _, p := range dropdownFixtures() {
		t.Run(p.name, func(t *testing.T) {
			ctx := fixtureContext(t)
			_, _, _, root := paintPreview(ctx, p.build(t, &ctx), p.dropdown)
			in := contentInsets(t, root)
			if got, ok := uniformInset(in); !ok || got != want {
				t.Errorf("the %s content sits %+v from the card's edges, want %dpx on every side", p.name, in, want)
			}
		})
	}
}

// The inset check sees a trailing margin: the same dropdown with a
// margin under its last section reports a deeper foot than its top.
func TestDropdownContentInsetsSeeATrailingMargin(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	ctx := fixtureContext(t)
	v := brightnessDropdown(ctx).(*brightnessView)
	content := treetest.WithClass(v, "dropdown-content")[0].(*widget.Box)
	last := content.Children()[len(content.Children())-1]
	last.(interface{ SetMargin(render.Insets) }).SetMargin(render.Insets{Bottom: 12})
	_, _, _, root := paintPreview(ctx, v, "brightness")
	in := contentInsets(t, root)
	if _, ok := uniformInset(in); ok || in.Bottom != in.Top+12 {
		t.Errorf("a 12px trailing margin left the insets %+v, want the foot 12px deeper than the top", in)
	}
}
