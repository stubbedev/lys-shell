// Package screenshare is the screen shares the desktop portal runs: what
// each ScreenCast or RemoteDesktop session streams, and a way to stop
// one. The portal serves Interface; Shares follows it for the bar.
package screenshare

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/feed"
)

// D-Bus identity: the portal backend's name and object, where Interface
// sits beside the org.freedesktop.impl.portal ones.
const (
	BusName   = "org.freedesktop.impl.portal.desktop.lys"
	Path      = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	Interface = "dev.stubbe.lys.ScreenShares1"
)

// The kinds of share.
const (
	KindScreenCast    = "screencast"
	KindRemoteDesktop = "remotedesktop"
)

// The kinds of target.
const (
	TargetOutput  = "output"
	TargetWindow  = "window"
	TargetRegion  = "region"
	TargetDynamic = "dynamic"
)

// Target is one source a share streams.
type Target struct {
	Kind string
	// Name is the output's connector, or the window's identifier.
	Name string
	// Title is a window's title where the compositor tells it.
	Title string
}

// Share is one session streaming the screen.
type Share struct {
	Session dbus.ObjectPath
	// App is the app's id; "" for an app outside a sandbox.
	App     string
	Kind    string
	Targets []Target
}

// Changed is emitted when a share starts or ends.
var Changed = dbusx.NewSignal[struct{}](Interface, "Changed")

// callTimeout bounds a call to the portal.
const callTimeout = 2 * time.Second

// Shares follows the portal's shares on a session bus: the list is read
// again on each Changed and each time the portal comes or goes.
type Shares struct {
	conn    *dbus.Conn
	cancel  context.CancelFunc
	changes feed.Feed[struct{}]

	mu   sync.Mutex
	list []Share
}

// Follow starts following the shares on conn.
func Follow(conn *dbus.Conn) (*Shares, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Shares{conn: conn, cancel: cancel}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	match := []dbus.MatchOption{dbus.WithMatchSender(BusName), dbus.WithMatchObjectPath(Path), dbus.WithMatchInterface(Interface)}
	if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
		conn.RemoveSignal(signals)
		cancel()
		return nil, fmt.Errorf("screen shares: watch %s: %w", Interface, err)
	}
	if err := conn.AddMatchSignalContext(ctx, dbusx.OwnerMatch(BusName)...); err != nil {
		conn.RemoveSignal(signals)
		cancel()
		return nil, fmt.Errorf("screen shares: watch %s: %w", BusName, err)
	}
	s.refresh(ctx)
	go func() {
		defer conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if _, _, isOwner := dbusx.NameOwnerChange(sig, BusName); isOwner || sig.Name == Changed.Name() {
					s.refresh(ctx)
				}
			}
		}
	}()
	return s, nil
}

// refresh reads the list; a portal that does not answer shares nothing.
func (s *Shares) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var list []Share
	if err := s.conn.Object(BusName, Path).CallWithContext(ctx, Interface+".List", 0).Store(&list); err != nil {
		list = nil
	}
	s.mu.Lock()
	same := slices.EqualFunc(s.list, list, sameShare)
	s.list = list
	s.mu.Unlock()
	if !same {
		s.changes.Publish(struct{}{})
	}
}

func sameShare(a, b Share) bool {
	return a.Session == b.Session && a.App == b.App && a.Kind == b.Kind && slices.Equal(a.Targets, b.Targets)
}

// List is the shares running now.
func (s *Shares) List() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// Subscribe ticks whenever the list changed.
func (s *Shares) Subscribe() (<-chan struct{}, func()) { return s.changes.Subscribe() }

// Stop ends the share of session.
func (s *Shares) Stop(ctx context.Context, session dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := s.conn.Object(BusName, Path).CallWithContext(ctx, Interface+".Stop", 0, session).Err; err != nil {
		return fmt.Errorf("screen shares: stop %s: %w", session, err)
	}
	return nil
}

// Close stops following.
func (s *Shares) Close() {
	s.cancel()
	s.changes.Close()
}
