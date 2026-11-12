// Package bussignal is what lys-shell's D-Bus signal readers share
// beyond dbusx's watches: a watch as a feed of ticks.
package bussignal

import (
	"context"
	"log"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Ticks is a watch as a feed of ticks: routes builds the watch's routes
// around tick, which a handler calls for a signal that changed what the
// caller reads. One tick is pending at most. The feed closes once the
// watch has ended, with ctx, with stop or with the connection; stop
// returns once the watch has taken its match rules off conn, with what
// Watch.Stop reports. A signal that does not decode is logged under
// what.
func Ticks(ctx context.Context, conn *dbus.Conn, from dbusx.Sender, what string, routes func(tick func()) []dbusx.Route) (ticks <-chan struct{}, stop func() error, err error) {
	feed := make(chan struct{}, 1)
	watch, err := dbusx.WatchSignals(ctx, conn, from,
		func(err error) { log.Printf("%s: %v", what, err) },
		routes(func() {
			select {
			case feed <- struct{}{}:
			default:
			}
		})...)
	if err != nil {
		return nil, nil, err
	}
	go func() {
		<-watch.Done()
		close(feed)
	}()
	return feed, watch.Stop, nil
}
