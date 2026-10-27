package bar

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/service/nightlight"
)

// nightlightLocationEach is how often the schedule refreshes its
// GeoClue location; the schedule itself ticks every minute.
const (
	nightlightScheduleTick = time.Minute
	nightlightLocationEach = 6 * time.Hour
)

// nightlightLabel renders the format: On/Off status with the applied
// temp and gamma while on, "--" placeholders when off; the configured
// values are always on their own placeholders.
func nightlightLabel(format string, applied bool, temp, gamma int) string {
	configTemp, configGamma := strconv.Itoa(temp), strconv.Itoa(gamma)
	status, tempText, gammaText := i18n.T("bar-nightlight-off"), "--", "--"
	if applied {
		status, tempText, gammaText = i18n.T("bar-nightlight-on"), configTemp, configGamma
	}
	return jinja.RenderOr(format, map[string]any{
		"status": status, "temp": tempText, "gamma": gammaText,
		"config_temp": configTemp, "config_gamma": configGamma,
	})
}

// colourFilter is the display filter the module drives.
type colourFilter interface {
	Protocol() string
	Apply(nightlight.Setting) error
	Clear() error
	Refused() error
	Close()
}

// sunsetEnv is everything the module touches outside itself; the shell
// wires the Wayland filter, clock, state dir, and GeoClue, tests fake
// them.
type sunsetEnv struct {
	// open connects the filter; changed runs on any goroutine when its
	// refusal may have changed.
	open     func(changed func()) (colourFilter, error)
	now      func() time.Time
	stateDir string
	// locate resolves the schedule location; nil disables the lookup.
	locate func(ctx context.Context) (nightlight.Location, error)
}

// liveSunsetEnv is the real environment.
func liveSunsetEnv() sunsetEnv {
	dir, _ := xdg.StateDir()
	return sunsetEnv{
		open: func(changed func()) (colourFilter, error) {
			f, err := nightlight.Open(changed)
			if err != nil {
				return nil, err
			}
			return f, nil
		},
		now:      time.Now,
		stateDir: dir,
		locate: func(ctx context.Context) (nightlight.Location, error) {
			conn, err := dbus.ConnectSystemBus()
			if err != nil {
				return nightlight.Location{}, err
			}
			defer func() { _ = conn.Close() }()
			return nightlight.QueryLocation(ctx, conn)
		},
	}
}

// nightlightModule is the night-light toggle with the solar
// auto-schedule.
type nightlightModule struct {
	ctx    ModuleContext
	env    sunsetEnv
	filter colourFilter
	btn    *barButton
	icon   *widget.Icon
	chrome moduleChrome

	enabled bool
	// refused is why the filter is not applied although the module is
	// on: another program owns the display colours, or the configured
	// strength is out of range.
	refused error
	// autoPhase is the last phase the schedule applied (zero while the
	// schedule is off); manualOverride holds a manual toggle until the
	// next sunrise/sunset.
	autoPhase      nightlight.Phase
	manualOverride bool
	// geo is the GeoClue location, preferred over the configured one.
	geo    *nightlight.Location
	cancel context.CancelFunc
}

func newNightlight(ctx ModuleContext) (Module, error) {
	return newNightlightWith(ctx, liveSunsetEnv())
}

// newNightlightWith builds the module over env: it connects the filter,
// replays a remembered manual toggle, then (with the loop) ticks the
// schedule and resolves the location. A compositor without a colour
// protocol fails the module.
func newNightlightWith(ctx ModuleContext, env sunsetEnv) (*nightlightModule, error) {
	cfg := ctx.Config.Nightlight
	m := &nightlightModule{ctx: ctx, env: env}
	filter, err := env.open(func() { ctx.Invoke(m.refreshRefusal) })
	if err != nil {
		return nil, err
	}
	m.filter = filter
	m.icon = moduleIcon(ctx, cfg.IconOffView())
	m.btn = newBarButton(ctx, m.icon, "")

	// The filter dies with the shell, so a manual toggle is replayed
	// rather than lost to the schedule.
	if restored, ok := nightlight.LoadOverride(env.stateDir, env.now()); ok {
		m.autoPhase, m.manualOverride = restored.Phase, true
		if restored.Enabled {
			m.startFilter()
		}
	}
	m.render()
	if ctx.App != nil {
		m.run()
	}
	return m, nil
}

// run starts the watchers: the 60s schedule tick and the GeoClue
// lookup (at start and every six hours while the schedule is on).
func (m *nightlightModule) run() {
	runCtx, cancel := context.WithCancel(m.ctx.Life())
	m.cancel = cancel
	go func() {
		tick := time.NewTicker(nightlightScheduleTick)
		defer tick.Stop()
		m.ctx.Invoke(m.evaluate)
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				m.ctx.Invoke(m.evaluate)
			}
		}
	}()
	if m.ctx.Config.Nightlight.AutoSchedule && m.env.locate != nil {
		go func() {
			for {
				lookup, done := context.WithTimeout(runCtx, 30*time.Second)
				loc, err := m.env.locate(lookup)
				done()
				if err == nil {
					m.ctx.Invoke(func() { m.setLocation(loc) })
				}
				select {
				case <-runCtx.Done():
					return
				case <-time.After(nightlightLocationEach):
				}
			}
		}()
	}
}

// Stop ends the watchers and lets go of the display colours.
func (m *nightlightModule) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.filter.Close()
}

func (m *nightlightModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.render()
}

// render shows the state icon and the label; a refused filter tints the
// icon and says why in the tooltip.
func (m *nightlightModule) render() {
	cfg := m.ctx.Config.Nightlight
	applied := m.enabled && m.refused == nil
	text := ""
	if cfg.LabelShow {
		text = nightlightLabel(cfg.Format, applied, int(cfg.Temperature), int(cfg.Gamma))
	}
	m.btn.SetLabel(text)
	if m.icon != nil {
		icon := cfg.IconOffView()
		if applied {
			icon = cfg.IconOnView()
		}
		m.icon.SetThemeName(icon.Name)
	}
	tooltip := ""
	if m.enabled && m.refused != nil {
		tooltip = m.refused.Error()
	}
	setClass(m.chrome, "nightlight-refused", tooltip != "")
	m.btn.SetTooltip(tooltip)
}

// refreshRefusal picks up a refusal the compositor sent after the
// filter applied.
func (m *nightlightModule) refreshRefusal() {
	if !m.enabled {
		return
	}
	refused := m.filter.Refused()
	if errors.Is(refused, m.refused) {
		return
	}
	if refused != nil {
		log.Printf("nightlight: %v", refused)
	}
	m.refused = refused
	m.render()
}

// currentPhase is the phase here and now: the GeoClue location when
// resolved, else the configured coordinates.
func (m *nightlightModule) currentPhase() nightlight.Phase {
	cfg := m.ctx.Config.Nightlight
	lat, lng := cfg.Latitude, cfg.Longitude
	if m.geo != nil {
		lat, lng = m.geo.Latitude, m.geo.Longitude
	}
	return nightlight.PhaseAt(m.env.now(), lat, lng)
}

// setLocation is LocationResolved: a new fix re-evaluates at once.
func (m *nightlightModule) setLocation(loc nightlight.Location) {
	if m.geo != nil && *m.geo == loc {
		return
	}
	m.geo = &loc
	m.evaluate()
}

// evaluate is evaluate_schedule: night turns the filter on, day off; a
// manual toggle holds until the phase changes, which clears it and the
// remembered record.
func (m *nightlightModule) evaluate() {
	if !m.ctx.Config.Nightlight.AutoSchedule {
		m.autoPhase, m.manualOverride = 0, false
		return
	}
	phase := m.currentPhase()
	if m.autoPhase != phase {
		m.autoPhase = phase
		m.manualOverride = false
		nightlight.ClearOverride(m.env.stateDir)
	}
	if m.manualOverride {
		return
	}
	if wantOn := phase == nightlight.PhaseNight; wantOn != m.enabled {
		if wantOn {
			m.startFilter()
		} else {
			m.stopFilter()
		}
	}
}

// RunAction handles the module's own :toggle; the rest fall through.
func (m *nightlightModule) RunAction(action config.ClickAction) {
	if action.Kind == config.ClickShell && action.Command == ":toggle" {
		m.toggle()
		return
	}
	runClickAction(m.ctx, action)
}

// toggle is the left-click :toggle: under the schedule it sets the
// override (seeding the phase when the schedule has not ticked yet),
// records the new state for restarts, and flips the filter.
func (m *nightlightModule) toggle() {
	if m.ctx.Config.Nightlight.AutoSchedule {
		m.manualOverride = true
		if m.autoPhase == 0 {
			m.autoPhase = m.currentPhase()
		}
	}
	nightlight.SaveOverride(m.env.stateDir, nightlight.Override{Phase: m.autoPhase, Enabled: !m.enabled}, m.env.now())
	if m.enabled {
		m.stopFilter()
		return
	}
	m.startFilter()
}

// startFilter applies the configured strength to every output.
func (m *nightlightModule) startFilter() {
	cfg := m.ctx.Config.Nightlight
	m.enabled, m.refused = true, nil
	setting, err := nightlight.NewSetting(int(cfg.Temperature), int(cfg.Gamma))
	if err == nil {
		err = m.filter.Apply(setting)
	}
	if err == nil {
		err = m.filter.Refused()
	}
	if err != nil {
		log.Printf("nightlight: %v", err)
		m.refused = err
	} else {
		log.Printf("nightlight: on at %dK %d%% through %s", setting.Temp(), setting.Gamma(), m.filter.Protocol())
	}
	m.render()
}

// stopFilter returns the outputs to their own colours.
func (m *nightlightModule) stopFilter() {
	if err := m.filter.Clear(); err != nil {
		log.Printf("nightlight: %v", err)
	} else {
		log.Printf("nightlight: off")
	}
	m.enabled, m.refused = false, nil
	m.render()
}

func (m *nightlightModule) Root() widget.Widget { return m.btn }
