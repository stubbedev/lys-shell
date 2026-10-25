package bar

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

func testStyle(t *testing.T, mutate func(b *config.BarConfig)) barStyle {
	t.Helper()
	cfg := config.Defaults()
	if mutate != nil {
		mutate(&cfg.Bar)
	}
	return computeStyle(cfg, styling.Default())
}

func TestComputeStyleDefaults(t *testing.T) {
	s := testStyle(t, nil)
	palette := styling.Default()
	if s.fg != palette.Fg {
		t.Errorf("fg = %#08x, want fg-default", s.fg)
	}
	if s.moduleGap != 8 { // 0.5 * 16 * 1.0
		t.Errorf("module-gap = %d, want 8", s.moduleGap)
	}
	if s.labelPx != 1.04*styling.RemBase {
		t.Errorf("label px = %v, want the 1.04rem base at multiplier 1.0", s.labelPx)
	}
	if px := testStyle(t, func(b *config.BarConfig) { b.ButtonLabelSize = config.Size{Value: 20, Unit: config.SizePixels} }).labelPx; px != 20 {
		t.Errorf("pixel label size = %v, want literal 20", px)
	}
}

func widgetConstraintsMax(w, h int) widget.Constraints {
	return widget.Constraints{Max: widget.Size{W: w, H: h}}
}

// isolateConfigDir points the config dir at a temp dir, so the theme
// never reads the developer's own styles.
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "lys-shell")
}

// newTestContext is a headless module context whose generation
// retires when the test ends, under headlessLoop: a module's
// goroutines never touch widgets once the next test runs.
func newTestContext(t *testing.T, cfg *config.Config) ModuleContext {
	t.Helper()
	style := computeStyle(cfg, styling.Default()).forModules()
	face := treetest.Font(t)
	gen := newMountGen()
	t.Cleanup(func() {
		headlessLoop.Lock()
		defer headlessLoop.Unlock()
		gen.retire()
	})
	return ModuleContext{Config: cfg, Font: face, Style: &style, gen: gen}
}

// styledContext is newTestContext with the bar stylesheet attached, as
// RunWith wires it.
func styledContext(t *testing.T, cfg *config.Config) ModuleContext {
	t.Helper()
	isolateConfigDir(t)
	ctx := newTestContext(t, cfg)
	ctx.Theme = apptheme.New(cfg)
	return ctx
}

// paintBar builds, lays out, and paints one bar root at width w,
// returning the pixels and the measured height.
func paintBar(t *testing.T, ctx ModuleContext, layout config.BarLayout, w int) ([]byte, int, widget.Widget) {
	t.Helper()
	root := buildRoot(ctx, layout, "DP-1")
	h := barThickness(root, widget.Size{W: w, H: 1 << 16})
	root.Measure(widgetConstraintsMax(w, h))
	root.Arrange(render.Rect{W: w, H: h})
	widget.CollectDamage(root)
	root.Measure(widgetConstraintsMax(w, h))
	root.Arrange(render.Rect{W: w, H: h})
	data := make([]byte, render.Stride(w)*max(h, 1))
	root.Paint(render.New(data, render.Stride(w), w, max(h, 1)))
	return data, h, root
}

func pixelAt(data []byte, w, x, y int) render.Color {
	o := y*render.Stride(w) + x*4
	return render.ColorFromBytes(data[o : o+4])
}

// The center section sits at the bar's middle however unequal the
// sides, as GtkCenterBox keeps it.
func TestBarCenterSectionIsCentered(t *testing.T) {
	cfg := config.Defaults()
	layout := config.BarLayout{
		Monitor: "*",
		Left:    []config.BarItem{{Module: "clock"}, {Module: "clock"}, {Module: "clock"}},
		Center:  []config.BarItem{{Module: "clock"}},
	}
	root := buildRoot(newTestContext(t, cfg), layout, "DP-1")
	root.Measure(widget.Constraints{Max: widget.Size{W: 1600, H: 60}})
	root.Arrange(render.Rect{W: 1600, H: 60})
	b := root.Center().Bounds()
	if mid := b.X + b.W/2; mid < 795 || mid > 805 {
		t.Errorf("center section %+v, midpoint %d, want the bar's 800", b, mid)
	}
}

func TestBarRootCarriesTheRustTreeAndClasses(t *testing.T) {
	cfg := config.Defaults()
	layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock", Class: "mine"}}}
	ctx := newTestContext(t, cfg)
	root := buildRoot(ctx, layout, "DP-1")
	for _, c := range []string{"top", "DP-1"} {
		if !widget.HasClass(root, c) {
			t.Errorf("root misses %q", c)
		}
	}
	if widget.HasClass(root, "floating") {
		t.Error("an inset-free bar is floating")
	}
	if root.Element() != "bar" {
		t.Errorf("root element = %q, want bar", root.Element())
	}
	if !strings.Contains(root.InlineStyle(), "--bar-padding-px: 6") {
		t.Errorf("root inline vars = %q, want the build_css block", root.InlineStyle())
	}
	if n := len(root.Start().Children()) + len(root.End().Children()); n != 0 {
		t.Errorf("the empty start and end zones hold %d items", n)
	}
	items := root.Center().Children()
	if len(items) != 1 || !widget.HasClass(items[0], "bar-item") {
		t.Fatalf("center items = %v, want one bar-item", items)
	}
	// The module wrapper carries module + the layout instance class;
	// the bar button sits inside it.
	wrap, ok := items[0].(*widget.Box).Children()[0].(*widget.Box)
	if !ok {
		t.Fatalf("bar-item child = %T, want the module wrapper", items[0].(*widget.Box).Children()[0])
	}
	btn, ok := wrap.Children()[0].(*barButton)
	if !ok {
		t.Fatalf("wrapper child = %T, want the bar button", wrap.Children()[0])
	}
	for _, c := range []string{"bar-button", cfg.Bar.ButtonVariant.CSSClass()} {
		if !btn.HasClass(c) {
			t.Errorf("bar button misses %q (classes %v)", c, btn.Classes())
		}
	}
	for _, c := range []string{"module", "mine"} {
		if !wrap.HasClass(c) {
			t.Errorf("module wrapper misses %q (classes %v)", c, wrap.Classes())
		}
	}

	cfg.Bar.InsetEdge = config.Size{Value: 4, Unit: config.SizePixels}
	root = buildRoot(newTestContext(t, cfg), layout, "DP-1")
	if !widget.HasClass(root, "floating") {
		t.Error("an inset bar is not floating")
	}
}

func TestGroupsAreBarItemsWithModules(t *testing.T) {
	cfg := config.Defaults()
	layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Group: &config.BarGroup{
		Name: "g1", Modules: []config.BarItem{{Module: "clock"}, {Module: "clock"}},
	}}}}
	root := buildRoot(newTestContext(t, cfg), layout, "DP-1")
	group := root.Start().Children()[0].(*widget.Box)
	if !group.HasClass("bar-item") || !group.HasClass("bar-group") || group.ID() != "g1" {
		t.Errorf("group box classes %v id %q", group.Classes(), group.ID())
	}
	if n := len(group.Children()); n != 2 {
		t.Fatalf("group children = %d, want 2", n)
	}
	for _, k := range group.Children() {
		if !widget.HasClass(k, "module") {
			t.Error("a grouped module lacks the module class")
		}
	}
}

func TestBarPaintsFromTheRustStylesheet(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.BorderLocation = config.BorderBottom
	cfg.Bar.BorderWidth = 2
	cfg.Bar.BorderColor = mustToken(config.TokenRed)
	ctx := styledContext(t, cfg)
	palette := ctx.Theme.RenderPalette()
	data, h, _ := paintBar(t, ctx, config.BarLayout{Monitor: "*"}, 200)
	if h <= 2 {
		t.Fatalf("bar height = %d", h)
	}
	// .bar: background-color from --bar-bg (bg-surface), the bottom
	// border from --bar-border-bottom and --bar-border-color.
	if got := pixelAt(data, 200, 100, 1); got != palette.Surface {
		t.Errorf("bar background = %#08x, want bg-surface %#08x", uint32(got), uint32(palette.Surface))
	}
	if got := pixelAt(data, 200, 100, h-1); got != palette.Red {
		t.Errorf("bottom border = %#08x, want red %#08x", uint32(got), uint32(palette.Red))
	}
	if got := pixelAt(data, 200, 100, 0); got == palette.Red {
		t.Error("the top edge carries the bottom-only border")
	}

	// background-opacity 0: color-mix to transparent.
	cfg.Bar.BackgroundOpacity = 0
	cfg.Bar.BorderLocation = config.BorderNone
	data, _, _ = paintBar(t, styledContext(t, cfg), config.BarLayout{Monitor: "*"}, 200)
	if got := pixelAt(data, 200, 100, 1); got.A() != 0 {
		t.Errorf("opacity 0 bar = %#08x, want transparent", uint32(got))
	}
}

func TestBarInsetsAreWindowMargins(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.InsetEdge = config.Size{Value: 10, Unit: config.SizePixels}
	ctx := styledContext(t, cfg)
	palette := ctx.Theme.RenderPalette()
	_, flatH, _ := paintBar(t, styledContext(t, config.Defaults()), config.BarLayout{Monitor: "*"}, 200)
	data, h, _ := paintBar(t, ctx, config.BarLayout{Monitor: "*"}, 200)
	if h != flatH+10 {
		t.Errorf("inset bar height = %d, want the flat %d plus the 10px edge margin", h, flatH)
	}
	if got := pixelAt(data, 200, 100, 5); got.A() != 0 {
		t.Errorf("inside the edge margin = %#08x, want transparent", uint32(got))
	}
	if got := pixelAt(data, 200, 100, 11); got != palette.Surface {
		t.Errorf("below the margin = %#08x, want the bar", uint32(got))
	}
	if bc := barConfigFor(ctx, config.BarLayout{Monitor: "*"}, &app.Output{Name: "DP-1"}); bc.Margin != 0 {
		t.Errorf("layer margin %d, want none: the insets are the bar's own", bc.Margin)
	}
}

// A bar is as thick as its content across its edge, a side bar's
// width and a top or bottom bar's height, not the output's extent
// along it; the side bar lays out as a column.
func TestBarIsAsThickAsItsContent(t *testing.T) {
	out := widget.Size{W: 1280, H: 800}
	for _, loc := range []config.Location{config.LocationTop, config.LocationBottom, config.LocationLeft, config.LocationRight} {
		cfg := config.Defaults()
		cfg.Bar.Location = loc
		layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
		bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
		if thick := barThickness(bar, out); thick == 0 || thick >= min(out.W, out.H) {
			t.Errorf("%s: thickness %d, want the content's", loc, thick)
		}
		if widget.HasClass(bar, "vertical") != loc.IsVertical() {
			t.Errorf("%s: vertical class %v", loc, widget.HasClass(bar, "vertical"))
		}
	}
}

// The bar's padding insets its zones across the bar and its
// padding-ends along it, turned with the bar on a side edge; at zero
// the zones run flush with the bar's box.
func TestBarPaddingInsetsTheZones(t *testing.T) {
	for _, tc := range []struct {
		loc           config.Location
		padding, ends int
	}{
		{config.LocationTop, 10, 15},
		{config.LocationLeft, 10, 15},
		{config.LocationTop, 0, 0},
	} {
		cfg := config.Defaults()
		cfg.Bar.Location = tc.loc
		cfg.Bar.Padding = config.Size{Value: float32(tc.padding), Unit: config.SizePixels}
		cfg.Bar.PaddingEnds = config.Size{Value: float32(tc.ends), Unit: config.SizePixels}
		layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}}}
		bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
		settleBar(bar, widget.Size{W: 1000, H: 1000})
		start := bar.Start().Bounds()
		wantX, wantY := tc.ends, tc.padding
		if tc.loc.IsVertical() {
			wantX, wantY = tc.padding, tc.ends
		}
		if start.X != wantX || start.Y != wantY {
			t.Errorf("%s padding %d ends %d: start zone at (%d,%d), want (%d,%d)", tc.loc, tc.padding, tc.ends, start.X, start.Y, wantX, wantY)
		}
	}
}

// button-gap is exactly the gap between the icon and the label, wider
// or narrower than lys-ui's own, on whichever side button-icon-position
// puts the icon; the block-prefix variant pads its label instead and
// keeps no gap.
func TestBarButtonGapSeparatesIconAndLabel(t *testing.T) {
	for _, tc := range []struct {
		variant config.BarButtonVariant
		at      config.IconPosition
		gap     float32
		want    int
	}{
		{config.ButtonBasic, config.IconStart, 20, 20},
		{config.ButtonBasic, config.IconStart, 2, 2},
		{config.ButtonBasic, config.IconEnd, 20, 20},
		{config.ButtonIconSquare, config.IconStart, 20, 20},
		{config.ButtonBlockPrefix, config.IconStart, 20, 0},
		{config.ButtonBlockPrefix, config.IconEnd, 20, 0},
	} {
		cfg := config.Defaults()
		cfg.Bar.ButtonVariant = tc.variant
		cfg.Bar.ButtonIconPosition = tc.at
		cfg.Bar.ButtonGap = config.Size{Value: tc.gap, Unit: config.SizePixels}
		layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
		bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
		settleBar(bar, widget.Size{W: 1000, H: 1000})
		btn := clockButton(bar)
		icon, label := shownIcon(btn).Bounds(), btn.Label().Bounds()
		gap := label.X - (icon.X + icon.W)
		if tc.at == config.IconEnd {
			gap = icon.X - (label.X + label.W)
		}
		if gap != tc.want {
			t.Errorf("%s, icon %s, button-gap %vpx: icon and label %dpx apart, want %d", tc.variant, tc.at, tc.gap, gap, tc.want)
		}
	}
}

// Inside a side bar lys-ui marks the module button vertical; along the
// top it does not.
func TestBarButtonFollowsTheBarsOrientation(t *testing.T) {
	for _, loc := range []config.Location{config.LocationTop, config.LocationLeft} {
		cfg := config.Defaults()
		cfg.Bar.Location = loc
		layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
		bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
		settleBar(bar, widget.Size{W: 1000, H: 1000})
		btn := clockButton(bar)
		if btn.HasClass("vertical") != loc.IsVertical() {
			t.Errorf("%s: the module button's vertical class is %v", loc, btn.HasClass("vertical"))
		}
	}
}

// clockButton is the module button of a bar whose center holds one
// clock.
func clockButton(bar *widget.Bar) *barButton {
	return bar.Center().Children()[0].(*widget.Box).Children()[0].(*widget.Box).Children()[0].(*barButton)
}

// paintedAt lays bar out within w x h, paints it and returns the
// pixel at p.
func paintedAt(bar *widget.Bar, w, h int, p widget.Point) render.Color {
	size := settleBar(bar, widget.Size{W: w, H: h})
	widget.CollectDamage(bar)
	data := make([]byte, render.Stride(size.W)*size.H)
	bar.Paint(render.New(data, render.Stride(size.W), size.W, size.H))
	return pixelAt(data, size.W, p.X, p.Y)
}

// A module button lightens under the pointer and returns to its
// configured background once the pointer leaves.
func TestBarButtonShadesUnderThePointer(t *testing.T) {
	layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
	bar := buildRoot(styledContext(t, config.Defaults()), layout, "DP-1")
	settleBar(bar, widget.Size{W: 1000, H: 100})
	b := clockButton(bar)
	lb := b.Label().Bounds()
	at := widget.Point{X: lb.X + 1, Y: lb.Y + 1}
	rest := paintedAt(bar, 1000, 100, at)
	router, _ := treetest.Pointer(bar, b, 1000, 100)
	if hovered := paintedAt(bar, 1000, 100, at); hovered == rest {
		t.Errorf("the hovered button paints %#x, as at rest", uint32(hovered))
	}
	router.Move(widget.Point{X: 1, Y: 1})
	if left := paintedAt(bar, 1000, 100, at); left != rest {
		t.Errorf("the button paints %#x after the pointer left, want its rest %#x", uint32(left), uint32(rest))
	}
}

// A block-prefix icon block fills the button's height even when the
// label is padded more than the icon, so the colored block reads as
// one band beside the label.
func TestBlockPrefixIconFillsTheButtonHeight(t *testing.T) {
	cfg := config.Defaults()
	cfg.Bar.ButtonVariant = config.ButtonBlockPrefix
	cfg.Bar.ButtonIconPadding = config.Scale(1)
	cfg.Bar.ButtonLabelPadding = config.Scale(3)
	layout := config.BarLayout{Monitor: "*", Center: []config.BarItem{{Module: "clock"}}}
	bar := buildRoot(styledContext(t, cfg), layout, "DP-1")
	settleBar(bar, widget.Size{W: 1000, H: 1000})
	btn := clockButton(bar)
	icon, label := shownIcon(btn).Bounds(), btn.Label().Bounds()
	if icon.Y != label.Y || icon.H != label.H {
		t.Errorf("the icon block spans y=%d..%d beside the label's y=%d..%d, want the same band", icon.Y, icon.Y+icon.H, label.Y, label.Y+label.H)
	}
}
