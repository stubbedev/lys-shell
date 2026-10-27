package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/styling"
)

// desktopRem is a "Sans 10" desktop's 1rem at 96 dpi.
const desktopRem = 10.0 * 96 / 72

// Every label of every dropdown, item titles, descriptions, statuses
// and section headers alike, renders at a size of the type scale at
// the styling scale, on a desktop whose rem is not lys-ui's 14px.
func TestDropdownTextIsOnTheTypeScale(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	rem := widget.RootFontSize()
	widget.SetRootFontSize(desktopRem)
	t.Cleanup(func() { widget.SetRootFontSize(rem) })
	for _, p := range dropdownFixtures() {
		t.Run(p.name, func(t *testing.T) {
			ctx := fixtureContext(t)
			scale := float64(ctx.Config.Styling.Scale)
			var sizes []float64
			for _, r := range styling.TextRoles() {
				sizes = append(sizes, r.Px(desktopRem, scale))
			}
			_, _, _, root := paintPreview(ctx, p.build(t, &ctx), p.dropdown)
			for _, off := range treetest.OffScale(root, ctx.Font, sizes) {
				t.Errorf("off the type scale: %s", off)
			}
		})
	}
}

// The network dropdown's item title is body text and its description
// caption text.
func TestNetworkItemTextSizes(t *testing.T) {
	rem := widget.RootFontSize()
	widget.SetRootFontSize(desktopRem)
	t.Cleanup(func() { widget.SetRootFontSize(rem) })
	for _, p := range dropdownFixtures() {
		if p.name != "network" {
			continue
		}
		ctx := fixtureContext(t)
		_, _, _, root := paintPreview(ctx, p.build(t, &ctx), p.dropdown)
		for text, want := range map[string]float64{
			"home": styling.Body.Px(desktopRem, 1),
			"WPA2": styling.Caption.Px(desktopRem, 1),
		} {
			l := treetest.LabelReading(t, root, text)
			if got := treetest.RenderedTextPx(l, ctx.Font); got < want*0.98 || got > want*1.02 {
				t.Errorf("%q renders at %.2fpx, want %.2fpx", text, got, want)
			}
		}
		return
	}
	t.Fatal("no network fixture")
}
