package bar

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

// surfaceSet tracks each output's surface of one kind, a bar or a dock,
// built from a per-output layout L, for config reloads and, for the bars,
// `lys-shell panel hide|show|toggle` (the Rust shell's BarMap plus the
// hidden_bars watcher). lys-ui cannot unmap a layer surface, so hiding
// closes the layer and showing builds a fresh one with open.
type surfaceSet[L any] struct {
	open  func(*app.Output, L) (surfaceLayer[L], error)
	await awaitFirstFrame
	order []string
	bars  map[string]*surfaceEntry[L]
	// publish hears connectors() after each plug, unplug and reload;
	// nil until publishTo.
	publish func(connectors []string)
}

// surfaceLayer is the part of a layer window the set drives.
type surfaceLayer[L any] interface {
	Close()
	// Rebuild refills the open bar from layout and the current config
	// on its own surface; an error means the surface refused the
	// change.
	Rebuild(layout L) error
}

type surfaceEntry[L any] struct {
	output *app.Output
	layout L
	// shows is whether the layout gives the output a bar; hidden is
	// the Shell1 hide state. The layer is open exactly when shows and
	// not hidden, once the output's wallpaper is ready.
	shows, hidden, ready bool
	cancelWait           func()
	layer                surfaceLayer[L]
}

type awaitFirstFrame func(out *app.Output, done func()) (cancel func())

func noWallpaper(_ *app.Output, done func()) func() {
	done()
	return func() {}
}

func newSurfaceSet[L any](open func(*app.Output, L) (surfaceLayer[L], error), await awaitFirstFrame) *surfaceSet[L] {
	return &surfaceSet[L]{open: open, await: await, bars: map[string]*surfaceEntry[L]{}}
}

// connectors names the outputs whose layout gives them a bar, in
// creation order.
func (b *surfaceSet[L]) connectors() []string {
	var out []string
	for _, name := range b.order {
		if b.bars[name].shows {
			out = append(out, name)
		}
	}
	return out
}

// publishTo hands publish the connectors now and after every later
// change of the set. It runs on the loop goroutine, as the changes do.
func (b *surfaceSet[L]) publishTo(publish func(connectors []string)) {
	b.publish = publish
	b.published()
}

func (b *surfaceSet[L]) published() {
	if b.publish != nil {
		b.publish(b.connectors())
	}
}

// apply closes the bars in hidden and reopens the others. It runs on
// the loop goroutine.
func (b *surfaceSet[L]) apply(hidden map[string]bool) {
	for _, name := range b.order {
		entry := b.bars[name]
		entry.hidden = hidden[name]
		b.sync(name, entry)
	}
}

// reload rebuilds every bar from layoutFor (a config reload): an open
// bar that stays rebuilds on its own surface, and outputs the new
// layout shows or hides follow it. A surface that refuses the rebuild
// is closed and opened afresh. The Shell1 hide state survives.
func (b *surfaceSet[L]) reload(layoutFor func(connector string) (L, bool)) {
	for _, name := range b.order {
		entry := b.bars[name]
		entry.layout, entry.shows = layoutFor(name)
		if entry.layer != nil && entry.shows && !entry.hidden {
			if err := entry.layer.Rebuild(entry.layout); err != nil {
				log.Printf("bar %s: rebuild: %v; reopening it", name, err)
				entry.layer.Close()
				entry.layer = nil
			}
		}
		b.sync(name, entry)
	}
	b.published()
}

// sync opens or closes the entry's layer to match shows, hidden and ready.
func (b *surfaceSet[L]) sync(name string, entry *surfaceEntry[L]) {
	want := entry.shows && !entry.hidden && entry.ready
	switch {
	case !want && entry.layer != nil:
		entry.layer.Close()
		entry.layer = nil
	case want && entry.layer == nil:
		layer, err := b.open(entry.output, entry.layout)
		if err != nil {
			log.Printf("bar %s: show: %v", name, err)
			return
		}
		entry.layer = layer
	}
}

// serveShellIPC keeps the application id on conn (its quit action ends
// the loop) and serves dev.stubbe.lys.Shell1 over state with the shell's
// hooks. The single-instance check claimed the id before RunWith; a
// shell without a session bus serves neither.
func serveShellIPC(conn *dbus.Conn, application *app.Application, state *shellipc.State, hooks shellipc.Hooks) func() {
	if conn == nil {
		log.Print("shell ipc: no session bus")
		return func() {}
	}
	var releases []func()
	run := shellipc.Actions{
		Quit:    func() { application.Invoke(application.Quit) },
		Inspect: func() { application.Invoke(func() { application.SetInspect(true) }) },
	}
	if release, err := shellipc.ServeApplication(conn, run); err == nil {
		releases = append(releases, release)
	} else {
		log.Printf("shell ipc: application id: %v", err)
	}
	hooks.VPNSSOCallback = network.NativeSignIn.DeliverSSOCallback
	if release, err := shellipc.Serve(conn, state, hooks); err == nil {
		releases = append(releases, release)
	} else {
		log.Printf("shell ipc: %v", err)
	}
	return func() {
		for _, release := range releases {
			release()
		}
	}
}

// plug registers a hotplugged output once its connector name is known
// (SyncMonitors) and opens its bar when the layout shows one. An
// output already registered under its name is left alone; one that
// was renamed is moved.
func (b *surfaceSet[L]) plug(output *app.Output, layout L, shows bool) {
	for name, entry := range b.bars {
		if entry.output == output && name != output.Name {
			b.drop(name)
		}
	}
	if entry, ok := b.bars[output.Name]; ok {
		if entry.output == output {
			return
		}
		b.drop(output.Name)
	}
	entry := &surfaceEntry[L]{output: output, layout: layout, shows: shows}
	b.order = append(b.order, output.Name)
	b.bars[output.Name] = entry
	entry.cancelWait = b.await(output, func() {
		entry.ready = true
		b.sync(output.Name, entry)
	})
	b.published()
}

// unplug closes an unplugged output's bar and forgets it.
func (b *surfaceSet[L]) unplug(output *app.Output) {
	for name, entry := range b.bars {
		if entry.output == output {
			b.drop(name)
		}
	}
	b.published()
}

func (b *surfaceSet[L]) close() {
	for _, name := range slices.Clone(b.order) {
		b.drop(name)
	}
	b.published()
}

func (b *surfaceSet[L]) drop(name string) {
	entry := b.bars[name]
	entry.cancelWait()
	if entry.layer != nil {
		entry.layer.Close()
	}
	delete(b.bars, name)
	b.order = slices.DeleteFunc(b.order, func(n string) bool { return n == name })
}

// followOutputs gives every named output of sess its bar from
// layoutFor, the startup outputs and each one plugged or renamed later,
// the Rust SyncMonitors; unplugged runs once an unplugged output's bar
// is gone. The returned stop ends the watch. It runs on the loop
// goroutine.
func (b *surfaceSet[L]) followOutputs(sess outputSource, layoutFor func(connector string) (L, bool), unplugged func(*app.Output)) (stop func()) {
	plug := func(output *app.Output) {
		if output.Name == "" {
			return
		}
		layout, show := layoutFor(output.Name)
		b.plug(output, layout, show)
	}
	for _, output := range sess.Outputs() {
		plug(output)
	}
	stopIdentity := sess.WatchOutputIdentity(plug)
	stopOutputs := sess.WatchOutputs(nil, func(output *app.Output) {
		b.unplug(output)
		unplugged(output)
	})
	return func() {
		stopOutputs()
		stopIdentity()
	}
}

// outputSource is the part of the Wayland session followOutputs reads.
type outputSource interface {
	Outputs() []*app.Output
	WatchOutputIdentity(fn func(*app.Output)) (stop func())
	WatchOutputs(added, removed func(*app.Output)) (stop func())
}

func logOutputWait(sess outputSource) (stop func()) {
	waiting := false
	check := func() {
		none := len(sess.Outputs()) == 0
		switch {
		case none && !waiting:
			log.Print("bar: the compositor has no output; the bar, wallpaper and OSD wait for one")
		case !none && waiting:
			log.Print("bar: an output appeared; the bar, wallpaper and OSD return")
		}
		waiting = none
	}
	check()
	return sess.WatchOutputs(func(*app.Output) { check() }, func(*app.Output) { check() })
}

// restyler is a surface that takes a config's styling in place.
type restyler interface{ Restyle(cfg *config.Config) }

// restyle restyles every open surface that can be, in place.
func (b *surfaceSet[L]) restyle(cfg *config.Config) {
	for _, name := range b.order {
		if r, ok := b.bars[name].layer.(restyler); ok {
			r.Restyle(cfg)
		}
	}
}

// peeker is a surface `lys-shell panel reveal` shows for a while.
type peeker interface{ peek(d time.Duration) }

// peek reveals monitor's bar ("" every bar) for d, where it auto-hides.
func (b *surfaceSet[L]) peek(monitor string, d time.Duration) {
	for _, name := range b.order {
		if monitor != "" && name != monitor {
			continue
		}
		if p, ok := b.bars[name].layer.(peeker); ok {
			p.peek(d)
		}
	}
}

// loopAnswerTimeout bounds how long a Shell1 call waits for the loop.
const loopAnswerTimeout = 2 * time.Second

// answerOnLoop runs fn on the loop and returns its error, failing with
// what was asked once the loop leaves it unanswered for
// loopAnswerTimeout.
func answerOnLoop(application *app.Application, what string, fn func() error) error {
	done := make(chan error, 1)
	application.Invoke(func() { done <- fn() })
	select {
	case err := <-done:
		return err
	case <-time.After(loopAnswerTimeout):
		return fmt.Errorf("%s: the shell did not answer in %v", what, loopAnswerTimeout)
	}
}

// keyboardWalker is a surface `lys-shell panel focus` hands the
// keyboard.
type keyboardWalker interface{ walkKeyboard() error }

// walk hands monitor's bar the keyboard; "" picks the bar on preferred,
// the focused output, or else the first bar.
func (b *surfaceSet[L]) walk(monitor, preferred string) error {
	target := monitor
	if target == "" {
		target = preferred
		if e, ok := b.bars[target]; !ok || e.layer == nil {
			target = b.firstOpen()
		}
	}
	e, ok := b.bars[target]
	if !ok || e.layer == nil {
		if monitor == "" {
			return errors.New("no bar is open")
		}
		return fmt.Errorf("no bar is open on %s", monitor)
	}
	w, ok := e.layer.(keyboardWalker)
	if !ok {
		return fmt.Errorf("the surface on %s takes no keyboard", target)
	}
	return w.walkKeyboard()
}

// firstOpen is the first output in order with an open surface, "" when
// none has one.
func (b *surfaceSet[L]) firstOpen() string {
	for _, name := range b.order {
		if b.bars[name].layer != nil {
			return name
		}
	}
	return ""
}
