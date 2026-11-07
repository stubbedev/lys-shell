package bar

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
)

// loopQueue is the loop: what is handed to it runs when drained.
type loopQueue struct{ fns []func() }

func (q *loopQueue) add(fn func()) { q.fns = append(q.fns, fn) }

func (q *loopQueue) drain() {
	for len(q.fns) > 0 {
		fn := q.fns[0]
		q.fns = q.fns[1:]
		fn()
	}
}

func prebuildingRegistry(t *testing.T, cfg *config.Config) (*dropdownRegistry, *loopQueue) {
	t.Helper()
	ctx := newTestContext(t, cfg)
	r := newDropdownRegistry(nil, cfg, ctx.Font, ctx.Style, ctx)
	q := &loopQueue{}
	r.idle, r.invoke = q.add, q.add
	return r, q
}

func clockOnly() *config.Config {
	cfg := config.Defaults()
	cfg.Bar.Layout = []config.BarLayout{{Monitor: "*", Show: true, Center: []config.BarItem{{Module: "clock"}}}}
	return cfg
}

func TestTheBarsDropdownsAreTheOnesTheirModulesOpen(t *testing.T) {
	cfg := clockOnly()
	if got := barDropdowns(cfg, dropdownBuilders()); !slices.Equal(got, []string{"calendar", "weather"}) {
		t.Errorf("dropdowns = %q, want the clock's calendar and weather only", got)
	}
	cfg.Bar.Layout[0].Show = false
	if got := barDropdowns(cfg, dropdownBuilders()); len(got) != 0 {
		t.Errorf("a hidden bar's dropdowns = %q, want none", got)
	}
}

func TestADropdownsFirstOpenAfterIdleBuildingBuildsNothing(t *testing.T) {
	r, q := prebuildingRegistry(t, clockOnly())
	r.attachHost("DP-1", fakeHost{}, 1000)
	r.attachHost("HDMI-A-1", fakeHost{}, 1000)
	if len(r.instances) != 0 {
		t.Fatal("dropdowns built before the loop was idle")
	}
	q.drain()
	if len(r.instances) != 2 || r.instances["calendar"] == nil || r.instances["weather"] == nil {
		t.Fatalf("built %d dropdowns, want the calendar and the weather", len(r.instances))
	}
	built := r.instances["calendar"]
	allocs := testing.AllocsPerRun(20, func() {
		if r.instance("calendar", r.builders["calendar"]) != built {
			t.Fatal("the open built the calendar again")
		}
	})
	if allocs > 1 {
		t.Errorf("a first open after the idle build allocates %v", allocs)
	}

	cold, _ := prebuildingRegistry(t, clockOnly())
	if coldAllocs := testing.AllocsPerRun(1, func() {
		cold.release()
		cold.instance("calendar", cold.builders["calendar"])
	}); coldAllocs <= 100 {
		t.Errorf("a cold first open allocates %v: the budget above proves nothing", coldAllocs)
	}
}

func TestAReleaseEndsAPrebuildAndAReloadStartsAnother(t *testing.T) {
	r, q := prebuildingRegistry(t, clockOnly())
	r.attachHost("DP-1", fakeHost{}, 1000)
	r.release()
	q.drain()
	if len(r.instances) != 0 {
		t.Errorf("a released registry built %d dropdowns", len(r.instances))
	}
	moved := clockOnly()
	moved.Bar.Location = config.LocationBottom
	r.setConfig(moved)
	q.drain()
	if len(r.instances) != 2 {
		t.Errorf("after a reload %d dropdowns built, want 2", len(r.instances))
	}
}

// aheadContent counts the hides its registry tells it of.
type aheadContent struct {
	*widget.Box
	hidden int
}

func (h *aheadContent) dropdownHidden() { h.hidden++ }

func TestAPrebuiltDropdownRestsAsAClosedOneDoes(t *testing.T) {
	r, q := prebuildingRegistry(t, clockOnly())
	content := &aheadContent{Box: widget.NewBox(widget.Column, 0, 0)}
	r.builders["calendar"] = func(ModuleContext) widget.Widget { return content }
	r.attachHost("DP-1", fakeHost{}, 1000)
	q.drain()
	if content.hidden != 1 {
		t.Errorf("hidden %d times, want once: what it follows while open must not run while built ahead", content.hidden)
	}
}

func TestWithoutBuildAheadNothingBuildsBeforeAnOpen(t *testing.T) {
	cfg := clockOnly()
	ctx := newTestContext(t, cfg)
	r := newDropdownRegistry(nil, cfg, ctx.Font, ctx.Style, ctx)
	r.buildAhead()
	r.attachHost("DP-1", fakeHost{}, 1000)
	if r.idle != nil || len(r.instances) != 0 {
		t.Error("a registry without an application built ahead")
	}
}

func TestAMountedBarBuildsItsDropdownsAhead(t *testing.T) {
	cfg := clockOnly()
	ctx := styledContext(t, cfg)
	a := apptest.Run(t)
	ctx.App = a
	rt := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	host := &barHost{app: a, rt: rt}
	outputs := a.Session().Outputs()
	if len(outputs) == 0 {
		t.Fatal("the headless sway shows no outputs")
	}
	built := func() int {
		r := rt.ctx.Dropdowns
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.instances)
	}
	if n := apptest.On(a, func() int { rt.mount(a, cfg); return built() }); n != 0 {
		t.Fatalf("%d dropdowns built before a bar attached", n)
	}
	var bar surfaceLayer[config.BarLayout]
	if err := apptest.On(a, func() (err error) { bar, err = host.open(outputs[0], cfg.Bar.Layout[0]); return err }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { apptest.On(a, func() bool { bar.Close(); return true }) })
	apptest.Until(t, a, "the calendar and the weather built ahead", func() bool {
		r := rt.ctx.Dropdowns
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.instances["calendar"] != nil && r.instances["weather"] != nil
	})

	unmounted := &barRuntime{ctx: ctx, font: ctx.Font, style: *ctx.Style, moduleStyle: *ctx.Style}
	unmounted.mount(nil, cfg)
	unmounted.ctx.Dropdowns.attachHost("DP-1", fakeHost{}, 1000)
	if r := unmounted.ctx.Dropdowns; r.idle != nil || len(r.instances) != 0 {
		t.Error("a bar mounted without an application built its dropdowns ahead")
	}
}
