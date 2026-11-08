package bar

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
)

func TestDashboardToggleHoldsTheKeyboard(t *testing.T) {
	rig := newDashboardRig(t)
	var window *app.Window
	apptest.On(rig.a, func() error {
		w, err := rig.a.NewWindow(app.WindowConfig{Title: "Focused", AppID: "focused", Root: widget.NewBox(widget.Column, 0, 0)})
		window = w
		return err
	})
	kb := keytest.Dial(t, rig.a)
	onWindow := func() bool { return rig.a.Session().KeyboardFocus() == window.HostSurface() }
	apptest.Until(t, rig.a, "the toplevel holds the keyboard", onWindow)
	apptest.On(rig.a, func() bool {
		rig.reg.setDashboardAnchor(dashboardRigOutput, rig.anchor)
		return true
	})
	toggle := func() {
		t.Helper()
		if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("", dashboardRigOutput) }); err != nil {
			t.Fatalf("toggle: %v", err)
		}
	}
	popover := func() *app.Popover {
		rig.reg.mu.Lock()
		defer rig.reg.mu.Unlock()
		return rig.reg.openPop[dashboardRigOutput]
	}

	toggle()
	apptest.Until(t, rig.a, "the open dashboard holds the keyboard", func() bool {
		return popover() != nil && !onWindow() && rig.a.Session().KeyboardFocus() != nil
	})
	notifications := rig.reg.ctx.Notifications
	if apptest.On(rig.a, notifications.DND) {
		t.Fatal("do not disturb starts on")
	}
	kb.Tap(keytest.Return)
	if !apptest.On(rig.a, notifications.DND) {
		t.Error("Return with no key before it did not press the first enabled tile")
	}
	kb.Tap(keytest.Space)
	if apptest.On(rig.a, notifications.DND) {
		t.Error("Space did not press the same tile")
	}

	pop := popover()
	toggle()
	apptest.Until(t, rig.a, "the second toggle closes it", pop.Closed)
	apptest.Until(t, rig.a, "the keys go back to the toplevel", onWindow)

	toggle()
	apptest.Until(t, rig.a, "it opens again", func() bool { return popover() != pop && popover() != nil && !onWindow() })
	pop = popover()
	kb.Tap(keytest.Escape)
	apptest.Until(t, rig.a, "Escape closes it", pop.Closed)
	apptest.Until(t, rig.a, "the keys go back after Escape", onWindow)
}

func TestDropdownToggleChoosesTheConnector(t *testing.T) {
	rig := newDashboardRig(t)
	if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("", "") }); err == nil || !strings.Contains(err.Error(), "no dashboard module in any bar") {
		t.Errorf("no module anywhere: %v", err)
	}
	if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("DP-9", "") }); err == nil || !strings.Contains(err.Error(), "on DP-9") {
		t.Errorf("no module on the one named: %v", err)
	}
	apptest.On(rig.a, func() bool {
		rig.reg.setDashboardAnchor(dashboardRigOutput, rig.anchor)
		return true
	})
	if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("DP-9", dashboardRigOutput) }); err == nil {
		t.Error("the one named has no module, yet another opened")
	}
	if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("", "ELSEWHERE") }); err != nil {
		t.Errorf("a focused output without the module should fall to the one with it: %v", err)
	}
	rig.close()
	apptest.On(rig.a, func() bool { rig.reg.detachHost(dashboardRigOutput); return true })
	if err := apptest.On(rig.a, func() error { return rig.reg.toggleDashboard("", "") }); err == nil {
		t.Error("an unplugged output's anchor survived")
	}
}

func TestDashboardModuleRegistersItsButton(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	full := computeStyle(cfg, styling.Default())
	ctx.Connector = "DP-1"
	ctx.Dropdowns = newDropdownRegistry(nil, cfg, ctx.Font, &full, ctx)
	row := widget.NewBox(widget.Row, 0, 0)
	if err := appendModule(row, config.BarItem{Module: "clock"}, ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Dropdowns.dashboardAnchors) != 0 {
		t.Error("the clock registered as the dashboard's anchor")
	}
	if err := appendModule(row, config.BarItem{Module: "dashboard"}, ctx); err != nil {
		t.Fatal(err)
	}
	anchor, ok := ctx.Dropdowns.dashboardAnchors["DP-1"].(*barButton)
	if !ok {
		t.Fatalf("the anchor is %T, want the module's button", ctx.Dropdowns.dashboardAnchors["DP-1"])
	}
	if findByClass(row, "dashboard") == nil || anchor.BarButton == nil {
		t.Error("the anchor is not the dashboard's button")
	}
}
