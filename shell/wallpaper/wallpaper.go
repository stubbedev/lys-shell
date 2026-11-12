// Package wallpaper renders the wallpaper service natively
// (crates/lys-shell/src/shell/wallpaper, bootstrap/wallpaper.rs, and
// the hotplug half of watchers/wallpaper.rs): one Background layer
// surface per output, reconciled against the service's per-monitor
// state, each swapping images with the wallpaper transition. lys-shell is
// the wallpaper provider; no swww or other tool is involved.
package wallpaper

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/imagedecode"
	"github.com/stubbedev/lys-desktop/outputsize"
	"github.com/stubbedev/lys-desktop/service/wallpaper/extract"
	"github.com/stubbedev/lys-desktop/styling/material"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// frameInterval paces transition steps.
const frameInterval = 16 * time.Millisecond

const firstFrameLimit = time.Second

const firstFrameBound = 2 * firstFrameLimit

// Shell owns the wallpaper service and its surfaces. Its methods run
// on the lys-ui loop goroutine.
type Shell struct {
	ui       loop
	svc      *wallpaper.Service
	cfg      config.WallpaperConfig
	anims    config.AnimationsConfig
	outputs  map[string]*app.Output
	surfaces map[string]*surface
	rasters  *rasters
	waits    map[*app.Output]*frameWait
}

type frameWait struct {
	waiters     []*frameWaiter
	cancelBound func()
}

type frameWaiter struct{ done func() }

// loop is what the shell does through the application: open a
// surface (handing back its close), and run a ticker on the loop.
type loop struct {
	newLayer func(app.LayerConfig) (closeLayer func(), err error)
	every    func(d time.Duration, fn func()) (cancel func())
	after    func(d time.Duration, fn func()) (cancel func())
}

// appLoop is the application's loop.
func appLoop(application *app.Application) loop {
	return loop{
		newLayer: func(cfg app.LayerConfig) (func(), error) {
			layer, err := application.NewLayer(cfg)
			if err != nil {
				return nil, err
			}
			return layer.Close, nil
		},
		every: application.Every,
		after: func(d time.Duration, fn func()) func() {
			var stop func()
			stop = application.Every(d, func() {
				stop()
				fn()
			})
			return stop
		},
	}
}

func newShell(ui loop, svc *wallpaper.Service, cfg *config.Config, d rasterDeps) *Shell {
	return &Shell{
		ui: ui, svc: svc, cfg: cfg.Wallpaper, anims: cfg.Animations,
		outputs: map[string]*app.Output{}, surfaces: map[string]*surface{},
		rasters: newRasters(d), waits: map[*app.Output]*frameWait{},
	}
}

// Start builds the service from config (build_wallpaper_service),
// registers the outputs that have a connector name, serves
// dev.stubbe.lys.Wallpaper1 on conn, and starts the cycling and extraction
// loops and the surfaces. It fails like the Rust service init when
// the D-Bus name is taken (another shell is running): no wallpaper is
// drawn then. The returned stop function ends the loops, releases the
// name, and lets go of the wallpapers once the loop has ended.
func Start(application *app.Application, outputs []*app.Output, cfg *config.Config, conn *dbus.Conn) (*Shell, func(), error) {
	var s *Shell
	svc := wallpaper.New(wallpaper.Options{
		Extractor:      extract.FromConfig(cfg.Styling.ColorExtractor),
		ThemingMonitor: cfg.Styling.ColorExtractor.ThemingMonitor,
		SharedCycle:    cfg.Wallpaper.CyclingSameImage,
		Sample:         func(ctx context.Context, monitor string) ([]material.ARGB, error) { return s.sample(ctx, monitor) },
	})
	release, err := wallpaper.Export(conn, svc)
	if err != nil {
		return nil, nil, err
	}
	s = newShell(appLoop(application), svc, cfg, rasterDeps{
		decode: imagedecode.OpenOffHeap,
		spawn:  func(fn func()) { go fn() },
		invoke: application.Invoke,
		forget: application.ForgetImage,
		cache:  openRasterCache(),
	})
	for _, out := range outputs {
		if out.Name != "" {
			s.outputs[out.Name] = out
			svc.RegisterMonitor(out.Name)
		}
	}
	Bootstrap(svc, cfg.Wallpaper)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)
	changes, unsubscribe := svc.Changes()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
				application.Invoke(s.reconcile)
			}
		}
	}()
	s.reconcile()
	return s, func() {
		cancel()
		unsubscribe()
		release()
		s.shutdown()
	}, nil
}

// shutdown lets go of every surface's wallpaper once the loop is done:
// nothing draws them any more, so their rasters are freed, and so are
// those a decode or resample still running makes.
func (s *Shell) shutdown() {
	for out, w := range s.waits {
		w.cancelBound()
		delete(s.waits, out)
	}
	for name, surf := range s.surfaces {
		surf.drop()
		delete(s.surfaces, name)
	}
	s.rasters.close()
}

// OutputWatcher is the session's output hotplug feed: the identity of
// a monitor once its connector name is known, and its removal.
type OutputWatcher interface {
	WatchOutputIdentity(fn func(*app.Output)) (stop func())
	WatchOutputs(added, removed func(*app.Output)) (stop func())
}

// Launch is Start on the shell's session connection, following
// hotplugged outputs through watch. A failure is logged and leaves the
// shell without wallpapers (nil).
func Launch(application *app.Application, outputs []*app.Output, cfg *config.Config, conn *dbus.Conn, watch OutputWatcher) (*Shell, func()) {
	if conn == nil {
		log.Print("wallpaper: cannot initialize wallpaper service: no session bus")
		return nil, func() {}
	}
	s, stop, err := Start(application, outputs, cfg, conn)
	if err != nil {
		log.Printf("wallpaper: cannot initialize wallpaper service: %v", err)
		return nil, func() {}
	}
	stopIdentity := watch.WatchOutputIdentity(s.AddOutput)
	stopRemoved := watch.WatchOutputs(nil, s.RemoveOutput)
	return s, func() {
		stopIdentity()
		stopRemoved()
		stop()
	}
}

// Service exposes the running service (theme providers subscribe to
// its Extracted ticks).
func (s *Shell) Service() *wallpaper.Service { return s.svc }

// Bootstrap applies the config to freshly registered monitors: the
// global fit mode, then cycling when a directory is set (else the
// single image), then the [[wallpaper.monitors]] entries on top.
//
// The Rust bootstrap leaves the service fit at fill and has the
// surfaces read fit-mode from config instead; seeding it here keeps the
// service, the D-Bus getters, and the pixels in agreement.
func Bootstrap(svc *wallpaper.Service, cfg config.WallpaperConfig) {
	svc.SetFitMode(cfg.FitMode, "")
	cycling := false
	if cfg.CyclingDirectory != "" {
		interval := cyclingInterval(cfg)
		if err := svc.StartCycling(cfg.CyclingDirectory, interval, cfg.CyclingMode); err != nil {
			log.Printf("wallpaper: could not start wallpaper cycling from config: %v", err)
		} else {
			cycling = true
		}
	}
	if !cycling && cfg.Wallpaper != "" {
		if err := svc.SetWallpaper(cfg.Wallpaper, ""); err != nil {
			log.Printf("wallpaper: cannot apply single-file wallpaper from config: %v", err)
		}
	}
	registered := svc.Monitors()
	for _, m := range cfg.Monitors {
		if _, ok := registered[m.Name]; m.Name == "" || !ok {
			continue
		}
		svc.SetFitMode(m.FitMode, m.Name)
		if m.Wallpaper == "" {
			continue
		}
		if err := svc.SetWallpaper(m.Wallpaper, m.Name); err != nil {
			log.Printf("wallpaper: wallpaper path not found for %s: %s", m.Name, m.Wallpaper)
		}
	}
}

// hotplugFitMode is the fit mode for a monitor appearing after
// startup: its own entry, else the global fit-mode.
func hotplugFitMode(cfg config.WallpaperConfig, name string) wallpaper.FitMode {
	if m, ok := cfg.Monitor(name); ok {
		return m.FitMode
	}
	return cfg.FitMode
}

// hotplugWallpaper is the image for a monitor appearing after startup;
// false leaves the service state alone: registration already seeded
// it from an active cycle, or no image is configured at all.
func hotplugWallpaper(cfg config.WallpaperConfig, name string, cycling bool) (string, bool) {
	if cycling {
		return "", false
	}
	path := cfg.Wallpaper
	if m, ok := cfg.Monitor(name); ok && m.Wallpaper != "" {
		path = m.Wallpaper
	}
	return path, path != ""
}

// AddOutput registers an output once its connector name is known and
// applies the config a hotplugged monitor gets (spawn_hotplug_watcher).
func (s *Shell) AddOutput(out *app.Output) {
	if out.Name == "" {
		return
	}
	s.outputs[out.Name] = out
	if _, known := s.svc.Monitors()[out.Name]; !known {
		s.svc.RegisterMonitor(out.Name)
		s.svc.SetFitMode(hotplugFitMode(s.cfg, out.Name), out.Name)
		if path, ok := hotplugWallpaper(s.cfg, out.Name, s.svc.CyclingConfig() != nil); ok {
			if err := s.svc.SetWallpaper(path, out.Name); err != nil {
				log.Printf("wallpaper: cannot apply wallpaper to new monitor %s: %v", out.Name, err)
			}
		}
	}
	s.reconcile()
}

// RemoveOutput drops an unplugged output from the service.
func (s *Shell) RemoveOutput(out *app.Output) {
	if out.Name == "" || s.outputs[out.Name] != out {
		return
	}
	delete(s.outputs, out.Name)
	s.svc.UnregisterMonitor(out.Name)
	s.reconcile()
}

// resolve is the image and fit a monitor renders (reconcile): its own
// state, else its [[wallpaper.monitors]] image, else the global one, else
// the built-in one.
func resolve(cfg config.WallpaperConfig, name string, st wallpaper.MonitorState) (string, wallpaper.FitMode) {
	path := st.Wallpaper
	if path == "" {
		if m, ok := cfg.Monitor(name); ok {
			path = m.Wallpaper
		}
	}
	if path == "" {
		path = cfg.Wallpaper
	}
	if path == "" {
		path = builtinWallpaper
	}
	return path, st.FitMode
}

// reconcile creates, updates, and removes surfaces to match the
// service state and the known outputs (keeps_surface: both sides must
// know a monitor for its surface to live).
func (s *Shell) reconcile() {
	monitors := s.svc.Monitors()
	for name, surf := range s.surfaces {
		_, registered := monitors[name]
		if out, ok := s.outputs[name]; !registered || !ok || surf.out != out {
			surf.close()
			delete(s.surfaces, name)
			s.release(surf.out)
		}
	}
	for name, st := range monitors {
		out, ok := s.outputs[name]
		if !ok {
			continue
		}
		surf, ok := s.surfaces[name]
		if !ok {
			surf = s.newSurface(name, out)
			s.surfaces[name] = surf
		}
		path, fit := resolve(s.cfg, name, st)
		surf.render(path, fit)
	}
}

// AwaitFirstFrame runs done on the loop once out's wallpaper has
// presented a frame: at once when it has, when out is left with no
// wallpaper to wait for, or, logged, after firstFrameBound. cancel
// drops done unrun.
func (s *Shell) AwaitFirstFrame(out *app.Output, done func()) (cancel func()) {
	if surf, ok := s.surfaces[out.Name]; ok && surf.out == out && surf.shown {
		done()
		return func() {}
	}
	w, ok := s.waits[out]
	if !ok {
		w = &frameWait{}
		w.cancelBound = s.ui.after(firstFrameBound, func() {
			log.Printf("wallpaper: %s has shown no frame after %v; the surfaces waiting for it map without it", out.Name, firstFrameBound)
			s.release(out)
		})
		s.waits[out] = w
	}
	waiter := &frameWaiter{done: done}
	w.waiters = append(w.waiters, waiter)
	return func() {
		w.waiters = slices.DeleteFunc(w.waiters, func(x *frameWaiter) bool { return x == waiter })
		if len(w.waiters) == 0 && s.waits[out] == w {
			delete(s.waits, out)
			w.cancelBound()
		}
	}
}

func (s *Shell) release(out *app.Output) {
	w, ok := s.waits[out]
	if !ok {
		return
	}
	delete(s.waits, out)
	w.cancelBound()
	for _, waiter := range w.waiters {
		waiter.done()
	}
}

func (s *Shell) presented(surf *surface) {
	if surf.dropped {
		return
	}
	surf.shown = true
	surf.stopLimit()
	s.release(surf.out)
}

// surface is one output's Background layer.
type surface struct {
	shell *Shell
	name  string
	out   *app.Output
	// closeLayer closes the layer surface; nil until it opens.
	closeLayer func()
	view       *view
	// path and fit are what was last asked for; gen invalidates a
	// decode that a newer request overtook.
	path        string
	fit         wallpaper.FitMode
	gen         uint64
	stopAnim    func()
	falling     bool
	dropped     bool
	shown       bool
	cancelLimit func()
}

func (s *Shell) newSurface(name string, out *app.Output) *surface {
	surf := &surface{shell: s, name: name, out: out, view: newView()}
	surf.cancelLimit = s.ui.after(firstFrameLimit, func() {
		surf.cancelLimit = nil
		log.Printf("wallpaper: %s: %s is not ready after %v; showing the built-in wallpaper until it is", surf.name, surf.path, firstFrameLimit)
		surf.fallback()
	})
	return surf
}

func (surf *surface) stopLimit() {
	if surf.cancelLimit != nil {
		surf.cancelLimit()
		surf.cancelLimit = nil
	}
}

func (surf *surface) openLayer() {
	s := surf.shell
	closeLayer, err := s.ui.newLayer(app.LayerConfig{
		Output:        surf.out,
		Layer:         app.LayerBackground,
		Anchor:        app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone: -1,
		Keyboard:      app.KeyboardNone,
		Namespace:     "lys-shell-wallpaper",
		Root:          surf.view,
		Background:    backdrop,
		OnPresented:   func() { s.presented(surf) },
		OnClosed: func() {
			// The output went away (or the compositor closed us): the
			// next reconcile rebuilds it if the monitor is still known.
			surf.drop()
			if s.surfaces[surf.name] == surf {
				delete(s.surfaces, surf.name)
			}
			s.release(surf.out)
		},
	})
	if err != nil {
		log.Printf("wallpaper: surface for %s: %v", surf.name, err)
		surf.drop()
		if s.surfaces[surf.name] == surf {
			delete(s.surfaces, surf.name)
		}
		s.release(surf.out)
		return
	}
	surf.closeLayer = closeLayer
}

func (surf *surface) close() {
	surf.drop()
	if surf.closeLayer != nil {
		surf.closeLayer()
	}
}

// drop stops what the surface has in flight and lets go of its
// wallpapers: a decode still running lands nowhere.
func (surf *surface) drop() {
	surf.stopLimit()
	if surf.stopAnim != nil {
		surf.stopAnim()
		surf.stopAnim = nil
	}
	surf.gen++
	surf.dropped = true
	surf.view.clear()
}

func (surf *surface) fallback() {
	if surf.view.cur != nil || surf.falling || surf.path == builtinWallpaper {
		return
	}
	surf.falling = true
	box, known := outputTarget(surf.out)
	rs := surf.shell.rasters
	rs.acquire(rasterKey{path: builtinWallpaper, fit: surf.fit, box: box, known: known}, func(r *raster) {
		if surf.dropped || surf.view.cur != nil {
			rs.release(r)
			return
		}
		surf.swap(r, false)
	}, func() {})
}

// outputTarget is the output's device-pixel size and scale; false when
// the compositor has not reported them yet.
func outputTarget(out *app.Output) (target, bool) {
	size, ok := outputsize.Device(out)
	if !ok {
		return target{}, false
	}
	scale := float64(max(out.Scale, 1))
	if out.LogicalW > 0 {
		scale = float64(size.X) / float64(out.LogicalW)
	}
	return target{w: size.X, h: size.Y, scale: scale}, true
}

// render asks for path's raster at fit for the output and swaps it in
// once it is made. A repeat of the current request is a no-op; a fit
// change alone re-renders without a transition.
func (surf *surface) render(path string, fit wallpaper.FitMode) {
	if path == surf.path && fit == surf.fit {
		return
	}
	animate := path != surf.path
	surf.path, surf.fit = path, fit
	surf.gen++
	gen := surf.gen
	box, known := outputTarget(surf.out)
	rs := surf.shell.rasters
	rs.acquire(rasterKey{path: path, fit: fit, box: box, known: known}, func(r *raster) {
		if gen != surf.gen {
			rs.release(r)
			return
		}
		surf.swap(r, animate)
	}, func() {
		if gen == surf.gen {
			surf.fallback()
		}
	})
}

func (surf *surface) swap(r *raster, animate bool) {
	if surf.stopAnim != nil {
		surf.stopAnim()
		surf.stopAnim = nil
	}
	pic := widget.NewImage(r.image)
	pic.SetScale(r.scale)
	trans := transitionOf(surf.shell.anims)
	kind := trans.Kind
	if !animate || trans.Duration <= 0 {
		kind = TransitionNone
	}
	rs := surf.shell.rasters
	transitions := surf.view.show(&shown{pic: pic, size: r.size(), release: func() { rs.release(r) }}, kind)
	if surf.closeLayer == nil {
		surf.openLayer()
	}
	if !transitions || surf.dropped {
		return
	}
	start := time.Now()
	surf.stopAnim = surf.shell.ui.every(frameInterval, func() {
		t := float64(time.Since(start)) / float64(trans.Duration)
		if !surf.view.step(t) && surf.stopAnim != nil {
			surf.stopAnim()
			surf.stopAnim = nil
		}
	})
}
