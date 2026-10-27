package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/logging"
	"github.com/stubbedev/lys-shell/internal/spawn"
	"github.com/stubbedev/lys-shell/internal/widgetipc"
	"github.com/stubbedev/lys-shell/internal/xdg"
	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/service/treeman"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/service/weather"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/layering"
	"github.com/stubbedev/lys-shell/shell/lock"
	"github.com/stubbedev/lys-shell/shell/osd"
	"github.com/stubbedev/lys-shell/shell/popups"
	"github.com/stubbedev/lys-shell/shell/powermenu"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
	wallpapershell "github.com/stubbedev/lys-shell/shell/wallpaper"
	"github.com/stubbedev/lys-shell/styling"
)

// Run loads the user config and shows the bar. A config failure logs
// and falls back to defaults — the Rust shell's behavior — rather than
// refusing to start.
func Run() error {
	svc, err := config.Open()
	if err != nil {
		log.Printf("lys-shell: config: %v", err)
	}
	if svc == nil {
		return RunWith(config.Defaults())
	}
	defer svc.Close()
	return run(svc.Config(), svc)
}

// RunWith shows the bar from a prepared config: one layer surface per
// output the layout gives a visible bar, then the lys-ui loop.
func RunWith(cfg *config.Config) error { return run(cfg, nil) }

// run shows the bar; with a service it follows config reloads.
func run(cfg *config.Config, svc *config.Service) error {
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return fmt.Errorf("bar: connect: %w", err)
	}
	defer sess.Close()

	application := app.NewApplication(sess)
	// lys-ui's Run serves the tree on the accessibility bus while the
	// desktop asks for assistive technologies (LYS_UI_A11Y=atspi forces it,
	// as GTK_A11Y does for the Rust shell).
	// Surfaces animate their content through [animations] (shell/reveal),
	// as the Rust shell's revealers do; lys-ui's own surface fades would
	// stack on top of them.
	application.SetSurfaceMotion(false)
	// current is the live config snapshot the long-lived services read.
	current := &atomic.Pointer[config.Config]{}
	current.Store(cfg)
	// osdRef is the OSD once it exists; the recorder toasts through it.
	osdRef := &atomic.Pointer[osd.Osd]{}
	// Programmatic copies (the screenshot host) and the ctrl+c/v keys
	// share one clipboard.
	if application.Clipboard() == nil {
		application.SetClipboard(app.NewClipboard(sess))
	}
	// The bars load the stylesheet bundle; the surfaces it does not
	// reach (OSD, popups, dropdowns) paint from the same resolved
	// palette. The shell gives the user a starter stylesheet to edit.
	if dir, err := config.Dir(); err == nil {
		styling.EnsureUserStylesScaffold(dir)
	}
	theme := apptheme.New(cfg)
	theme.WatchUserStyles(application)
	defer apptheme.Setup(application)()
	apptheme.ReportMissingIcons(cfg, config.StderrDiagnostics)
	// rt is what the bars are built from; a config reload or a new
	// palette re-derives it and rebuilds them.
	rt := &barRuntime{theme: theme, palette: new(styling.Palette)}
	if err := rt.derive(cfg); err != nil {
		return err
	}
	font := rt.font
	baseCtx := ModuleContext{Config: cfg, App: application, Font: font, Style: &rt.moduleStyle, Theme: theme}
	if svc != nil {
		baseCtx.SetConfig = configSetter(svc)
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
	if clip, err := clipboard.Start(application, clipboard.DefaultHistory()); err == nil {
		baseCtx.Clipboard = clip
	}
	if detectCompositor() == compositorHyprland {
		if conn, err := hyprland.Connect(); err != nil {
			log.Printf("bar: Hyprland modules will not appear: %v", err)
		} else {
			baseCtx.Hyprland = conn
		}
	}
	if battery, err := upower.NewSystem(); err == nil {
		defer func() { _ = battery.Close() }()
		baseCtx.Battery = battery
	}
	backlights := brightness.NewSystem(cfg.Brightness.EnableExternal)
	defer func() { _ = backlights.Close() }()
	baseCtx.Brightness = backlights
	audio := connectAudio()
	if audio != nil {
		defer func() { _ = audio.Close() }()
		baseCtx.Pulse = audio
	}
	// lateStarts are the clients that could not start without their
	// service; each starts when the service appears.
	lateStarts := map[string]lateStart{}
	if bt, err := bluetooth.NewSystem(); err == nil {
		defer func() { _ = bt.Close() }()
		baseCtx.Bluetooth = bt
		// Every Rust bar warms all its dropdowns, so the bluetooth
		// dropdown's pairing watcher runs whether or not a bluetooth
		// module is placed: a request still reaches the user.
		startBtPairingNotifier(bt)
	} else {
		log.Printf("bluetooth: %v", err)
		lateStarts[bluetooth.BusName] = func() (func(*ModuleContext), func(), error) {
			bt, err := bluetooth.NewSystem()
			if err != nil {
				return nil, nil, err
			}
			return func(c *ModuleContext) {
				c.Bluetooth = bt
				startBtPairingNotifier(bt)
			}, func() { _ = bt.Close() }, nil
		}
	}
	if nm, err := network.NewSystem(); err == nil {
		defer func() { _ = nm.Close() }()
		baseCtx.Network = nm
	}
	if svc, stop, err := startNetworkService(); err == nil {
		defer stop()
		baseCtx.Networking = svc
	} else {
		log.Printf("network: %v", err)
		lateStarts[network.BusName] = func() (func(*ModuleContext), func(), error) {
			svc, stop, err := startNetworkService()
			if err != nil {
				return nil, nil, err
			}
			return func(c *ModuleContext) { c.Networking = svc }, stop, nil
		}
	}
	if pp, err := powerprofiles.NewSystem(); err == nil {
		defer func() { _ = pp.Close() }()
		baseCtx.PowerProfiles = pp
	}
	if media, err := mpris.NewSession(cfg.Media.PlayersIgnored, cfg.Media.PlayerPriority); err == nil {
		defer func() { _ = media.Close() }()
		baseCtx.Media = media
	}
	// The idle-inhibit service owns its state here and serves it on the
	// session bus for the `lys-shell idle` CLI.
	inhibitState := idleinhibit.NewState(cfg.IdleInhibit.StartupDuration.Duration())
	baseCtx.IdleInhibit = inhibitState
	// One inhibitor, held on a bar surface while the user's toggle or an
	// app's portal hold asks for it.
	var holder *idleHolder
	if application.IdleInhibitAvailable() {
		holder = &idleHolder{state: inhibitState, inhibit: application.InhibitIdle}
		changes, stop := inhibitState.Changes()
		defer stop()
		go func() {
			for range changes {
				application.Invoke(holder.sync)
			}
		}()
	}
	baseCtx.Treeman = treeman.New("treeman")
	// The weather service polls for the whole session (bootstrap/weather.rs);
	// after its first fetch it only polls while a module or dropdown
	// follows it.
	weatherSvc := weather.New(weatherSettings(cfg.Weather))
	defer weatherSvc.Close()
	baseCtx.Weather = weatherSvc
	// The notification service owns the well-known name on the session
	// bus; other senders deliver through it.
	notifSvc := startNotifications(cfg.Notification)
	baseCtx.Notifications = notifSvc
	sniStore := sni.NewStore()
	baseCtx.SNI = sniStore
	if conn, err := dbus.ConnectSessionBus(); err == nil {
		defer func() { _ = conn.Close() }()
		if notifSvc != nil {
			server, err := notifications.Serve(conn, notifSvc)
			if err != nil {
				log.Printf("notifications: daemon: %v", err)
			} else {
				defer func() { _ = server.Release() }()
			}
		}
		// The recorder asks the share picker on this bus for an output or
		// a region, records it on the GPU with audio from the audio service,
		// and reports through the OSD and desktop notifications.
		stateDir, _ := xdg.StateDir()
		recState := recorder.NewState(recorder.NewGPUEngine(sharepicker.NewClient(conn), audio, logicalOutputSize(application, sess.Outputs), stateDir),
			func() config.RecorderConfig { return current.Load().Recorder },
			recorder.Hooks{Toast: func(label, icon string, ms uint32) {
				if o := osdRef.Load(); o != nil {
					o.ShowToast(widgetipc.ToastRequest{Label: &label, Icon: &icon, DurationMS: &ms})
				}
			}, Notify: desktopnotify.Notify})
		baseCtx.Recorder = recState
		if release, err := recorder.NewDaemon(recState).Export(conn); err == nil {
			defer release()
		} else {
			log.Printf("recorder: daemon: %v", err)
		}
		if audio != nil {
			if release, err := pulse.NewDaemon(audio).Export(conn); err == nil {
				defer release()
			} else {
				log.Printf("audio: daemon: %v", err)
			}
		}
	}
	stopMail := startMail(&baseCtx)
	defer stopMail()
	bwLife, stopBitwarden := context.WithCancel(context.Background())
	defer stopBitwarden()
	baseCtx.Bitwarden = newBitwardenHost(bwLife, application, current.Load)
	sniHost, err := sni.NewHost(sniStore)
	if err == nil {
		defer func() { _ = sniHost.Close() }()
		baseCtx.Tray = NewTrayService(sniHost)
	} else {
		log.Printf("systray: host: %v", err)
	}
	defer serveCLIDaemons(baseCtx, sniHost)()
	customUpd := newCustomUpdates()
	baseCtx.CustomUpdates = customUpd
	baseCtx.Attachers = &[]interface{ Attach(app.Host) }{}
	if conn, err := dbus.ConnectSessionBus(); err == nil {
		defer func() { _ = conn.Close() }()
		daemon := idleinhibit.NewDaemon(inhibitState)
		if release, err := daemon.Export(conn); err == nil {
			defer release()
		} else {
			log.Printf("idle-inhibit: daemon: %v", err)
		}
	}

	outputs := sess.Outputs()
	if len(outputs) == 0 {
		return errors.New("bar: no output to draw on")
	}
	// Wallpapers render on their own Background layers; hotplugged
	// outputs join once their connector name is known.
	wall, stopWallpaper := wallpapershell.Launch(application, outputs, cfg, sess)
	defer stopWallpaper()
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
	captureSvc := startCapture(application, sess.Outputs, current.Load, baseCtx.Hyprland, font, theme.Sheet)
	servePortalHosts(application, sess.Outputs, current.Load, screenshot.CompositorFocus{Hyprland: baseCtx.Hyprland}.FocusedOutput, font, theme.Sheet)
	defer captureSvc.close()
	baseCtx.Screenshot = captureSvc.trigger
	rt.ctx = baseCtx
	rt.mount(application, cfg)
	// Notification popups render on one monitor, bar or not.
	var popupHost *popups.Popups
	if notifSvc != nil {
		popupHost = popups.New(application, notifSvc, cfg, font, theme.Sheet, sess.Outputs)
		go popupHost.Run()
	}
	host := &barHost{app: application, rt: rt, holder: holder}
	bars := newBarSet(host.open)
	// plugOutput gives a named output its bar; startup
	// outputs and hotplugged ones (once their connector name is known)
	// take the same path, the Rust SyncMonitors. A bar that cannot open
	// is logged, not fatal.
	plugOutput := func(output *app.Output) {
		if output.Name == "" {
			return
		}
		layout, show := barLayoutFor(current.Load(), output.Name)
		bars.plug(output, layout, show)
	}
	for _, output := range outputs {
		plugOutput(output)
	}
	defer sess.WatchOutputIdentity(plugOutput)()
	defer sess.WatchOutputs(nil, func(output *app.Output) {
		bars.unplug(output)
		osdSrv.DetachOutput(output.Name)
		if popupHost != nil {
			popupHost.DetachOutput(output.Name)
		}
		rt.ctx.Dropdowns.detachHost(output.Name)
	})()
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
	if sysConn, err := dbus.ConnectSystemBus(); err != nil {
		log.Printf("bar: system bus: %v; modules of services that start later will not appear", err)
	} else {
		defer func() { _ = sysConn.Close() }()
		late := &lateServices{}
		defer late.stop()
		lateCtx, stopLate := context.WithCancel(context.Background())
		defer stopLate()
		if err := late.watch(lateCtx, sysConn, lateStarts, application.Invoke, func(apply func(*ModuleContext)) {
			if apply != nil {
				apply(&rt.ctx)
			}
			restyle(current.Load())
		}); err != nil {
			log.Printf("bar: %v; modules of services that start later will not appear", err)
		}
	}
	// lockScreen starts below; a reload reaches it once it exists.
	var lockScreen *lock.Screen
	if svc != nil {
		// A reload recompiles the stylesheet for the new snapshot,
		// rebuilds the bars, and hands the OSD and popups their new
		// sections. Snapshots published faster than the loop applies
		// them apply once, as the latest.
		reloads := newReloadQueue(application.Invoke, func(old, next *config.Config) {
			current.Store(next)
			apptheme.ReportMissingIcons(next, config.StderrDiagnostics)
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
			applyNotificationConfig(notifSvc, next.Notification)
			weatherSvc.Configure(weatherSettings(next.Weather))
			if wall != nil {
				wall.SetConfig(next)
			}
			if popupHost != nil {
				popupHost.SetConfig(next)
			}
			if lockScreen != nil {
				lockScreen.SetConfig(next)
			}
		})
		defer svc.Subscribe(reloads.push)()
		// A secrets reload re-resolves the weather API keys
		// (spawn_secrets_reload_watcher).
		defer svc.SubscribeSecrets(func() { weatherSvc.Configure(weatherSettings(current.Load().Weather)) })()
	}
	// A fresh color extraction re-resolves the provider palette and
	// recompiles the bundle, the Rust shell's theme hot-apply.
	if wall != nil {
		ticks, stopTicks := wall.Service().Extracted()
		defer stopTicks()
		go func() {
			for range ticks {
				application.Invoke(func() {
					// The bundle recompiles regardless (the Rust CSS
					// watcher); the bars rebuild only for a new palette, so
					// an extraction that changed nothing (a monitor plugged
					// in) leaves them open.
					theme.Reload()
					if rt.paletteStale() {
						restyle(current.Load())
					}
				})
			}
		}()
	}
	// The OSD watcher runs whether or not the OSD is enabled: the OSD
	// drops events while disabled, so a reload can turn it on or off
	// (the Rust OsdEnabledChanged).
	osdCtx, stopOsd := context.WithCancel(context.Background())
	defer stopOsd()
	go func() {
		var layouts osd.Layouts
		if source, err := keyboardLayoutSource(baseCtx); err == nil {
			defer source.close()
			layouts = source
		} else {
			log.Printf("osd: keyboard layout: %v", err)
		}
		osdSrv.Watch(osdCtx, baseCtx.Pulse, baseCtx.Brightness, layouts)
	}()
	// The ext-session-lock screen and its triggers (logind, and `lys-shell
	// lock` through Shell1).
	lockScreen, stopLock := lock.Start(application, sess.Outputs, current.Load(), font, theme.Sheet, func() {
		if current.Load().Bitwarden.LockWithSession {
			baseCtx.Bitwarden.lockIfOpen()
		}
	})
	defer stopLock()
	defer serveShellIPC(application, bars, func() bool {
		application.Invoke(lockScreen.Lock)
		return true
	})()
	// The launcher surface serves `lys-shell launcher` sessions.
	defer startLauncher(application, sess.Outputs, current.Load, rt.ctx, theme, font)()
	// The widget socket carries out-of-process pushes (`lys-shell toast`).
	widgetSrv := widgetipc.NewServer()
	if stop, err := widgetSrv.Listen(); err == nil {
		defer stop()
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
			customUpd.dispatch(update.ID, update.Output)
		}
	}()
	return application.Run()
}

// connectAudio starts the audio service (the Rust bootstrap's
// AudioService with its daemon). A missing sound server leaves the
// shell without audio, logged, rather than failing it.
func connectAudio() *pulse.Service {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	audio, err := pulse.Connect(ctx, "")
	if err != nil {
		log.Printf("audio: %v", err)
		return nil
	}
	return audio
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

// mount starts a generation for cfg: the previous one is retired, the
// dropdowns are rebuilt, and the context carries the new snapshot.
func (r *barRuntime) mount(application *app.Application, cfg *config.Config) {
	if r.ctx.gen != nil {
		r.ctx.gen.retire()
	}
	r.ctx.Config = cfg
	r.ctx.Font = r.font
	r.ctx.Style = &r.moduleStyle
	r.ctx.gen = newMountGen()
	if r.ctx.Dropdowns != nil {
		// The old bars' dropdown instances go with them.
		r.ctx.Dropdowns.release()
	}
	r.ctx.Dropdowns = newDropdownRegistry(application, cfg, r.font, &r.style, r.ctx)
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
