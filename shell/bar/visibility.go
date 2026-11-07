package bar

import (
	"log"
	"slices"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

// barSet tracks each output's bar for `lys-shell panel hide|show|toggle`
// (the Rust shell's BarMap plus the hidden_bars watcher) and config
// reloads. lys-ui cannot unmap a layer surface, so hiding closes the
// bar's layer and showing builds a fresh one with open.
type barSet struct {
	open  func(*app.Output, config.BarLayout) (barLayer, error)
	order []string
	bars  map[string]*barEntry
	// publish hears connectors() after each plug, unplug and reload;
	// nil until publishTo.
	publish func(connectors []string)
}

// barLayer is the part of a bar's layer window the set drives.
type barLayer interface {
	Close()
	// Rebuild refills the open bar from layout and the current config
	// on its own surface; an error means the surface refused the
	// change.
	Rebuild(layout config.BarLayout) error
}

type barEntry struct {
	output *app.Output
	layout config.BarLayout
	// shows is whether the layout gives the output a bar; hidden is
	// the Shell1 hide state. The layer is open exactly when shows and
	// not hidden.
	shows, hidden bool
	layer         barLayer
}

func newBarSet(open func(*app.Output, config.BarLayout) (barLayer, error)) *barSet {
	return &barSet{open: open, bars: map[string]*barEntry{}}
}

// connectors names the outputs whose layout gives them a bar, in
// creation order.
func (b *barSet) connectors() []string {
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
func (b *barSet) publishTo(publish func(connectors []string)) {
	b.publish = publish
	b.published()
}

func (b *barSet) published() {
	if b.publish != nil {
		b.publish(b.connectors())
	}
}

// apply closes the bars in hidden and reopens the others. It runs on
// the loop goroutine.
func (b *barSet) apply(hidden map[string]bool) {
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
func (b *barSet) reload(layoutFor func(connector string) (config.BarLayout, bool)) {
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

// sync opens or closes the entry's layer to match shows and hidden.
func (b *barSet) sync(name string, entry *barEntry) {
	want := entry.shows && !entry.hidden
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
// the loop) and serves dev.stubbe.lys.Shell1 over state, with lock as its
// Lock hook. The single-instance check claimed the id before RunWith; a
// shell without a session bus serves neither.
func serveShellIPC(conn *dbus.Conn, application *app.Application, state *shellipc.State, lock func() bool, dashboardToggle func(monitor string) error) func() {
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
	if release, err := shellipc.Serve(conn, state, shellipc.Hooks{
		Lock: lock,
		// The VPN callback goes straight to the native sign-in, whose
		// waiting browser sign-ins are process-wide, as the Rust
		// daemon's are.
		VPNSSOCallback:  network.NativeSignIn.DeliverSSOCallback,
		DashboardToggle: dashboardToggle,
	}); err == nil {
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
func (b *barSet) plug(output *app.Output, layout config.BarLayout, shows bool) {
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
	entry := &barEntry{output: output, layout: layout, shows: shows}
	b.order = append(b.order, output.Name)
	b.bars[output.Name] = entry
	b.sync(output.Name, entry)
	b.published()
}

// unplug closes an unplugged output's bar and forgets it.
func (b *barSet) unplug(output *app.Output) {
	for name, entry := range b.bars {
		if entry.output == output {
			b.drop(name)
		}
	}
	b.published()
}

func (b *barSet) drop(name string) {
	if entry := b.bars[name]; entry.layer != nil {
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
func (b *barSet) followOutputs(sess outputSource, layoutFor func(connector string) (config.BarLayout, bool), unplugged func(*app.Output)) (stop func()) {
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
