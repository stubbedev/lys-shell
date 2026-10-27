package settings

import (
	"cmp"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// desktopRem is a "Sans 10" desktop's 1rem at 96 dpi, the size the
// sidebar read tiny at.
const desktopRem = 10.0 * 96 / 72

// styledWindow is the settings window under the shell's stylesheet on
// a desktopRem desktop, laid out at its default size.
func styledWindow(t *testing.T) (*kit, *window) {
	t.Helper()
	rem := widget.RootFontSize()
	widget.SetRootFontSize(desktopRem)
	t.Cleanup(func() { widget.SetRootFontSize(rem) })
	k := testKit(t, "")
	w := newWindow(k, k.store.svc.Config(), (&fakeAfter{}).after)
	apptheme.New(k.store.svc.Config()).Attach(w.root)
	widget.SizeWithin(w.root, widget.Size{W: windowWidth, H: windowHeight})
	w.root.Arrange(render.Rect{W: windowWidth, H: windowHeight})
	return k, w
}

// scaleSizes is every type-scale size at scale over desktopRem.
func scaleSizes(scale float64) []float64 {
	var out []float64
	for _, r := range styling.TextRoles() {
		out = append(out, r.Px(desktopRem, scale))
	}
	return out
}

// widestApart is the two of names whose text in face differs most in
// width, the wider first.
func widestApart(face render.Font, names []string) (string, string) {
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Compare(face.Shape(b, 1).Advance(), face.Shape(a, 1).Advance())
	})
	return names[0], names[len(names)-1]
}

// The sidebar's page links are body text and its section headers
// caption text, the desktop's sizes rather than lys-ui's fractions of
// them. Both are read off pairs of their buttons, whose content lys-ui
// keeps out of reach.
func TestSidebarTextIsOnTheTypeScale(t *testing.T) {
	k, w := styledWindow(t)
	tr := i18n.Settings()
	var navs, heads []string
	for _, sec := range w.sections {
		heads = append(heads, tr.Get(sec.key))
		for _, p := range sec.pages {
			navs = append(navs, tr.Get(p.navKey))
		}
	}
	px := func(names []string) float64 {
		long, short := widestApart(k.face, names)
		return treetest.ButtonTextPx(treetest.Button(t, w.sidebar, long), treetest.Button(t, w.sidebar, short), k.face)
	}
	nav, head := px(navs), px(heads)
	if want := styling.Body.Px(desktopRem, 1); !near(nav, want) {
		t.Errorf("nav links render at %.2fpx, want the body's %.2fpx", nav, want)
	}
	if want := styling.Caption.Px(desktopRem, 1); !near(head, want) {
		t.Errorf("section headers render at %.2fpx, want the caption's %.2fpx", head, want)
	}
}

// Every label of the window Walk reaches, sidebar and the open page,
// renders at a size of the type scale.
func TestSettingsTextIsOnTheTypeScale(t *testing.T) {
	k, w := styledWindow(t)
	for _, off := range treetest.OffScale(w.root, k.face, scaleSizes(1)) {
		t.Errorf("off the type scale: %s", off)
	}
}

// A label sized in Go past the scale is caught, styled or not.
func TestOffScaleCatchesAHardCodedSize(t *testing.T) {
	k, w := styledWindow(t)
	stray := widget.NewLabel(k.face, 13, "stray", 0)
	w.root.Append(stray, false)
	widget.SizeWithin(w.root, widget.Size{W: windowWidth, H: windowHeight})
	w.root.Arrange(render.Rect{W: windowWidth, H: windowHeight})
	off := treetest.OffScale(w.root, k.face, scaleSizes(1))
	if len(off) == 0 {
		t.Fatal("a 13px label passed the type-scale check")
	}
}

// near reports two sizes equal within the measuring's rounding.
func near(a, b float64) bool { return a > b*0.98 && a < b*1.02 }
