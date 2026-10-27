package bar

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/nightlight"
)

// fakeSunset is a scripted sunsetEnv: a fake filter, a settable clock,
// and a temp state dir.
type fakeSunset struct {
	running bool
	applied nightlight.Setting
	starts  int
	stops   int
	refused error
	closed  bool
	changed func()
	clock   time.Time
}

func (f *fakeSunset) Apply(s nightlight.Setting) error {
	f.running, f.applied = true, s
	f.starts++
	return nil
}

func (f *fakeSunset) Clear() error {
	f.running, f.refused = false, nil
	f.stops++
	return nil
}

func (f *fakeSunset) Refused() error { return f.refused }

func (f *fakeSunset) Protocol() string { return "fake_colour_v1" }

func (f *fakeSunset) Close() { f.closed, f.running = true, false }

func (f *fakeSunset) env(t *testing.T) sunsetEnv {
	return sunsetEnv{
		open: func(changed func()) (colourFilter, error) {
			f.changed = changed
			return f, nil
		},
		now:      func() time.Time { return f.clock },
		stateDir: t.TempDir(),
	}
}

func newTestNightlight(t *testing.T, cfg *config.Config, env sunsetEnv) *nightlightModule {
	t.Helper()
	m, err := newNightlightWith(newTestContext(t, cfg), env)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Copenhagen, where 2024-06-21 01:00 UTC is night and 12:00 day.
func sunsetConfig(auto bool) *config.Config {
	cfg := config.Defaults()
	cfg.Nightlight.AutoSchedule = auto
	cfg.Nightlight.Latitude, cfg.Nightlight.Longitude = 55.6, 12.5
	return cfg
}

var (
	sunsetNight = time.Date(2024, 6, 21, 1, 0, 0, 0, time.UTC)
	sunsetDay   = time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
)

func TestNightlightToggleFlipsTheFilterAndIcon(t *testing.T) {
	cfg := sunsetConfig(false)
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, cfg, fake.env(t))
	if m.enabled || m.icon.Name() != cfg.Nightlight.IconOff || m.btn.Label().Text() != "Off" {
		t.Fatalf("resting = enabled %v icon %q label %q", m.enabled, m.icon.Name(), m.btn.Label().Text())
	}
	m.RunAction(config.ParseClickAction(":toggle"))
	if !fake.running || !m.enabled || m.icon.Name() != "ld-moon-symbolic" || m.btn.Label().Text() != "On" {
		t.Fatalf("after toggle = running %v enabled %v icon %q", fake.running, m.enabled, m.icon.Name())
	}
	m.RunAction(config.ParseClickAction(":toggle"))
	if fake.running || m.enabled {
		t.Fatal("the second toggle did not stop the filter")
	}
	// Other actions do not toggle.
	m.RunAction(config.ParseClickAction("dropdown:nightlight"))
	if fake.starts != 1 {
		t.Errorf("starts = %d, want only the one toggle", fake.starts)
	}
	// Without the schedule, evaluating drives nothing.
	m.evaluate()
	if fake.running {
		t.Error("evaluate drove the filter with auto-schedule off")
	}
}

func TestNightlightScheduleFollowsTheSun(t *testing.T) {
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, sunsetConfig(true), fake.env(t))
	m.evaluate()
	if !fake.running {
		t.Fatal("night: the schedule did not enable the filter")
	}
	fake.clock = sunsetDay
	m.evaluate()
	if fake.running {
		t.Fatal("day: the schedule did not disable the filter")
	}
}

func TestNightlightManualOverrideHoldsUntilTheBoundary(t *testing.T) {
	fake := &fakeSunset{clock: sunsetNight}
	env := fake.env(t)
	m := newTestNightlight(t, sunsetConfig(true), env)
	m.evaluate() // night: on
	m.toggle()   // the user turns it off at night
	if fake.running {
		t.Fatal("toggle did not stop the filter")
	}
	m.evaluate()
	if fake.running {
		t.Fatal("the schedule overrode the manual toggle within the same phase")
	}
	if rec, ok := nightlight.LoadOverride(env.stateDir, fake.clock); !ok || rec.Enabled || rec.Phase != nightlight.PhaseNight {
		t.Errorf("record = %+v, %v; want night/off", rec, ok)
	}
	// Crossing into day hands control back and drops the record.
	fake.clock = sunsetDay
	m.evaluate()
	if m.manualOverride {
		t.Error("the override survived the phase change")
	}
	if _, ok := nightlight.LoadOverride(env.stateDir, fake.clock); ok {
		t.Error("the record survived the phase change")
	}
	// The next night the schedule drives again.
	fake.clock = sunsetNight.Add(24 * time.Hour)
	m.evaluate()
	if !fake.running {
		t.Error("the schedule did not resume after the boundary")
	}
}

func TestNightlightRestartReplaysTheToggle(t *testing.T) {
	fake := &fakeSunset{clock: sunsetDay}
	env := fake.env(t)
	cfg := sunsetConfig(true)
	nightlight.SaveOverride(env.stateDir, nightlight.Override{Phase: nightlight.PhaseDay, Enabled: true}, sunsetDay.Add(-time.Minute))
	m := newTestNightlight(t, cfg, env)
	if !fake.running || !m.enabled {
		t.Fatal("a remembered manual enable was not replayed")
	}
	// Day would turn it off, but the replayed override holds.
	m.evaluate()
	if !fake.running {
		t.Error("the schedule dropped the replayed override in the same phase")
	}
	// A remembered disable starts nothing.
	fake2 := &fakeSunset{clock: sunsetDay}
	env2 := fake2.env(t)
	nightlight.SaveOverride(env2.stateDir, nightlight.Override{Enabled: false}, sunsetDay)
	newTestNightlight(t, cfg, env2)
	if fake2.starts != 0 {
		t.Error("a remembered disable started the filter")
	}
}

func TestNightlightLocation(t *testing.T) {
	cfg := sunsetConfig(true)
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, cfg, fake.env(t))
	// A GeoClue fix replaces the configured coordinates: at 12:00 UTC
	// it is night in Sydney.
	m.setLocation(nightlight.Location{Latitude: -33.87, Longitude: 151.21})
	if !fake.running {
		t.Error("the resolved location did not drive the schedule")
	}
}

func TestNightlightAppliesTheConfiguredStrength(t *testing.T) {
	cfg := sunsetConfig(false)
	cfg.Nightlight.Temperature, cfg.Nightlight.Gamma = 3200, 85
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.toggle()
	if fake.applied.Temp() != 3200 || fake.applied.Gamma() != 85 {
		t.Errorf("applied %dK %d%%, want 3200K 85%%", fake.applied.Temp(), fake.applied.Gamma())
	}
	m.Stop()
	if !fake.closed {
		t.Error("Stop did not close the filter, which would leave the display tinted")
	}
}

// Another program owning the display colours shows on the module: the
// off icon, the error class and the reason in the tooltip, and it
// clears when the user turns the filter off.
func TestNightlightShowsARefusal(t *testing.T) {
	cfg := sunsetConfig(false)
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, cfg, fake.env(t))
	chrome := widget.NewBox(widget.Row, 0, 0)
	m.setChrome(chrome)
	m.toggle()
	if m.btn.TooltipText() != "" || widget.HasClass(chrome, "nightlight-refused") {
		t.Fatal("a clean apply shows a refusal")
	}
	refusal := &nightlight.RefusedError{Protocol: "zwlr_gamma_control_manager_v1", Output: "DP-1"}
	fake.refused = refusal
	fake.changed()
	if got := m.btn.TooltipText(); got != refusal.Error() {
		t.Errorf("tooltip = %q, want %q", got, refusal.Error())
	}
	if !widget.HasClass(chrome, "nightlight-refused") {
		t.Error("no nightlight-refused class")
	}
	if m.icon.Name() != cfg.Nightlight.IconOff || m.btn.Label().Text() != "Off" {
		t.Errorf("refused shows icon %q label %q, want the off state", m.icon.Name(), m.btn.Label().Text())
	}
	m.toggle()
	if m.btn.TooltipText() != "" || widget.HasClass(chrome, "nightlight-refused") {
		t.Error("turning the filter off kept the refusal")
	}
}

// A strength outside the documented ranges is refused with the range in
// the tooltip instead of being clamped.
func TestNightlightRejectsAnOutOfRangeStrength(t *testing.T) {
	cfg := sunsetConfig(false)
	cfg.Nightlight.Temperature = 500
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.toggle()
	if fake.starts != 0 {
		t.Error("an out-of-range temperature reached the filter")
	}
	if tip := m.btn.TooltipText(); !strings.Contains(tip, "500K") {
		t.Errorf("tooltip = %q, want the rejected temperature", tip)
	}
}

func TestNightlightFailsWithoutAColourProtocol(t *testing.T) {
	env := (&fakeSunset{clock: sunsetDay}).env(t)
	env.open = func(func()) (colourFilter, error) { return nil, nightlight.ErrUnsupported }
	if _, err := newNightlightWith(newTestContext(t, sunsetConfig(false)), env); !errors.Is(err, nightlight.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
