package bar

import (
	"context"
	"log"
	"sync"

	"github.com/stubbedev/lys-desktop/service/upower"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
)

// lateServiceNames are the system services whose modules the bars
// build only while they run. One can start after the shell: NixOS
// starts NetworkManager without D-Bus activation, so a shell started
// while it restarts finds it absent, and a switch can bring any of
// them up later.
var lateServiceNames = []string{network.BusName, upower.BusName, bluetooth.BusName, powerprofiles.BusName}

// lateStart brings up a client the shell could not start without its
// service: apply hands it to the module context on the UI loop, stop
// ends it with the shell.
type lateStart func() (apply func(*ModuleContext), stop func(), err error)

// lateServices rebuilds the bars each time one of lateServiceNames
// gains an owner, first starting that service's client when starts
// holds one the shell still lacks. A start that fails is retried on
// the next appearance; one that succeeded is not run again.
type lateServices struct {
	mu        sync.Mutex
	stops     []func()
	unwatches []func() error
}

// watch follows the names on conn until ctx ends. invoke runs on the
// UI loop; rebuild gets the client to apply, nil when there is none.
func (l *lateServices) watch(ctx context.Context, conn *dbus.Conn, starts map[string]lateStart, invoke func(func()), rebuild func(apply func(*ModuleContext))) error {
	for _, name := range lateServiceNames {
		start := starts[name]
		unwatch, err := dbusx.WatchOwner(ctx, conn, name, func() {
			var apply func(*ModuleContext)
			if start != nil {
				a, stop, err := start()
				if err != nil {
					log.Printf("bar: %s appeared, but its client did not start: %v", name, err)
				} else {
					apply, start = a, nil
					l.mu.Lock()
					l.stops = append(l.stops, stop)
					l.mu.Unlock()
				}
			}
			invoke(func() { rebuild(apply) })
		})
		if err != nil {
			return err
		}
		l.mu.Lock()
		l.unwatches = append(l.unwatches, unwatch)
		l.mu.Unlock()
	}
	return nil
}

// stop ends the watches, taking their match rules off the shared
// connection, and every client a late start brought up.
func (l *lateServices) stop() {
	l.mu.Lock()
	unwatches := l.unwatches
	l.unwatches = nil
	l.mu.Unlock()
	for _, unwatch := range unwatches {
		if err := unwatch(); err != nil {
			log.Printf("bar: %v", err)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, stop := range l.stops {
		stop()
	}
	l.stops = nil
}
