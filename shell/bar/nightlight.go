package bar

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"strconv"
	"sync"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/clockwatch"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/service/nightlight"
)

// The schedule's cadence. A jump of the clock, as a resume from suspend
// is, wakes it at once; otherwise it wakes every nightlightScheduleTick,
// all it has should the kernel refuse to report jumps, and steps every
// nightlightEaseTick inside a transition, where 900 steps over the
// default 15 minutes keep each one below what shows. A location refreshes every
// nightlightLocationEach, and a failed lookup retries sooner so a
// GeoClue that comes up after the shell is picked up.
const (
	nightlightScheduleTick  = time.Minute
	nightlightEaseTick      = time.Second
	nightlightLocationEach  = 6 * time.Hour
	nightlightLocationRetry = 5 * time.Minute
	nightlightLookupTimeout = 30 * time.Second
)

// nightlightLabel renders the format: On/Off status with the live temp
// and gamma while the filter applies (nil live when it does not), "--"
// placeholders otherwise; the configured values are always on their own
// placeholders.
func nightlightLabel(format string, live *nightlight.Setting, temp, gamma int) string {
	return jinja.RenderOr(format, nightlightVars(live, temp, gamma))
}

func nightlightVars(live *nightlight.Setting, temp, gamma int) map[string]any {
	status, tempText, gammaText := i18n.T("bar-nightlight-off"), "--", "--"
	if live != nil {
		status, tempText, gammaText = i18n.T("bar-nightlight-on"), strconv.Itoa(live.Temp()), strconv.Itoa(live.Gamma())
	}
	return map[string]any{
		"status": status, "temp": tempText, "gamma": gammaText,
		"config_temp": strconv.Itoa(temp), "config_gamma": strconv.Itoa(gamma),
	}
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
	// locate resolves the schedule location.
	locate func(ctx context.Context) (nightlight.Location, error)
	// clockJumps calls onJump on every jump of the wall clock until ctx
	// ends, and returns why not if it cannot.
	clockJumps func(ctx context.Context, onJump func()) error
}

// liveSunsetEnv is the real environment, its state dir resolved from
// vars. It fails when they name none: the records would otherwise land
// in whatever directory the shell started from.
func liveSunsetEnv(vars xdg.Env, system *dbus.Conn) (sunsetEnv, error) {
	dir, ok := vars.StateDir(version.Name)
	if !ok {
		return sunsetEnv{}, errors.New("nightlight: no state directory: neither XDG_STATE_HOME nor HOME is an absolute path")
	}
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
			if system == nil {
				return nightlight.Location{}, errors.New("geoclue: the shell has no system bus connection")
			}
			return nightlight.QueryLocation(ctx, system)
		},
		clockJumps: clockwatch.Watch,
	}, nil
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
	firstRead

	// night is the configured full strength, and nightErr why it is
	// unusable, which stops the schedule and refuses a toggle.
	night    nightlight.Setting
	nightErr error

	enabled bool
	// live is the strength on the display while enabled.
	live nightlight.Setting
	// refused is why the filter is not applied although the module is
	// on: another program owns the display colours, or the configured
	// strength is out of range.
	refused error
	// autoPhase is the last phase the schedule applied (zero while the
	// schedule is off); manualOverride holds a manual toggle until the
	// next phase.
	autoPhase      nightlight.Phase
	manualOverride bool
	// heldThroughEase keeps a toggle that already shows where the new
	// phase's transition is heading, so the transition does not snap
	// back to its start only to return.
	heldThroughEase bool
	// stepping is whether the last evaluation is driving a transition.
	stepping bool

	// location is what the schedule follows, nil with none; locationErr
	// is why there is none, nil while the first lookup is out.
	location    *nightlight.Location
	locationErr error
	// pinned is the config naming the location, so GeoClue stays out.
	pinned bool
	// lookupErr is the last failed lookup's text, so a failure repeating
	// on every retry logs once.
	lookupErr string

	// life ends with Stop; nil until run, which only the loop reaches.
	life   context.Context
	cancel context.CancelFunc
	timer  *time.Timer
}

func newNightlight(ctx ModuleContext) (Module, error) {
	env, err := liveSunsetEnv(xdg.OS, ctx.System)
	if err != nil {
		return nil, err
	}
	return newNightlightWith(ctx, env), nil
}

// newNightlightWith builds the module over env: it connects the filter,
// settles what the schedule follows, replays a remembered manual toggle,
// then (with the loop) ticks the schedule and resolves the location. A
// compositor without a colour protocol fails the module.
func newNightlightWith(ctx ModuleContext, env sunsetEnv) *nightlightModule {
	cfg := ctx.Config.Nightlight
	m := &nightlightModule{ctx: ctx, env: env}
	ctx.OnRetire(m.Stop)
	m.icon = moduleIcon(ctx, cfg.IconOffView())
	m.btn = newBarButton(ctx, m.icon, "")

	if m.night, m.nightErr = nightlight.NewSetting(int(cfg.Temperature), int(cfg.Gamma)); m.nightErr != nil {
		log.Printf("nightlight: %v", m.nightErr)
		m.refused = m.nightErr
	}
	if cfg.AutoSchedule {
		m.resolvePinnedLocation()
	}
	m.render()
	m.onOwner(func() {
		filter, err := env.open(func() { m.onOwner(m.readRefusal) })
		if err != nil {
			log.Printf("nightlight: %v", err)
			return
		}
		m.filter = filter
		var remembered *nightlight.Location
		if cfg.AutoSchedule && !m.pinned {
			remembered = m.readRememberedLocation()
		}
		restored, hasRestored := nightlight.LoadOverride(env.stateDir, env.now())
		m.onLoop(func() {
			if remembered != nil && m.location == nil {
				m.location = remembered
			}
			// The filter dies with the shell, so a manual toggle is replayed
			// rather than lost to the schedule.
			if hasRestored {
				m.autoPhase, m.manualOverride = restored.Phase, true
				if restored.Enabled {
					m.startFilter()
				}
			}
			m.render()
			m.reveal()
			if ctx.App != nil {
				m.run()
			}
		})
	})
	return m
}

// nightlightOwner runs every filter call and state-file write in order, for
// every bar and generation: a generation's filter closes before the next one's
// opens, and nothing the compositor or the disk holds up reaches the loop.
var nightlightOwner serialWorker

func (m *nightlightModule) onOwner(job func()) {
	if m.ctx.App == nil {
		job()
		return
	}
	nightlightOwner.do(job)
}

func (m *nightlightModule) onLoop(fn func()) {
	if m.ctx.App == nil {
		fn()
		return
	}
	m.ctx.Invoke(fn)
}

// serialWorker runs jobs in order on one goroutine, there only while it has
// work.
type serialWorker struct {
	mu      sync.Mutex
	queue   []func()
	running bool
}

func (w *serialWorker) do(job func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.queue = append(w.queue, job)
	if !w.running {
		w.running = true
		go w.drain()
	}
}

func (w *serialWorker) drain() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		job := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		job()
	}
}

// resolvePinnedLocation settles what the config says the schedule follows. A
// spelled-out 0, 0 counts as unset, as it was the former default; any other
// unusable pair stops the schedule rather than falling to GeoClue behind the
// user's back.
func (m *nightlightModule) resolvePinnedLocation() {
	pinned, ok, err := m.ctx.Config.Nightlight.PinnedLocation()
	switch {
	case errors.Is(err, nightlight.ErrNullIsland):
		log.Printf("nightlight: ignoring latitude and longitude: %v; using GeoClue", err)
	case err != nil:
		m.pinned, m.locationErr = true, err
		log.Printf("nightlight: the auto-schedule has no location: %v", err)
		return
	case ok:
		m.pinned, m.location = true, &pinned
		return
	}
}

// readRememberedLocation is the last fix on disk, nil with none.
func (m *nightlightModule) readRememberedLocation() *nightlight.Location {
	remembered, err := nightlight.LoadLocation(m.env.stateDir)
	switch {
	case err == nil:
		return &remembered
	case !errors.Is(err, fs.ErrNotExist):
		log.Printf("nightlight: %v", err)
	}
	return nil
}

// run starts the watchers: the schedule's timer and, with the schedule
// on, the clock and, unless the config pins the location, the GeoClue
// lookup.
func (m *nightlightModule) run() {
	m.life, m.cancel = context.WithCancel(m.ctx.Life())
	m.ctx.Invoke(m.tick)
	if !m.ctx.Config.Nightlight.AutoSchedule {
		return
	}
	go m.followClock(m.life)
	if !m.pinned {
		go m.followLocation(m.life)
	}
}

// followClock re-evaluates the schedule the moment the clock jumps.
func (m *nightlightModule) followClock(ctx context.Context) {
	err := m.env.clockJumps(ctx, func() { m.ctx.Invoke(m.tick) })
	if err != nil {
		log.Printf("nightlight: a resume can lag by up to %s: %v", nightlightScheduleTick, err)
	}
}

// followLocation looks the location up at start and then every
// nightlightLocationEach, sooner after a failure.
func (m *nightlightModule) followLocation(ctx context.Context) {
	for {
		lookup, done := context.WithTimeout(ctx, nightlightLookupTimeout)
		loc, err := m.env.locate(lookup)
		done()
		if ctx.Err() != nil {
			return
		}
		wait := nightlightLocationEach
		if err != nil {
			wait = nightlightLocationRetry
			m.ctx.Invoke(func() { m.lookupFailed(err) })
		} else {
			m.ctx.Invoke(func() { m.setLocation(loc) })
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// tick is one schedule wakeup: evaluate, then sleep until the next.
func (m *nightlightModule) tick() {
	if m.life.Err() != nil {
		return
	}
	m.evaluate()
	m.armTick()
}

// armTick sets the timer for the next wakeup. Nothing wakes the module
// without a schedule to follow; setLocation arms it when one arrives.
func (m *nightlightModule) armTick() {
	if m.life == nil || m.life.Err() != nil || !m.ctx.Config.Nightlight.AutoSchedule || m.location == nil {
		return
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(m.nextTick(), func() { m.ctx.Invoke(m.tick) })
}

// nextTick is how long the schedule sleeps: a step while a transition
// runs, otherwise a minute or until the next transition starts,
// whichever comes first, so a transition starts on time.
func (m *nightlightModule) nextTick() time.Duration {
	if m.stepping {
		return nightlightEaseTick
	}
	now := m.env.now()
	next, ok := nightlight.NextEase(now, *m.location, m.ctx.Config.Nightlight.TransitionDuration.Transition())
	if !ok {
		return nightlightScheduleTick
	}
	return min(nightlightScheduleTick, next.Sub(now))
}

// Stop ends the watchers and lets go of the display colours.
func (m *nightlightModule) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.onOwner(func() {
		if m.filter != nil {
			m.filter.Close()
		}
	})
}

func (m *nightlightModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.hideUntilRead(c)
	m.render()
}

// render shows the state icon, the label and any problem: a refused
// filter or a schedule without a location tints the icon and says why in
// the tooltip.
func (m *nightlightModule) render() {
	cfg := m.ctx.Config.Nightlight
	m.renderLabel()
	if m.icon != nil {
		icon := cfg.IconOffView()
		if m.enabled && m.refused == nil {
			icon = cfg.IconOnView()
		}
		m.icon.SetThemeName(icon.Name)
	}
	refusal, noLocation := "", ""
	switch {
	case m.refused != nil:
		refusal = m.refused.Error()
	case cfg.AutoSchedule && m.location == nil && m.locationErr != nil:
		noLocation = i18n.T("bar-nightlight-no-location", i18n.Str("reason", m.locationErr.Error()))
	}
	setClass(m.chrome, "nightlight-refused", refusal != "")
	setClass(m.chrome, "nightlight-no-location", noLocation != "")
	m.btn.SetTooltip(refusal + noLocation)
}

// renderLabel shows the label alone, all a transition step changes.
func (m *nightlightModule) renderLabel() {
	cfg := m.ctx.Config.Nightlight
	if !cfg.LabelShow {
		m.btn.SetLabel("")
		return
	}
	var live *nightlight.Setting
	if m.enabled && m.refused == nil {
		live = &m.live
	}
	m.btn.SetLabel(nightlightLabel(cfg.Format, live, int(cfg.Temperature), int(cfg.Gamma)))
}

// readRefusal asks the filter, on its owner, about a refusal the compositor
// sent after it applied.
func (m *nightlightModule) readRefusal() {
	if m.filter == nil {
		return
	}
	refused := m.filter.Refused()
	m.onLoop(func() { m.refreshRefusal(refused) })
}

func (m *nightlightModule) refreshRefusal(refused error) {
	if !m.enabled {
		return
	}
	if errors.Is(refused, m.refused) {
		return
	}
	if refused != nil {
		log.Printf("nightlight: %v", refused)
	}
	m.refused = refused
	m.render()
}

// setLocation is LocationResolved: a new fix is remembered for the next
// start and re-evaluates at once. Only GeoClue calls it, and never with
// a pinned location.
func (m *nightlightModule) setLocation(loc nightlight.Location) {
	m.lookupErr, m.locationErr = "", nil
	if m.location != nil && *m.location == loc {
		return
	}
	m.location = &loc
	log.Printf("nightlight: the location changed, following GeoClue")
	m.onOwner(func() {
		if err := nightlight.SaveLocation(m.env.stateDir, loc); err != nil {
			log.Printf("nightlight: %v", err)
		}
	})
	m.evaluate()
	m.armTick()
	m.render()
}

// lookupFailed logs a failed lookup, once while it keeps failing the
// same way. The schedule keeps the location it has; with none, the
// module shows why.
func (m *nightlightModule) lookupFailed(err error) {
	if text := err.Error(); text != m.lookupErr {
		m.lookupErr = text
		log.Printf("nightlight: the location lookup failed: %v", err)
	}
	m.locationErr = err
	m.render()
}

// evaluate is evaluate_schedule: the filter shows the schedule's target,
// off when that is the identity; a manual toggle holds until the phase
// changes, which clears it and the remembered record. With no location
// or no usable strength there is no target and the filter stays as it
// is.
func (m *nightlightModule) evaluate() {
	cfg := m.ctx.Config.Nightlight
	if !cfg.AutoSchedule {
		m.autoPhase, m.manualOverride = 0, false
		return
	}
	m.stepping = false
	if m.location == nil || m.nightErr != nil {
		return
	}
	target := nightlight.TargetAt(m.env.now(), *m.location, cfg.TransitionDuration.Transition(), m.night)
	switch {
	case m.manualOverride && m.autoPhase == 0:
		m.autoPhase = target.Phase
	case m.autoPhase != target.Phase:
		m.heldThroughEase = m.manualOverride && m.enabled == (target.Phase == nightlight.PhaseNight)
		m.autoPhase = target.Phase
		m.manualOverride = false
		m.onOwner(func() {
			if err := nightlight.ClearOverride(m.env.stateDir); err != nil {
				log.Printf("nightlight: a restart can replay the ended toggle: %v", err)
			}
		})
	}
	if m.manualOverride {
		return
	}
	if m.heldThroughEase && target.Easing {
		return
	}
	m.heldThroughEase = false
	m.stepping = target.Easing
	m.show(target.Setting)
}

// show brings the filter to a schedule target. A refusal holds until the
// filter next clears: stepping would only be turned away again.
func (m *nightlightModule) show(target nightlight.Setting) {
	switch {
	case target.IsIdentity():
		if m.enabled {
			m.stopFilter()
		}
	case !m.enabled || (m.refused == nil && m.live != target):
		m.applyFilter(target)
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
// override, which also ends a running transition (seeding the phase when
// the schedule has not ticked yet, or at the first location when it has
// none), records the new state for restarts, and flips the filter.
func (m *nightlightModule) toggle() {
	cfg := m.ctx.Config.Nightlight
	if cfg.AutoSchedule {
		m.manualOverride, m.stepping = true, false
		if m.autoPhase == 0 && m.location != nil {
			m.autoPhase = nightlight.TargetAt(m.env.now(), *m.location, cfg.TransitionDuration.Transition(), m.night).Phase
		}
	}
	override, at := nightlight.Override{Phase: m.autoPhase, Enabled: !m.enabled}, m.env.now()
	m.onOwner(func() {
		if err := nightlight.SaveOverride(m.env.stateDir, override, at); err != nil {
			log.Printf("nightlight: the toggle will not survive a restart: %v", err)
		}
	})
	if m.enabled {
		m.stopFilter()
		return
	}
	m.startFilter()
}

// startFilter applies the configured full strength to every output, or
// shows why it is unusable.
func (m *nightlightModule) startFilter() {
	if m.nightErr != nil {
		m.enabled, m.refused = true, m.nightErr
		m.render()
		return
	}
	m.applyFilter(m.night)
}

// applyFilter puts a strength on every output. Only turning on logs and
// repaints the whole module: a transition step changes the label at most.
func (m *nightlightModule) applyFilter(setting nightlight.Setting) {
	wasOn := m.enabled
	m.enabled, m.refused, m.live = true, nil, setting
	if wasOn {
		m.renderLabel()
	} else {
		m.render()
	}
	m.onOwner(func() {
		if m.filter == nil {
			return
		}
		err := m.filter.Apply(setting)
		if err == nil {
			err = m.filter.Refused()
		}
		if err != nil {
			log.Printf("nightlight: %v", err)
			m.onLoop(func() {
				m.refused = err
				m.render()
			})
			return
		}
		if !wasOn {
			log.Printf("nightlight: on at %dK %d%% through %s", setting.Temp(), setting.Gamma(), m.filter.Protocol())
		}
	})
}

// stopFilter returns the outputs to their own colours.
func (m *nightlightModule) stopFilter() {
	m.onOwner(func() {
		if m.filter == nil {
			return
		}
		if err := m.filter.Clear(); err != nil {
			log.Printf("nightlight: %v", err)
		} else {
			log.Printf("nightlight: off")
		}
	})
	m.enabled, m.refused = false, m.nightErr
	m.render()
}

func (m *nightlightModule) Root() widget.Widget { return m.btn }
