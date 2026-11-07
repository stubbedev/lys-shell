package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/wlr"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/spawn"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/internal/widgetipc"
	"github.com/stubbedev/lys-shell/logging"
	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/shellipc"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/service/treeman"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/service/weather"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/layering"
	"github.com/stubbedev/lys-shell/shell/lockclient"
	"github.com/stubbedev/lys-shell/shell/osd"
	"github.com/stubbedev/lys-shell/shell/popups"
	"github.com/stubbedev/lys-shell/shell/powermenu"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
	wallpapershell "github.com/stubbedev/lys-shell/shell/wallpaper"
	"github.com/stubbedev/lys-shell/styling"
)

// Run hands the session's cursor to lys-login's display manager in
// the background, loads the user config and shows the bar. A config
// failure logs and falls back to defaults — the Rust shell's behavior —
// rather than refusing to start; a failed cursor record only logs, as
// the greeter then keeps its own cursor.
func Run(session *dbus.Conn) error {
	go func() {
		if err := recordCursor(context.Background()); err != nil {
			log.Printf("lys-shell: %v", err)
		}
	}()
	svc, err := config.Open()
	if err != nil {
		log.Printf("lys-shell: config: %v", err)
	}
	if svc == nil {
		return RunWith(config.Defaults(), session)
	}
	defer svc.Close()
	return run(svc.Config(), svc, session)
}

// followReducedMotion keeps the toolkit's reduced motion on while the
// desktop or the shell's config asks for it: the application follows
// only the desktop, and without a portal that is never. Under lys-wm the
// compositor's setting replaces the key's here.
func followReducedMotion(application *app.Application, cfg *config.Config, svc *config.Service) (stop func()) {
	apply := func(c *config.Config) {
		widget.SetReducedMotion(application.ReducedMotion() || c.Animations.Reduced())
	}
	apply(cfg)
	if svc == nil {
		return func() {}
	}
	offDesktop := application.OnReducedMotionChange(func(bool) { apply(svc.Config()) })
	offConfig := svc.Subscribe(func(_, next *config.Config) {
		application.Invoke(func() { apply(next) })
	})
	stopFollow, err := lyswm.FollowReducedMotion(svc.SetSessionReducedMotion, svc.ClearSessionReducedMotion)
	if err != nil {
		log.Printf("lys-shell: lys-wm reduced motion: %v", err)
		stopFollow = func() {}
	}
	return func() {
		stopFollow()
		offConfig()
		offDesktop()
	}
}

// RunWith shows the bar from a prepared config: one layer surface per
// output the layout gives a visible bar, then the lys-ui loop.
func RunWith(cfg *config.Config, session *dbus.Conn) error { return run(cfg, nil, session) }

// run shows the bar; with a service it follows config reloads.
func run(cfg *config.Config, svc *config.Service, session *dbus.Conn) error {
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return fmt.Errorf("bar: connect: %w", err)
	}
	defer sess.Close()

	var opts []app.Option
	if session != nil {
		opts = append(opts, app.WithSessionBus(session))
	}
	application := app.NewApplication(sess, opts...)
	// lys-ui's Run serves the tree on the accessibility bus while the
	// desktop asks for assistive technologies (LYS_UI_A11Y=atspi forces it,
	// as GTK_A11Y does for the Rust shell).
	// Surfaces animate their content through [animations] (shell/reveal),
	// as the Rust shell's revealers do; lys-ui's own surface fades would
	// stack on top of them.
	application.SetSurfaceMotion(false)
	outputs := sess.Outputs()
	if len(outputs) == 0 {
		return errors.New("bar: no output to draw on")
	}
	// Programmatic copies (the screenshot host) and the ctrl+c/v keys
	// share one clipboard.
	if application.Clipboard() == nil {
		application.SetClipboard(app.NewClipboard(sess))
	}
	s := newShellStart(application, sess, cfg, svc, session)
	defer s.down.run()
	s.releaseHold = application.Hold()
	gate := newStartGate(s.mount)
	wall, stopWallpaper := wallpapershell.Launch(application, outputs, cfg, session, sess, gate.done)
	defer stopWallpaper()
	s.wall = wall
	servicesUp := make(chan struct{})
	go func() {
		defer close(servicesUp)
		system, err := dbus.ConnectSystemBus()
		if err != nil {
			log.Printf("bar: system bus: %v", err)
		}
		s.startServices(system)
		application.Invoke(gate.done)
	}()
	err = application.Run()
	<-servicesUp
	if s.err != nil {
		return s.err
	}
	return err
}

const (
	configFlushTimeout = 5 * time.Second
	pollIdleAfter      = 5 * time.Minute
)

type startGate struct {
	pending int
	open    func()
}

func newStartGate(open func()) *startGate { return &startGate{pending: 2, open: open} }

func (g *startGate) done() {
	g.pending--
	if g.pending == 0 {
		g.open()
	}
}

type teardown []func()

func (t *teardown) add(fn func()) { *t = append(*t, fn) }

func (t *teardown) addAsync(start func() (stop func())) {
	stopped := make(chan func(), 1)
	go func() { stopped <- start() }()
	t.add(func() { (<-stopped)() })
}

func (t *teardown) run() {
	for _, fn := range slices.Backward(*t) {
		fn()
	}
}

type shellSession interface {
	Outputs() []*app.Output
	GlobalShortcutsManager() *wlr.GlobalShortcutsManagerV1
	wallpapershell.OutputWatcher
}

type shellStart struct {
	application *app.Application
	sess        shellSession
	cfg         *config.Config
	svc         *config.Service
	session     *dbus.Conn
	system      *dbus.Conn
	rebuild     func(apply func(*ModuleContext))
	// current is the live config snapshot the long-lived services read.
	current *atomic.Pointer[config.Config]
	// osdRef is the OSD once it exists; the recorder toasts through it.
	osdRef *atomic.Pointer[osd.Osd]
	ctx    ModuleContext
	// lateStarts are the clients that could not start without their
	// service; each starts when the service appears.
	lateStarts   map[string]lateStart
	backlights   *brightness.System
	inhibitState *idleinhibit.State
	weatherSvc   *weather.Service
	notifSvc     *notifications.Service
	customUpd    *customUpdates
	wall         *wallpapershell.Shell
	releaseHold  func()
	down         teardown
	err          error
}

func newShellStart(application *app.Application, sess shellSession, cfg *config.Config, svc *config.Service, session *dbus.Conn) *shellStart {
	s := &shellStart{
		application: application, sess: sess, cfg: cfg, svc: svc, session: session,
		current: &atomic.Pointer[config.Config]{},
		osdRef:  &atomic.Pointer[osd.Osd]{},
		ctx:     ModuleContext{Config: cfg, App: application},
	}
	s.current.Store(cfg)
	return s
}

func (s *shellStart) fail(err error) {
	s.err = err
	s.application.Quit()
}

// startServices starts the clients and exports, the system ones over
// system, which it closes with the shell; nil leaves them out.
func (s *shellStart) startServices(system *dbus.Conn) {
	if system != nil {
		s.system = system
		s.down.add(func() { _ = system.Close() })
		s.ctx.System = system
	}
	s.lateStarts = map[string]lateStart{}
	bwLife, stopBitwarden := context.WithCancel(context.Background())
	s.down.add(stopBitwarden)
	s.ctx.Bitwarden = newBitwardenHost(bwLife, s.application, s.current.Load, s.session)
	networkVault := s.ctx.Bitwarden.networkVault()
	var (
		starts   sync.WaitGroup
		hypr     *hyprland.Connection
		audio    *pulse.Service
		bt       *bluetooth.Service
		btErr    error
		netSvc   *network.Service
		stopNet  func()
		netErr   error
		media    *mpris.Service
		mediaErr error
		notifs   *notifications.Service
	)
	if detectCompositor() == compositorHyprland {
		starts.Go(func() {
			conn, err := hyprland.Connect()
			if err != nil {
				log.Printf("bar: Hyprland modules will not appear: %v", err)
				return
			}
			hypr = conn
		})
	}
	backlights := brightness.NewSystem(s.system, s.cfg.Brightness.EnableExternal)
	starts.Go(func() { audio = pulse.Start("") })
	starts.Go(func() { bt, btErr = startBluetooth(s.system) })
	starts.Go(func() { netSvc, stopNet, netErr = startNetworkService(s.system, networkVault) })
	starts.Go(func() { media, mediaErr = startMedia(s.session, s.cfg.Media) })
	starts.Go(func() { notifs = startNotifications(s.cfg.Notification) })
	starts.Wait()

	s.ctx.Hyprland = hypr
	if s.system != nil {
		s.ctx.Battery = upower.NewOn(s.system)
		s.ctx.Network = network.NewOn(s.system)
		s.ctx.PowerProfiles = powerprofiles.NewOn(s.system)
	}
	s.backlights = backlights
	s.down.add(func() { _ = backlights.Close() })
	s.ctx.Brightness = backlights
	s.down.add(func() { _ = audio.Close() })
	s.ctx.Pulse = audio
	if btErr == nil {
		s.down.add(func() { _ = bt.Close() })
		s.ctx.Bluetooth = bt
		// Every Rust bar warms all its dropdowns, so the bluetooth
		// dropdown's pairing watcher runs whether or not a bluetooth
		// module is placed: a request still reaches the user.
		startBtPairingNotifier(bt, s.session)
	} else {
		log.Printf("bluetooth: %v", btErr)
		s.lateStarts[bluetooth.BusName] = func() (func(*ModuleContext), func(), error) {
			bt, err := startBluetooth(s.system)
			if err != nil {
				return nil, nil, err
			}
			return func(c *ModuleContext) {
				c.Bluetooth = bt
				startBtPairingNotifier(bt, s.session)
			}, func() { _ = bt.Close() }, nil
		}
	}
	if netErr == nil {
		s.down.add(stopNet)
		s.ctx.Networking = netSvc
	} else {
		log.Printf("network: %v", netErr)
		s.lateStarts[network.BusName] = func() (func(*ModuleContext), func(), error) {
			svc, stop, err := startNetworkService(s.system, networkVault)
			if err != nil {
				return nil, nil, err
			}
			return func(c *ModuleContext) { c.Networking = svc }, stop, nil
		}
	}
	if mediaErr == nil {
		s.down.add(func() { _ = media.Close() })
		s.ctx.Media = media
	} else {
		log.Printf("media: %v", mediaErr)
	}
	// The idle-inhibit service owns its state here and serves it on the
	// session bus for the `lys-shell idle` CLI.
	s.inhibitState = idleinhibit.NewState(s.cfg.IdleInhibit.StartupDuration.Duration())
	s.ctx.IdleInhibit = s.inhibitState
	s.ctx.Treeman = treeman.New("treeman")
	// The weather service polls for the whole session (bootstrap/weather.rs);
	// after its first fetch it only polls while a module or dropdown
	// follows it.
	s.weatherSvc = weather.New(weatherSettings(s.cfg.Weather))
	s.down.add(s.weatherSvc.Close)
	s.ctx.Weather = s.weatherSvc
	// The notification service owns the well-known name on the session
	// bus; other senders deliver through it.
	s.notifSvc = notifs
	s.ctx.Notifications = s.notifSvc
	if svc := s.notifSvc; svc != nil {
		s.down.add(func() { svc.FlushWrites(configFlushTimeout) })
	}
	sniStore := sni.NewStore()
	s.ctx.SNI = sniStore
	if conn := s.session; conn != nil {
		if s.notifSvc != nil {
			server, err := notifications.Serve(conn, s.notifSvc)
			if err != nil {
				log.Printf("notifications: daemon: %v", err)
			} else {
				s.down.add(func() { _ = server.Release() })
			}
		}
		// The recorder asks the share picker on this bus for an output or
		// a region, records it on the GPU with audio from the audio service,
		// and reports through the OSD and desktop notifications.
		stateDir, ok := xdg.OS.StateDir(version.Name)
		if !ok {
			log.Print("recorder: no state directory: neither XDG_STATE_HOME nor HOME is an absolute path; a picked source is not remembered")
		}
		recState := recorder.NewState(recorder.NewGPUEngine(sharepicker.NewClient(conn), audio, logicalOutputSize(s.application, s.sess.Outputs), stateDir),
			func() config.RecorderConfig { return s.current.Load().Recorder },
			recorder.Hooks{Toast: func(label, icon string, ms uint32) {
				if o := s.osdRef.Load(); o != nil {
					o.ShowToast(widgetipc.ToastRequest{Label: &label, Icon: &icon, DurationMS: &ms})
				}
			}, Notify: desktopnotify.Notify})
		s.ctx.Recorder = recState
		if release, err := recorder.NewDaemon(recState).Export(conn); err == nil {
			s.down.add(release)
		} else {
			log.Printf("recorder: daemon: %v", err)
		}
		floor := func() float64 { return float64(s.current.Load().Brightness.MinBrightness) }
		focus := screenshot.CompositorFocus{Hyprland: s.ctx.Hyprland}
		if release, err := brightness.NewDaemon(backlights, focus.FocusedOutput, floor).Export(conn); err == nil {
			s.down.add(release)
		} else {
			log.Printf("brightness: daemon: %v", err)
		}
		if release, err := pulse.NewDaemon(audio).Export(conn); err == nil {
			s.down.add(release)
		} else {
			log.Printf("audio: daemon: %v", err)
		}
	}
	s.down.add(startMail(&s.ctx, s.session))
	sniHost, err := startTrayHost(s.session, sniStore)
	if err == nil {
		s.down.add(func() { _ = sniHost.Close() })
		s.ctx.Tray = NewTrayService(sniHost)
	} else {
		log.Printf("systray: host: %v", err)
	}
	s.down.add(serveCLIDaemons(s.session, s.ctx, sniHost))
	s.customUpd = newCustomUpdates()
	s.ctx.CustomUpdates = s.customUpd
	s.ctx.Attachers = &[]interface{ Attach(app.Host) }{}
	if conn := s.session; conn != nil {
		daemon := idleinhibit.NewDaemon(s.inhibitState)
		if release, err := daemon.Export(conn); err == nil {
			s.down.add(release)
		} else {
			log.Printf("idle-inhibit: daemon: %v", err)
		}
	}
	s.watchLateServices()
}

func (s *shellStart) watchLateServices() {
	if s.system == nil {
		log.Print("bar: no system bus; modules of services that start later will not appear")
		return
	}
	late := &lateServices{}
	s.down.add(late.stop)
	lateCtx, stopLate := context.WithCancel(context.Background())
	s.down.add(stopLate)
	if err := late.watch(lateCtx, s.system, s.lateStarts, s.application.Invoke, s.lateAppeared); err != nil {
		log.Printf("bar: %v; modules of services that start later will not appear", err)
	}
}

func (s *shellStart) lateAppeared(apply func(*ModuleContext)) {
	if s.rebuild == nil {
		if apply != nil {
			apply(&s.ctx)
		}
		return
	}
	s.rebuild(apply)
}

func (s *shellStart) mount() {
	application, sess, cfg, svc := s.application, s.sess, s.cfg, s.svc
	current, osdRef := s.current, s.osdRef
	// The bars load the stylesheet bundle; the surfaces it does not
	// reach (OSD, popups, dropdowns) paint from the same resolved
	// palette. The shell gives the user a starter stylesheet to edit.
	if dir, err := config.Dir(); err == nil {
		styling.EnsureUserStylesScaffold(dir)
	}
	theme := apptheme.New(cfg)
	theme.WatchUserStyles(application)
	s.down.add(apptheme.Setup(application))
	apptheme.ReportMissingIcons(cfg, config.StderrDiagnostics)
	s.down.add(followReducedMotion(application, cfg, svc))
	// rt is what the bars are built from; a config reload or a new
	// palette re-derives it and rebuilds them.
	rt := &barRuntime{theme: theme, palette: new(styling.Palette)}
	if err := rt.derive(cfg); err != nil {
		s.fail(err)
		return
	}
	font := rt.font
	baseCtx := s.ctx
	baseCtx.Font, baseCtx.Style, baseCtx.Theme = font, &rt.moduleStyle, theme
	polls := newPollHub()
	baseCtx.Polls = polls
	if application.IdleNotifyAvailable() {
		stopIdle, err := application.OnIdle(pollIdleAfter, func() { polls.setIdle(true) }, func() { polls.setIdle(false) })
		if err != nil {
			log.Printf("bar: polls run through idle: %v", err)
		} else {
			s.down.add(stopIdle)
		}
	}
	if svc != nil {
		writer := &configWriter{store: svc}
		s.down.add(func() { writer.flush(configFlushTimeout) })
		baseCtx.SetConfig = writer.set
	}
	baseCtx.Toast = func(label, icon string) {
		if o := osdRef.Load(); o != nil {
			o.ShowToast(widgetipc.ToastRequest{Label: &label, Icon: &icon})
		}
	}
	baseCtx.PowerMenu = powermenu.New(powermenu.Deps{
		Config: current.Load,
		Open: func(cfg app.LayerConfig) (powermenu.Window, error) {
			w, err := application.NewLayer(cfg)
			if err != nil {
				return nil, err
			}
			return w, nil
		},
		Run:   spawn.Quiet,
		Font:  font,
		Sheet: theme.Sheet,
	})
	// The clipboard history starts with the shell rather than when the
	// launcher first opens, so it covers the session; a compositor
	// without data-control simply has none (bootstrap/mod.rs).
	history, err := clipboard.DefaultHistory(cfg.Clipboard.HistoryMiB.Budget())
	if err != nil {
		s.fail(fmt.Errorf("bar: clipboard history: %w", err))
		return
	}
	if clip, err := clipboard.Start(application, history); err == nil {
		baseCtx.Clipboard = clip
		s.down.add(clip.Close)
	} else {
		history.Close()
		if !errors.Is(err, clipboard.ErrNoDataControl) {
			log.Printf("bar: clipboard history: %v", err)
		}
	}
	// One inhibitor, held on a bar surface while the user's toggle or an
	// app's portal hold asks for it.
	var holder *idleHolder
	if application.IdleInhibitAvailable() {
		holder = &idleHolder{state: s.inhibitState, inhibit: application.InhibitIdle}
		changes, stop := s.inhibitState.Changes()
		s.down.add(stop)
		go func() {
			for range changes {
				application.Invoke(holder.sync)
			}
		}()
	}
	osdSrv := osd.New(osd.Deps{
		Open: func(lc app.LayerConfig) (osd.Window, error) {
			w, err := application.NewLayer(lc)
			if err != nil {
				return nil, err
			}
			return w, nil
		},
		Outputs: sess.Outputs,
		Invoke:  application.Invoke,
		After:   func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
		Sheet:   theme.Sheet,
		Font:    font,
	}, cfg)
	osdRef.Store(osdSrv)
	captureSvc := newCaptureServices(application, sess.Outputs, current.Load, baseCtx.Hyprland, baseCtx.Clipboard, font, theme.Sheet)
	s.down.add(captureSvc.close)
	s.down.addAsync(func() func() { return captureSvc.serve(s.session) })
	focused := screenshot.CompositorFocus{Hyprland: baseCtx.Hyprland}.FocusedOutput
	s.down.addAsync(func() func() {
		return servePortalHosts(s.session, application, sess.Outputs, current.Load, focused, font, theme.Sheet)
	})
	baseCtx.Screenshot = captureSvc.trigger
	baseCtx.PickColor = func() {
		captureSvc.pickColor(func(hex string) {
			application.Invoke(func() {
				c := application.Clipboard()
				if c == nil {
					log.Printf("launcher: picked %s, but there is no clipboard to copy it to", hex)
					return
				}
				if err := c.WriteText(hex); err != nil {
					log.Printf("launcher: copying the picked color: %v", err)
					return
				}
				baseCtx.Toast(i18n.T("launcher-control-color-copied", i18n.Str("color", hex)), "ld-palette-symbolic")
			})
		})
	}
	if application.ToplevelsAvailable() {
		tasks := newTaskHub(appToplevels{application}, application.Invoke)
		s.down.add(tasks.close)
		baseCtx.Tasks = tasks
	}
	rt.ctx = baseCtx
	rt.mount(application, cfg)
	// Notification popups render on one monitor, bar or not.
	var popupHost *popups.Popups
	if s.notifSvc != nil {
		popupHost = popups.New(application, s.notifSvc, cfg, font, theme.Sheet, sess.Outputs)
		go popupHost.Run()
	}
	host := &barHost{app: application, rt: rt, holder: holder}
	bars := newBarSet(host.open)
	// A bar that cannot open is logged, not fatal.
	s.down.add(bars.followOutputs(sess, func(connector string) (config.BarLayout, bool) {
		return barLayoutFor(current.Load(), connector)
	}, func(output *app.Output) {
		osdSrv.DetachOutput(output.Name)
		if popupHost != nil {
			popupHost.DetachOutput(output.Name)
		}
		rt.ctx.Dropdowns.detachHost(output.Name)
	}))
	// restyle re-derives the palette, styles, and font from cfg and
	// rebuilds the bars from them (the Rust modules re-render from their
	// property watches, and the CSS watcher recompiles the bundle).
	restyle := func(cfg *config.Config) {
		if err := rt.derive(cfg); err != nil {
			log.Printf("%v; keeping the previous font", err)
		}
		rt.mount(application, cfg)
		bars.reload(func(connector string) (config.BarLayout, bool) { return barLayoutFor(cfg, connector) })
	}
	// A service that starts after the shell (or restarts) brings its
	// modules back: its client starts if it could not before, and the
	// bars rebuild around it.
	s.rebuild = func(apply func(*ModuleContext)) {
		if apply != nil {
			apply(&rt.ctx)
			rt.ctx.Dropdowns.release()
		}
		restyle(current.Load())
	}
	if svc != nil {
		// A reload recompiles the stylesheet for the new snapshot,
		// rebuilds the bars, and hands the OSD and popups their new
		// sections. Snapshots published faster than the loop applies
		// them apply once, as the latest.
		reloads := newReloadQueue(application.Invoke, func(old, next *config.Config) {
			current.Store(next)
			theme.SetConfig(next)
			if barsAffected(old, next) {
				restyle(next)
			} else {
				// The open bars stay; dropdowns opened from now on
				// read the new snapshot.
				rt.ctx.Config = next
				rt.ctx.Dropdowns.setConfig(next)
			}
			osdSrv.SetConfig(next)
			s.backlights.SetExternal(next.Brightness.EnableExternal)
			applyNotificationConfig(s.notifSvc, next.Notification)
			s.weatherSvc.Configure(weatherSettings(next.Weather))
			if s.wall != nil {
				s.wall.SetConfig(next)
			}
			if popupHost != nil {
				popupHost.SetConfig(next)
			}
			if baseCtx.Clipboard != nil {
				if err := baseCtx.Clipboard.SetBudget(next.Clipboard.HistoryMiB.Budget()); err != nil {
					log.Printf("bar: clipboard history budget: %v", err)
				}
			}
		})
		s.down.add(svc.Subscribe(func(old, next *config.Config) {
			apptheme.ReportMissingIcons(next, config.StderrDiagnostics)
			theme.Precompile(next)
			reloads.push(old, next)
		}))
		// A secrets reload re-resolves the weather API keys
		// (spawn_secrets_reload_watcher).
		s.down.add(svc.SubscribeSecrets(func() { s.weatherSvc.Configure(weatherSettings(current.Load().Weather)) }))
	}
	// A fresh color extraction re-resolves the provider palette and
	// recompiles the bundle, the Rust shell's theme hot-apply.
	if s.wall != nil {
		ticks, stopTicks := s.wall.Service().Extracted()
		s.down.add(stopTicks)
		go func() {
			for range ticks {
				compiled := apptheme.Compile(current.Load())
				application.Invoke(func() {
					// The bars rebuild only for a new palette, so an
					// extraction that changed nothing (a monitor plugged in)
					// leaves them open.
					theme.Apply(compiled)
					if rt.paletteStale() {
						restyle(current.Load())
					}
				})
			}
		}()
	}
	// The level and layout watchers run whether or not the OSD is enabled:
	// the OSD drops their events while disabled, so a reload can turn it on
	// or off (the Rust OsdEnabledChanged). The lock-key source, which opens
	// input devices, follows the setting instead.
	osdCtx, stopOsd := context.WithCancel(context.Background())
	s.down.add(stopOsd)
	go func() {
		var layouts osd.Layouts
		if source, err := keyboardLayoutSource(baseCtx); err == nil {
			defer source.close()
			layouts = source
		} else {
			log.Printf("osd: keyboard layout: %v", err)
		}
		osdSrv.Watch(osdCtx, baseCtx.Pulse, baseCtx.Brightness, layouts, lockKeySource())
	}()
	// The lock triggers (logind, lock-on-start, and `lys-shell lock`
	// through Shell1) start `lys-login lock`, the login screen in its
	// lock mode, as a process of its own.
	locker, stopLock := lockclient.Start(s.system, current.Load, func() {
		polls.setLocked(true)
		if current.Load().Bitwarden.LockWithSession {
			baseCtx.Bitwarden.lockVault()
		}
	}, func() { polls.setLocked(false) })
	s.down.add(stopLock)
	visibility := shellipc.NewState(func(hidden map[string]bool) {
		application.Invoke(func() { bars.apply(hidden) })
	})
	bars.publishTo(visibility.SetConnectors)
	s.down.addAsync(func() func() {
		return serveShellIPC(s.session, application, visibility, func() bool {
			locker.Lock()
			return true
		}, func(monitor string) error {
			return toggleDashboardOnLoop(application, rt, monitor, focused)
		})
	})
	// The launcher surface serves `lys-shell launcher` sessions.
	stopLauncher, openLauncher := startLauncher(application, sess.Outputs, current.Load, func() ModuleContext { return rt.ctx }, theme, font)
	s.down.add(stopLauncher)
	if sess.GlobalShortcutsManager() != nil {
		s.down.add(registerShellShortcuts(application.RegisterGlobalShortcut, shellShortcuts(openLauncher, func() {
			go func() {
				if err := toggleDashboardOnLoop(application, rt, "", focused); err != nil {
					log.Printf("dashboard: %v", err)
				}
			}()
		}, visibility)))
	}
	// The widget socket carries out-of-process pushes (`lys-shell toast`).
	widgetSrv := widgetipc.NewServer()
	if stop, err := widgetSrv.Listen(); err == nil {
		s.down.add(stop)
	} else {
		log.Printf("widget socket: %v", err)
	}
	go func() {
		for req := range widgetSrv.Toasts() {
			osdSrv.ShowToast(req)
		}
	}()
	go func() {
		for update := range widgetSrv.Updates() {
			s.customUpd.dispatch(update.ID, update.Output)
		}
	}()
	s.releaseHold()
}

// barConfigFor shows one output's bar on lys-ui's bar window: anchored
// to the configured edge and stretched along it, as thick as the bar's
// content, reserving that thickness when the bar is exclusive. The
// window keeps no layer margins: the bar's CSS margins (the insets and
// the shadow margin) sit inside it, as GTK sizes a layer window by its
// margin box.
func barConfigFor(ctx ModuleContext, layout config.BarLayout, output *app.Output) app.BarConfig {
	return app.BarConfig{
		Output:    output,
		Bar:       buildRoot(ctx, layout, output.Name),
		Layer:     layering.For(ctx.Config.General, ctx.Config.Bar.Layer),
		Exclusive: ctx.Config.Bar.Exclusive,
		Namespace: "lys-shell-bar-" + output.Name,
	}
}

// buildRoot assembles one bar on lys-ui's Bar, so the bar CSS styles
// it:
//
//	bar.<edge>.<connector>[.floating]   (the --bar-* variables inline)
//	├ box.start
//	├ box.center
//	└ box.end
//
// The bar's CSS margins carry the insets and the shadow margin, its
// border, background and padding the bar chrome; each zone spaces its
// items by the module gap.
func buildRoot(ctx ModuleContext, layout config.BarLayout, connector string) *widget.Bar {
	bar := widget.NewBar(barEdge(ctx.Config.Bar.Location))
	if connector != "" {
		bar.AddClass(connector)
	}
	if ctx.Theme != nil {
		ctx.Theme.Attach(bar)
	}
	fillBar(bar, ctx, layout)
	return bar
}

// fillBar gives bar the config-derived parts of buildRoot: its
// floating class, the --bar-* variables, and the zones' modules, which
// replace the ones it held. The edge is the window's to move.
func fillBar(bar *widget.Bar, ctx ModuleContext, layout config.BarLayout) {
	cfg := ctx.Config
	bar.SetClass("floating", floating(cfg))
	bar.SetInlineStyle(inlineDecls(styling.BarCSS(cfg.Bar, cfg.Styling.ColorExtractor.ThemeProvider)))
	for _, z := range []struct {
		box   *widget.Box
		items []config.BarItem
	}{{bar.Start(), layout.Left}, {bar.Center(), layout.Center}, {bar.End(), layout.Right}} {
		z.box.Clear()
		appendItems(z.box, z.items, ctx)
	}
}

// barEdge is the lys-ui edge a bar at location runs along.
func barEdge(location config.Location) widget.BarEdge {
	switch location {
	case config.LocationTop:
		return widget.BarTop
	case config.LocationBottom:
		return widget.BarBottom
	case config.LocationLeft:
		return widget.BarLeft
	case config.LocationRight:
		return widget.BarRight
	}
	panic(fmt.Sprintf("bar: location %q has no edge", location))
}

// logicalSize converts an output's current mode to logical pixels at
// the output's scale; the compositor's fractional-scale preference
// overrides this live once the surface exists. Bar scale is a UI scale
// for sizes, not a device scale, and never divides here.
func logicalSize(output *app.Output) widget.Size {
	scale := max(output.Scale, 1)
	return widget.Size{W: output.ModeW / scale, H: output.ModeH / scale}
}

// barRuntime is the state a mount generation of bars is built from:
// the module context (with its dropdown registry), the theme, and what
// derive resolves from a config snapshot.
type barRuntime struct {
	ctx   ModuleContext
	theme *apptheme.Theme
	// palette is the one resolved palette every Go-painted surface
	// (OSD, popups, lock, capture) holds; derive updates it in place,
	// on the loop.
	palette *styling.Palette
	// style is the dropdowns'; moduleStyle is the modules', which
	// leave their normal ink to the stylesheet (--bar-btn-label-color):
	// a zero fg is "unset" to lys-ui, so only a module's deliberate state
	// color stays programmatic.
	style, moduleStyle barStyle
	font               render.Font
}

// derive resolves the palette, styles, and font for cfg from the
// theme's compiled palette. A font that does not load is an error and
// leaves the previous font in place.
func (r *barRuntime) derive(cfg *config.Config) error {
	*r.palette = *r.theme.RenderPalette()
	r.style = computeStyle(cfg, r.palette)
	r.moduleStyle = r.style.forModules()
	face, err := app.Font(cfg.General.FontSans, r.style.labelPx)
	if err != nil {
		return fmt.Errorf("bar: font %q: %w", cfg.General.FontSans, err)
	}
	r.font = app.FontFallback(face)
	return nil
}

// paletteStale reports whether the theme's compiled palette differs
// from the one derive last resolved.
func (r *barRuntime) paletteStale() bool { return *r.theme.RenderPalette() != *r.palette }

// mount starts a generation for cfg: the previous one is retired and the
// context carries the new snapshot. The dropdown registry made by the
// first mount is remounted by every later one.
func (r *barRuntime) mount(application *app.Application, cfg *config.Config) {
	if r.ctx.gen != nil {
		r.ctx.gen.retire()
	}
	r.ctx.Config = cfg
	r.ctx.Font = r.font
	r.ctx.Style = &r.moduleStyle
	r.ctx.gen = newMountGen()
	if r.ctx.Dropdowns != nil {
		r.ctx.Dropdowns.remount(r.ctx)
		return
	}
	r.ctx.Dropdowns = newDropdownRegistry(application, cfg, r.font, &r.style, r.ctx)
	r.ctx.Dropdowns.buildAhead()
}

// barLayoutFor is the output's layout and whether it shows a bar.
func barLayoutFor(cfg *config.Config, connector string) (config.BarLayout, bool) {
	layout, ok := FindLayout(cfg.Bar.Layout, connector)
	return layout, ok && layout.Show
}

// barHost opens the bars of the shell's mount generation; `lys-shell
// panel show` and a reload open bars through it.
type barHost struct {
	app    *app.Application
	rt     *barRuntime
	holder *idleHolder
}

// context is the module context of output's bar: each bar is its own
// generation, so closing or rebuilding it (panel hide, a reload) ends
// its modules.
func (h *barHost) context(output *app.Output) ModuleContext {
	ctx := h.rt.ctx
	ctx.Connector = output.Name
	ctx.Attachers = &[]interface{ Attach(app.Host) }{}
	ctx.gen = newMountGen()
	return ctx
}

// open shows one output's bar.
func (h *barHost) open(output *app.Output, layout config.BarLayout) (barLayer, error) {
	ctx := h.context(output)
	bc := barConfigFor(ctx, layout, output)
	window, err := h.app.NewBar(bc)
	if err != nil {
		ctx.gen.retire()
		return nil, err
	}
	window.SetBackgroundBlur(ctx.Config.Bar.BackgroundOpacity < 100)
	w := &barWindow{host: h, output: output, layer: window, bar: bc.Bar, gen: ctx.gen, layerKind: bc.Layer}
	w.attach(ctx)
	if h.holder != nil {
		h.holder.add(window)
	}
	return w, nil
}

// barWindow is an open bar: closing it closes the layer and ends the
// generation its modules run in.
type barWindow struct {
	host      *barHost
	output    *app.Output
	layer     *app.BarWindow
	bar       *widget.Bar
	gen       *mountGen
	layerKind app.Layer
}

// attach hands the window to what ctx's modules and the dropdowns
// need it for.
func (w *barWindow) attach(ctx ModuleContext) {
	for _, a := range *ctx.Attachers {
		a.Attach(w.layer)
	}
	w.host.rt.ctx.Dropdowns.attachHost(w.output.Name, w.layer, logicalSize(w.output).H)
}

// Rebuild refills the bar from the current mount generation on its own
// surface: its modules restart, the stack layer, the edge and the
// exclusive zone move in place, and the surface refits to the new
// content on the next frame. A refused change leaves the old modules
// running.
func (w *barWindow) Rebuild(layout config.BarLayout) error {
	cfg := w.host.rt.ctx.Config
	if kind := layering.For(cfg.General, cfg.Bar.Layer); kind != w.layerKind {
		if err := w.layer.SetLayer(kind); err != nil {
			return fmt.Errorf("move to layer %v: %w", kind, err)
		}
		w.layerKind = kind
	}
	if edge := barEdge(cfg.Bar.Location); edge != w.bar.Edge() {
		if err := w.layer.SetEdge(edge); err != nil {
			return fmt.Errorf("move to %s: %w", cfg.Bar.Location, err)
		}
	}
	w.layer.SetBackgroundBlur(cfg.Bar.BackgroundOpacity < 100)
	if err := w.layer.SetExclusive(cfg.Bar.Exclusive); err != nil {
		return fmt.Errorf("set exclusive %v: %w", cfg.Bar.Exclusive, err)
	}
	ctx := w.host.context(w.output)
	w.gen.retire()
	w.gen = ctx.gen
	fillBar(w.bar, ctx, layout)
	w.attach(ctx)
	return nil
}

func (w *barWindow) Close() {
	if w.host.holder != nil {
		w.host.holder.remove(w.layer)
	}
	w.layer.Close()
	w.gen.retire()
}

func startBluetooth(system *dbus.Conn) (*bluetooth.Service, error) {
	if system == nil {
		return nil, errors.New("no system bus")
	}
	return bluetooth.New(system)
}

func startMedia(session *dbus.Conn, cfg config.MediaConfig) (*mpris.Service, error) {
	if session == nil {
		return nil, errors.New("no session bus")
	}
	return mpris.New(session, cfg.PlayersIgnored, cfg.PlayerPriority)
}

func startTrayHost(session *dbus.Conn, store *sni.Store) (*sni.Host, error) {
	if session == nil {
		return nil, errors.New("no session bus")
	}
	return sni.NewHostOn(session, store)
}
