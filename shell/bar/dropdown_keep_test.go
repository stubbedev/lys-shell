package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

func TestAReloadOfTheRecorderCaptureSettingsKeepsTheOpenDropdowns(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	r := newDropdownRegistry(nil, cfg, ctx.Font, ctx.Style, ctx)
	content := &countingContent{Box: widget.NewBox(widget.Column, 0, 0)}
	builds := 0
	build := func(ModuleContext) widget.Widget { builds++; return content }
	first := r.instance("recorder", build)
	generation := r.ctx.gen

	capture := config.Defaults()
	capture.Recorder.Microphone = !cfg.Recorder.Microphone
	capture.Recorder.WebcamX = 40
	r.setConfig(capture)
	if r.instance("recorder", build) != first || builds != 1 || content.closed != 0 {
		t.Fatalf("a capture-only reload rebuilt the dropdown: %d builds, %d releases", builds, content.closed)
	}
	if generation.retiredNow() {
		t.Error("a capture-only reload ended what the dropdown follows")
	}
	if r.cfg != capture || r.ctx.Config != capture {
		t.Error("the next open does not read the new snapshot")
	}

	moved := config.Defaults()
	moved.Bar.Location = config.LocationBottom
	r.setConfig(moved)
	if content.closed != 1 || !generation.retiredNow() {
		t.Fatalf("a bar change released %d times, generation retired %v; want once, retired", content.closed, generation.retiredNow())
	}
	r.instance("recorder", build)
	if builds != 2 {
		t.Errorf("after a bar change %d builds, want the dropdown built again", builds)
	}
}

func TestARemountKeepsTheDropdownsWhoseInputsStayed(t *testing.T) {
	r, q := prebuildingRegistry(t, clockOnly())
	r.attachHost("DP-1", fakeHost{}, 1000)
	r.setDashboardAnchor("DP-1", widget.NewBox(widget.Column, 0, 0))
	q.drain()
	calendar := r.instances["calendar"]
	if calendar == nil {
		t.Fatal("the calendar was not built ahead")
	}
	instances := r.ctx.gen

	bars := r.ctx
	bars.gen = newMountGen()
	r.remount(bars)
	if len(r.hosts) != 0 || len(r.dashboardAnchors) != 0 {
		t.Errorf("the old bars' %d hosts and %d dashboard anchors stayed", len(r.hosts), len(r.dashboardAnchors))
	}
	if r.ctx.gen != instances {
		t.Error("the dropdowns took the bars' generation")
	}
	bars.gen.retire()
	if instances.retiredNow() {
		t.Error("retiring the bars ended the dropdowns")
	}
	r.attachHost("DP-1", fakeHost{}, 1000)
	q.drain()
	if r.instances["calendar"] != calendar {
		t.Fatal("a remount with the same config and style built the calendar again")
	}
	allocs := testing.AllocsPerRun(20, func() { r.instance("calendar", r.builders["calendar"]) })
	if allocs > 1 {
		t.Errorf("a first open after a remount allocates %v", allocs)
	}
}

func TestARemountWithANewStyleBuildsTheDropdownsAgainAhead(t *testing.T) {
	r, q := prebuildingRegistry(t, clockOnly())
	r.attachHost("DP-1", fakeHost{}, 1000)
	q.drain()
	calendar := r.instances["calendar"]
	instances := r.ctx.gen

	r.style.labelPx++
	r.remount(r.ctx)
	if !instances.retiredNow() || len(r.instances) != 0 {
		t.Fatalf("a new style kept %d instances, generation retired %v", len(r.instances), instances.retiredNow())
	}
	r.attachHost("DP-1", fakeHost{}, 1000)
	q.drain()
	rebuilt := r.instances["calendar"]
	if rebuilt == nil || rebuilt == calendar {
		t.Fatal("the calendar was not built again ahead in the new style")
	}
	allocs := testing.AllocsPerRun(20, func() {
		if r.instance("calendar", r.builders["calendar"]) != rebuilt {
			t.Fatal("the open built the calendar again")
		}
	})
	if allocs > 1 {
		t.Errorf("a first open after a restyle allocates %v", allocs)
	}
}
