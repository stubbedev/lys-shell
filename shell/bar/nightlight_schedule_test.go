package bar

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		locate: func(context.Context) (nightlight.Location, error) {
			return nightlight.Location{}, errors.New("no GeoClue in this test")
		},
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
	lat, lng := 55.6, 12.5
	cfg.Nightlight.Latitude, cfg.Nightlight.Longitude = &lat, &lng
	return cfg
}

// unpinnedConfig has the schedule on with no coordinates, as a new
// config does.
func unpinnedConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Nightlight.AutoSchedule = true
	return cfg
}

func mustLocation(t *testing.T, latitude, longitude float64) nightlight.Location {
	t.Helper()
	loc, err := nightlight.NewLocation(latitude, longitude)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// captureLog collects the log for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// duskEase is when Copenhagen's dusk transition starts on sunsetDay's
// date, per cfg, and how long it runs.
func duskEase(t *testing.T, cfg *config.Config) (time.Time, time.Duration) {
	t.Helper()
	transition := cfg.Nightlight.TransitionDuration.Transition()
	start, ok := nightlight.NextEase(sunsetDay, mustLocation(t, 55.6, 12.5), transition)
	if !ok {
		t.Fatal("no dusk in reach")
	}
	return start, transition.Duration()
}

// dawnEase is when the dawn transition after that dusk starts.
func dawnEase(t *testing.T, cfg *config.Config) time.Time {
	t.Helper()
	start, length := duskEase(t, cfg)
	next, ok := nightlight.NextEase(start.Add(length), mustLocation(t, 55.6, 12.5), cfg.Nightlight.TransitionDuration.Transition())
	if !ok {
		t.Fatal("no dawn in reach")
	}
	return next
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
	if err := nightlight.SaveOverride(env.stateDir, nightlight.Override{Phase: nightlight.PhaseDay, Enabled: true}, sunsetDay.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
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
	if err := nightlight.SaveOverride(env2.stateDir, nightlight.Override{Enabled: false}, sunsetDay); err != nil {
		t.Fatal(err)
	}
	newTestNightlight(t, cfg, env2)
	if fake2.starts != 0 {
		t.Error("a remembered disable started the filter")
	}
}

// A toggle whose record cannot be saved still toggles, and says so.
func TestNightlightToggleLogsAFailedSave(t *testing.T) {
	logged := captureLog(t)
	fake := &fakeSunset{clock: sunsetDay}
	env := fake.env(t)
	blocker := filepath.Join(env.stateDir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env.stateDir = filepath.Join(blocker, "state")
	m := newTestNightlight(t, sunsetConfig(false), env)
	m.toggle()
	if !fake.running {
		t.Error("a failed save stopped the toggle")
	}
	if got := logged.String(); !strings.Contains(got, "will not survive a restart") || !strings.Contains(got, env.stateDir) {
		t.Errorf("log = %q, want the failed save naming %s", got, env.stateDir)
	}
}

// A toggle that saves logs no failure.
func TestNightlightToggleSavesQuietly(t *testing.T) {
	logged := captureLog(t)
	fake := &fakeSunset{clock: sunsetDay}
	env := fake.env(t)
	m := newTestNightlight(t, sunsetConfig(false), env)
	m.toggle()
	if strings.Contains(logged.String(), "will not survive") {
		t.Errorf("log = %q", logged.String())
	}
	if rec, ok := nightlight.LoadOverride(env.stateDir, sunsetDay); !ok || !rec.Enabled {
		t.Errorf("record = %+v, %v", rec, ok)
	}
}

// With no coordinates configured, a GeoClue fix drives the schedule: at
// 12:00 UTC it is night in Sydney, and a move moves the schedule.
func TestNightlightFollowsGeoClueWithoutCoordinates(t *testing.T) {
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, unpinnedConfig(), fake.env(t))
	m.setLocation(mustLocation(t, -33.87, 151.21))
	if !fake.running {
		t.Error("the resolved location did not drive the schedule")
	}
	m.setLocation(mustLocation(t, 55.6, 12.5))
	if fake.running {
		t.Error("travelling to where it is day left the filter on")
	}
}

// Explicit coordinates are the user's choice: they win, and GeoClue is
// never asked, while it is asked without them.
func TestNightlightPinnedCoordinatesBeatGeoClue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		lookups bool
		running bool
	}{
		{"pinned to Copenhagen, GeoClue in Sydney", sunsetConfig(true), false, false},
		{"unpinned, GeoClue in Sydney", unpinnedConfig(), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSunset{clock: sunsetDay}
			env := fake.env(t)
			var asked atomic.Int32
			located := make(chan struct{}, 8)
			sydney := mustLocation(t, -33.87, 151.21)
			env.locate = func(context.Context) (nightlight.Location, error) {
				asked.Add(1)
				located <- struct{}{}
				return sydney, nil
			}
			m := newTestNightlight(t, tc.cfg, env)
			m.run()
			t.Cleanup(m.Stop)
			if tc.lookups {
				<-located
			} else {
				time.Sleep(lookupGrace)
			}
			waitForSchedule(t, func() bool { return !tc.running || fake.running })
			headlessLoop.Lock()
			defer headlessLoop.Unlock()
			if got := asked.Load() > 0; got != tc.lookups {
				t.Errorf("asked GeoClue = %v, want %v", got, tc.lookups)
			}
			if fake.running != tc.running {
				t.Errorf("running = %v, want %v", fake.running, tc.running)
			}
		})
	}
}

// lookupGrace is how long a lookup that must not happen gets to happen.
const lookupGrace = 100 * time.Millisecond

// waitForSchedule polls under the headless loop's lock until cond holds.
func waitForSchedule(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		headlessLoop.Lock()
		done := cond()
		headlessLoop.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the schedule did not reach the expected state")
}

// No location is a defined state: at 01:00 UTC it is night at 0, 0, and
// the schedule must not act on it.
func TestNightlightWithoutALocationDoesNotFollowASun(t *testing.T) {
	logs := captureLog(t)
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, unpinnedConfig(), fake.env(t))
	chrome := widget.NewBox(widget.Row, 0, 0)
	m.setChrome(chrome)
	m.evaluate()
	if fake.starts != 0 || m.enabled {
		t.Fatal("the schedule drove the filter with no location")
	}
	if m.btn.TooltipText() != "" {
		t.Errorf("tooltip %q while the first lookup is out", m.btn.TooltipText())
	}
	failure := errors.New("geoclue: get client: no such service")
	m.lookupFailed(failure)
	if tip := m.btn.TooltipText(); !strings.Contains(tip, failure.Error()) {
		t.Errorf("tooltip = %q, want the reason", tip)
	}
	if !widget.HasClass(chrome, "nightlight-no-location") || widget.HasClass(chrome, "nightlight-refused") {
		t.Error("the module does not show the no-location state")
	}
	if !strings.Contains(logs.String(), failure.Error()) {
		t.Errorf("log = %q, want the failed lookup", logs.String())
	}
	m.evaluate()
	if fake.starts != 0 {
		t.Error("a failed lookup started the filter")
	}
	// A fix ends the state and the schedule starts.
	m.setLocation(mustLocation(t, 55.6, 12.5))
	if !fake.running {
		t.Error("the first fix did not start the schedule")
	}
	if m.btn.TooltipText() != "" || widget.HasClass(chrome, "nightlight-no-location") {
		t.Error("the fix did not clear the no-location state")
	}
}

// With the schedule off, a failed lookup has nothing to say.
func TestNightlightHidesTheNoLocationStateWithTheScheduleOff(t *testing.T) {
	cfg := config.Defaults()
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, cfg, fake.env(t))
	chrome := widget.NewBox(widget.Row, 0, 0)
	m.setChrome(chrome)
	m.lookupFailed(errors.New("geoclue: down"))
	if m.btn.TooltipText() != "" || widget.HasClass(chrome, "nightlight-no-location") {
		t.Error("the no-location state shows with the schedule off")
	}
}

func TestNightlightLogsARepeatingLookupFailureOnce(t *testing.T) {
	logs := captureLog(t)
	fake := &fakeSunset{clock: sunsetDay}
	m := newTestNightlight(t, unpinnedConfig(), fake.env(t))
	for range 3 {
		m.lookupFailed(errors.New("geoclue: down"))
	}
	if got := strings.Count(logs.String(), "geoclue: down"); got != 1 {
		t.Errorf("logged %d times, want once: %q", got, logs.String())
	}
	m.lookupFailed(errors.New("geoclue: denied"))
	if !strings.Contains(logs.String(), "geoclue: denied") {
		t.Error("a different failure was not logged")
	}
	// Recovering and failing again is news.
	m.setLocation(mustLocation(t, 55.6, 12.5))
	logs.Reset()
	m.lookupFailed(errors.New("geoclue: denied"))
	if !strings.Contains(logs.String(), "geoclue: denied") {
		t.Error("a failure after a recovery was not logged")
	}
}

// A failed lookup keeps the location the schedule already has.
func TestNightlightKeepsItsLocationThroughAFailedLookup(t *testing.T) {
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, unpinnedConfig(), fake.env(t))
	chrome := widget.NewBox(widget.Row, 0, 0)
	m.setChrome(chrome)
	m.setLocation(mustLocation(t, 55.6, 12.5))
	m.lookupFailed(errors.New("geoclue: down"))
	if widget.HasClass(chrome, "nightlight-no-location") {
		t.Error("a failed refresh hid the location the schedule follows")
	}
	fake.clock = sunsetDay
	m.evaluate()
	if fake.running {
		t.Error("the schedule stopped following the location it had")
	}
}

// A spelled-out 0, 0 was the old default: it is ignored with a warning
// and GeoClue is used, never the sun over the Atlantic.
func TestNightlightIgnoresSpelledOutNullCoordinates(t *testing.T) {
	logs := captureLog(t)
	cfg := unpinnedConfig()
	lat, lng := 0.0, 0.0
	cfg.Nightlight.Latitude, cfg.Nightlight.Longitude = &lat, &lng
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	if fake.running {
		t.Error("0, 0 drove the schedule")
	}
	if m.pinned {
		t.Error("0, 0 kept GeoClue out")
	}
	if !strings.Contains(logs.String(), "0, 0") {
		t.Errorf("log = %q, want the warning", logs.String())
	}
	m.setLocation(mustLocation(t, 55.6, 12.5))
	if !fake.running {
		t.Error("GeoClue did not take over from 0, 0")
	}
}

// One coordinate, or an impossible pair, is a mistake to show rather
// than to paper over with GeoClue.
func TestNightlightShowsABadCoordinatePair(t *testing.T) {
	logs := captureLog(t)
	cfg := unpinnedConfig()
	lat := 55.6
	cfg.Nightlight.Latitude = &lat
	fake := &fakeSunset{clock: sunsetNight}
	env := fake.env(t)
	var asked atomic.Int32
	env.locate = func(context.Context) (nightlight.Location, error) {
		asked.Add(1)
		return mustLocation(t, 55.6, 12.5), nil
	}
	m := newTestNightlight(t, cfg, env)
	chrome := widget.NewBox(widget.Row, 0, 0)
	m.setChrome(chrome)
	m.run()
	t.Cleanup(m.Stop)
	time.Sleep(lookupGrace)
	headlessLoop.Lock()
	defer headlessLoop.Unlock()
	if fake.starts != 0 {
		t.Error("half a coordinate pair started the filter")
	}
	if asked.Load() != 0 {
		t.Error("half a coordinate pair fell back to GeoClue")
	}
	if tip := m.btn.TooltipText(); !strings.Contains(tip, "both latitude and longitude") {
		t.Errorf("tooltip = %q, want the mistake", tip)
	}
	if !widget.HasClass(chrome, "nightlight-no-location") {
		t.Error("no nightlight-no-location class")
	}
	if !strings.Contains(logs.String(), "both latitude and longitude") {
		t.Errorf("log = %q, want the mistake", logs.String())
	}
}

// A remembered fix lets a restart at night tint at once; a config that
// pins the location ignores it.
func TestNightlightRestartUsesTheRememberedFix(t *testing.T) {
	fake := &fakeSunset{clock: sunsetNight}
	env := fake.env(t)
	first := newTestNightlight(t, unpinnedConfig(), env)
	first.setLocation(mustLocation(t, 55.6, 12.5))
	first.Stop()

	again := &fakeSunset{clock: sunsetNight}
	restartEnv := again.env(t)
	restartEnv.stateDir = env.stateDir
	m := newTestNightlight(t, unpinnedConfig(), restartEnv)
	m.evaluate()
	if !again.running {
		t.Error("the restart did not apply the remembered fix")
	}

	pinnedDay := sunsetConfig(true)
	if err := nightlight.SaveLocation(env.stateDir, mustLocation(t, -33.87, 151.21)); err != nil {
		t.Fatal(err)
	}
	pinned := &fakeSunset{clock: sunsetDay}
	pinnedEnv := pinned.env(t)
	pinnedEnv.stateDir = env.stateDir
	p := newTestNightlight(t, pinnedDay, pinnedEnv)
	p.evaluate()
	if pinned.running {
		t.Error("a remembered fix in Sydney beat the pinned coordinates")
	}
}

func TestNightlightEasesIntoAndOutOfNight(t *testing.T) {
	cfg := sunsetConfig(true)
	cfg.Nightlight.Temperature, cfg.Nightlight.Gamma = 3000, 80
	start, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: start.Add(-time.Second)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	if fake.running {
		t.Fatal("the filter is on before the ease")
	}
	fake.clock = start
	m.evaluate()
	if fake.running || !m.stepping {
		t.Fatalf("the first moment shows %v, stepping %v; want no filter yet, stepping", fake.running, m.stepping)
	}
	lastTemp := nightlight.MaxTemp
	for at := time.Minute; at < length; at += time.Minute {
		fake.clock = start.Add(at)
		m.evaluate()
		if !fake.running || !m.stepping {
			t.Fatalf("at %v: running %v stepping %v", at, fake.running, m.stepping)
		}
		if fake.applied.Temp() >= lastTemp || fake.applied.Temp() <= 3000 {
			t.Fatalf("at %v: temp %d after %d", at, fake.applied.Temp(), lastTemp)
		}
		lastTemp = fake.applied.Temp()
	}
	if fake.stops != 0 {
		t.Errorf("the ease cleared the filter %d times", fake.stops)
	}
	fake.clock = start.Add(length)
	m.evaluate()
	if fake.applied.Temp() != 3000 || fake.applied.Gamma() != 80 || m.stepping {
		t.Errorf("the end shows %dK %d%%, stepping %v", fake.applied.Temp(), fake.applied.Gamma(), m.stepping)
	}
	starts := fake.starts
	m.evaluate()
	if fake.starts != starts {
		t.Error("a settled night re-applied the same strength")
	}

	dawn := dawnEase(t, cfg)
	fake.clock = dawn
	m.evaluate()
	if fake.applied.Temp() != 3000 || !fake.running {
		t.Errorf("the dawn ease starts at %dK, running %v; want the full strength", fake.applied.Temp(), fake.running)
	}
	fake.clock = dawn.Add(length / 2)
	m.evaluate()
	if !fake.running || fake.applied.Temp() <= 3000 {
		t.Errorf("mid dawn shows %dK, running %v", fake.applied.Temp(), fake.running)
	}
	fake.clock = dawn.Add(length)
	m.evaluate()
	if fake.running {
		t.Error("the filter is on after the dawn ease")
	}
}

// A restart mid-transition lands on that moment's value, with one apply
// and never the full strength on the way.
func TestNightlightRestartMidEaseLandsOnTheMoment(t *testing.T) {
	cfg := sunsetConfig(true)
	cfg.Nightlight.Temperature = 3000
	start, length := duskEase(t, cfg)
	now := start.Add(length / 3)
	fake := &fakeSunset{clock: now}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	night, err := nightlight.NewSetting(3000, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := nightlight.TargetAt(now, mustLocation(t, 55.6, 12.5), cfg.Nightlight.TransitionDuration.Transition(), night)
	if fake.starts != 1 || fake.applied != want.Setting {
		t.Errorf("%d applies landing on %+v, want one on %+v", fake.starts, fake.applied, want.Setting)
	}
	if fake.applied.Temp() == 3000 {
		t.Error("the restart flashed to the full strength")
	}
}

// A toggle during an ease applies at once and the next ticks leave it
// alone, past the solar event in the ease's middle, until the next ease.
func TestNightlightToggleCancelsARunningEase(t *testing.T) {
	cfg := sunsetConfig(true)
	cfg.Nightlight.Temperature = 3000
	start, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: start.Add(length / 4)}
	env := fake.env(t)
	m := newTestNightlight(t, cfg, env)
	m.evaluate()
	if !fake.running {
		t.Fatal("the ease did not start")
	}
	m.toggle()
	if fake.running || m.stepping {
		t.Fatalf("the toggle left running %v, stepping %v", fake.running, m.stepping)
	}
	for _, at := range []time.Duration{length / 4, length / 2, length * 3 / 4, length, 2 * length} {
		fake.clock = start.Add(at)
		m.evaluate()
		if fake.running || m.stepping {
			t.Fatalf("at %v the schedule overwrote the toggle", at)
		}
	}
	if _, ok := nightlight.LoadOverride(env.stateDir, fake.clock); !ok {
		t.Error("the record did not survive the event inside the ease")
	}
	dawn := dawnEase(t, cfg)
	fake.clock = dawn.Add(-time.Second)
	m.evaluate()
	if fake.running {
		t.Error("the override did not hold up to the next ease")
	}
	fake.clock = dawn.Add(length)
	m.evaluate()
	if m.manualOverride {
		t.Error("the override outlived the next ease")
	}
}

func TestNightlightToggleOnMidEaseAppliesTheFullStrength(t *testing.T) {
	cfg := sunsetConfig(true)
	cfg.Nightlight.Temperature = 3000
	start, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: start.Add(length / 2)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	m.toggle() // off
	m.toggle() // on
	if !fake.running || fake.applied.Temp() != 3000 {
		t.Errorf("toggled on shows %dK, running %v; want the full strength", fake.applied.Temp(), fake.running)
	}
	fake.clock = start.Add(length * 3 / 4)
	before := fake.starts
	m.evaluate()
	if fake.starts != before {
		t.Error("the next tick overwrote the toggle")
	}
}

// A toggle already showing where the new phase's ease ends is not thrown
// back to the ease's start: a night turned off stays off through dawn.
func TestNightlightOverrideAtTheDestinationSkipsTheEase(t *testing.T) {
	cfg := sunsetConfig(true)
	start, length := duskEase(t, cfg)
	dawn := dawnEase(t, cfg)

	fake := &fakeSunset{clock: start.Add(2 * length)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	m.toggle()
	fake.clock = dawn
	m.evaluate()
	if fake.running {
		t.Fatal("dawn's ease flashed the filter on after it was turned off for the night")
	}
	fake.clock = dawn.Add(length / 2)
	m.evaluate()
	if fake.running {
		t.Fatal("dawn's ease turned the filter on midway")
	}
	if m.manualOverride {
		t.Error("the override outlived the phase change")
	}

	fake = &fakeSunset{clock: start.Add(-time.Hour)}
	m = newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	m.toggle()
	if !fake.running {
		t.Fatal("the toggle did not turn the filter on")
	}
	stops := fake.stops
	fake.clock = start
	m.evaluate()
	if fake.stops != stops || !fake.running {
		t.Fatal("dusk's ease dropped a filter already on")
	}
	fake.clock = start.Add(length)
	m.evaluate()
	if !fake.running || fake.applied.Temp() != int(cfg.Nightlight.Temperature) {
		t.Errorf("after dusk's ease: running %v at %dK", fake.running, fake.applied.Temp())
	}
}

// Without an override in the way the ease runs even when the module
// already matches its end, as after a restart mid-dawn with the filter
// off: the moment's value, not a skip.
func TestNightlightOnlyAnOverrideSkipsAnEase(t *testing.T) {
	cfg := sunsetConfig(true)
	dawn := dawnEase(t, cfg)
	_, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: dawn.Add(length / 2)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	if !fake.running || !m.stepping {
		t.Errorf("mid dawn after a restart: running %v, stepping %v", fake.running, m.stepping)
	}
}

func TestNightlightZeroTransitionSwitchesAtOnce(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	if err := osWrite(path, "modules:\n  nightlight:\n    auto-schedule: true\n    latitude: 55.6\n    longitude: 12.5\n    transition-duration: 0\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dusk, length := duskEase(t, cfg)
	if length != 0 {
		t.Fatalf("length = %v", length)
	}
	fake := &fakeSunset{clock: dusk.Add(-time.Second)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	if fake.running || m.stepping {
		t.Fatal("the filter moved before dusk")
	}
	fake.clock = dusk
	m.evaluate()
	if !fake.running || m.stepping || fake.applied.Temp() != int(cfg.Nightlight.Temperature) {
		t.Errorf("at dusk: running %v stepping %v at %dK", fake.running, m.stepping, fake.applied.Temp())
	}
	if fake.starts != 1 {
		t.Errorf("%d applies, want the one", fake.starts)
	}
}

// The module wakes every second only inside a transition, otherwise at
// most once a minute, and on time for the next transition.
func TestNightlightTicksFastOnlyDuringAnEase(t *testing.T) {
	cfg := sunsetConfig(true)
	start, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: start.Add(-time.Hour)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.evaluate()
	if got := m.nextTick(); got != nightlightScheduleTick {
		t.Errorf("idle tick = %v, want %v", got, nightlightScheduleTick)
	}
	fake.clock = start.Add(-10 * time.Second)
	m.evaluate()
	if got := m.nextTick(); got != 10*time.Second {
		t.Errorf("just before the ease = %v, want to wake at its start", got)
	}
	fake.clock = start.Add(length / 2)
	m.evaluate()
	if got := m.nextTick(); got != nightlightEaseTick {
		t.Errorf("during the ease = %v, want %v", got, nightlightEaseTick)
	}
	m.toggle()
	if got := m.nextTick(); got == nightlightEaseTick {
		t.Error("an override keeps the fast tick")
	}
	m.toggle()
	fake.clock = start.Add(2 * length)
	m.evaluate()
	if got := m.nextTick(); got != nightlightScheduleTick {
		t.Errorf("after the ease = %v, want %v", got, nightlightScheduleTick)
	}
}

// The running module steps by itself on its timer, and stops with Stop.
func TestNightlightStepsOnItsTimer(t *testing.T) {
	cfg := sunsetConfig(true)
	start, length := duskEase(t, cfg)
	fake := &fakeSunset{clock: start.Add(length / 4)}
	m := newTestNightlight(t, cfg, fake.env(t))
	m.run()
	waitForSchedule(t, func() bool { return fake.running })
	headlessLoop.Lock()
	fake.clock = start.Add(length / 2)
	first := fake.applied
	applied := fake.starts
	headlessLoop.Unlock()
	waitForSchedule(t, func() bool { return fake.applied != first && fake.starts > applied })
	headlessLoop.Lock()
	m.Stop()
	stopped := fake.starts
	fake.clock = start.Add(length * 3 / 4)
	headlessLoop.Unlock()
	time.Sleep(nightlightEaseTick + 100*time.Millisecond)
	headlessLoop.Lock()
	defer headlessLoop.Unlock()
	if fake.starts != stopped {
		t.Error("the module stepped after Stop")
	}
}

// With the schedule off nothing wakes the module at all.
func TestNightlightSetsNoTimerWithTheScheduleOff(t *testing.T) {
	fake := &fakeSunset{clock: sunsetNight}
	m := newTestNightlight(t, sunsetConfig(false), fake.env(t))
	m.run()
	t.Cleanup(m.Stop)
	if m.timer != nil {
		t.Error("the schedule armed a timer while off")
	}
	if fake.starts != 0 {
		t.Error("the schedule drove the filter while off")
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
