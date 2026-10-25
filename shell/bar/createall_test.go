package bar

import (
	"errors"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// stubModule is a module that is nothing but its root.
type stubModule struct{ root *widget.Box }

func (m stubModule) Root() widget.Widget { return m.root }

// An unavailable module is left out of its row and the rest of the
// section still builds: one missing daemon never takes the bar down.
func TestAppendItemsSkipsModuleThatFailsToBuild(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	ok := stubModule{root: widget.NewBox(widget.Row, 0, 0)}
	factories["test-ok"] = func(ModuleContext) (Module, error) { return ok, nil }
	factories["test-missing"] = func(ModuleContext) (Module, error) {
		return nil, errors.New("upower: The name is not activatable")
	}
	defer delete(factories, "test-ok")
	defer delete(factories, "test-missing")

	row := itemsZone([]config.BarItem{{Module: "test-missing"}, {Module: "test-ok"}}, ctx)

	items := row.Children()
	if len(items) != 2 {
		t.Fatalf("section items = %d, want 2 (one bar-item per layout item)", len(items))
	}
	if got := len(items[0].(*widget.Box).Children()); got != 0 {
		t.Fatalf("failed module's bar-item children = %d, want 0 (the module is left out)", got)
	}
	if got := len(items[1].(*widget.Box).Children()); got != 1 {
		t.Fatalf("working module's bar-item children = %d, want 1", got)
	}
}

// An unknown module name is logged and skipped the same way, instead
// of failing the whole bar.
func TestAppendItemsSkipsUnknownModule(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	row := itemsZone([]config.BarItem{{Module: "no-such-module"}}, ctx)
	if got := len(row.Children()[0].(*widget.Box).Children()); got != 0 {
		t.Fatalf("unknown module's bar-item children = %d, want 0", got)
	}
}

// updates is in the schema without a module in either shell: its slot
// is skipped as "not implemented" (the Rust factory's warning) and the
// rest of the section builds.
func TestUnimplementedSchemaModuleIsSkipped(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	if _, err := Create("updates", ctx); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("updates: %v, want a not-implemented error", err)
	}
	items := itemsZone([]config.BarItem{{Module: "updates"}, {Module: "clock"}}, ctx).Children()
	if len(items[0].(*widget.Box).Children()) != 0 || len(items[1].(*widget.Box).Children()) != 1 {
		t.Error("want the updates item empty and the clock built")
	}
}

// Modules that show an icon in Rust carry it: the clock assembles its
// icon-name beside the time, and a bare icon root (power, dashboard)
// becomes an icon-only button with the icon as its .bar-button-icon.
func TestModulesCarryTheirIcons(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	m, err := Create("clock", ctx)
	if err != nil {
		t.Fatal(err)
	}
	btn, ok := m.Root().(*barButton)
	if !ok || shownIcon(btn) == nil || shownIcon(btn).Name() != cfg.Clock.IconName || btn.Label().Text() == "" {
		t.Fatalf("clock root %T, want a bar button with %q and the time", m.Root(), cfg.Clock.IconName)
	}
	cfg.Clock.IconShow = false
	if m, err = Create("clock", ctx); err != nil {
		t.Fatal(err)
	}
	if btn := m.Root().(*barButton); shownIcon(btn) != nil {
		t.Error("icon-show off kept the clock icon")
	}

	icon := widget.NewThemeIcon("ld-power-symbolic", 16)
	b := asBarButton(ctx, icon)
	if shownIcon(b) != icon || !icon.HasClass("bar-button-icon") || b.Label().Visible() {
		t.Errorf("icon root: icon shown %v, label visible %v, want the button's own icon.bar-button-icon and no label", shownIcon(b) == icon, b.Label().Visible())
	}
}

// itemsZone is a bar zone holding items.
func itemsZone(items []config.BarItem, ctx ModuleContext) *widget.Box {
	zone := widget.NewBox(widget.Row, 0, 0)
	appendItems(zone, items, ctx)
	return zone
}
