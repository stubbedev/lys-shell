package bar

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/screenshare"
)

// fakeShares is the portal's dev.stubbe.lys.ScreenShares1.
type fakeShares struct {
	conn    *dbus.Conn
	mu      sync.Mutex
	shares  []screenshare.Share
	stopped []dbus.ObjectPath
}

func (f *fakeShares) List() ([]screenshare.Share, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.shares), nil
}

func (f *fakeShares) Stop(session dbus.ObjectPath) *dbus.Error {
	f.mu.Lock()
	i := slices.IndexFunc(f.shares, func(s screenshare.Share) bool { return s.Session == session })
	if i < 0 {
		f.mu.Unlock()
		return dbusx.InvalidArgs("no screen share")
	}
	f.shares = slices.Delete(f.shares, i, i+1)
	f.stopped = append(f.stopped, session)
	f.mu.Unlock()
	f.changed()
	return nil
}

func (f *fakeShares) set(shares ...screenshare.Share) {
	f.mu.Lock()
	f.shares = shares
	f.mu.Unlock()
	f.changed()
}

func (f *fakeShares) changed() { _ = screenshare.Changed.Emit(f.conn, screenshare.Path, struct{}{}) }

// servePortalShares serves the fake on a private bus and points the bar
// at it.
func servePortalShares(t *testing.T) (*fakeShares, shareSource) {
	t.Helper()
	bus := dbustest.Start(t)
	f := &fakeShares{conn: bus.Conn(t)}
	release, err := dbusx.Serve(f.conn, dbusx.Service{
		Name: screenshare.BusName, Path: screenshare.Path, Interface: screenshare.Interface,
		Methods: f, Signals: []dbusx.SignalDecl{screenshare.Changed},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	shares, err := screenshare.Follow(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shares.Close)
	return f, shares
}

var chromeShare = screenshare.Share{
	Session: "/org/freedesktop/portal/desktop/session/1_9/s", App: "org.chromium.Chromium", Kind: screenshare.KindScreenCast,
	Targets: []screenshare.Target{{Kind: screenshare.TargetOutput, Name: "DP-1"}},
}

func TestTheIndicatorShowsWhileAShareRunsAndStopEndsIt(t *testing.T) {
	f, shares := servePortalShares(t)
	cfg := config.Defaults()
	cfg.ScreenShare.LabelShow = true
	ctx := newTestContext(t, cfg)
	ctx.ScreenShares = shares
	m, err := newScreenShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sm := m.(*countIndicator)
	defer sm.Stop()
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { sm.setChrome(chrome); return true })
	if onHeadlessLoop(chrome.Visible) {
		t.Fatal("the indicator shows with nothing shared")
	}

	f.set(chromeShare)
	waitHeadless(t, "the indicator", func() bool { return chrome.Visible() && sm.btn.Label().Text() == "1" })

	v := onHeadlessLoop(func() *screenShareView { return screenShareDropdown(ctx).(*screenShareView) })
	defer onHeadlessLoop(func() bool { v.dropdownClosed(); return true })
	labels := onHeadlessLoop(func() []string { return treetest.Labels(v) })
	if !slices.Contains(labels, "org.chromium.Chromium") || !slices.ContainsFunc(labels, func(l string) bool { return strings.Contains(l, "DP-1") }) {
		t.Errorf("dropdown labels %q, want the app and its screen", labels)
	}
	stops := onHeadlessLoop(func() []widget.Widget { return treetest.WithClass(v, "screen-share-stop") })
	if len(stops) != 1 {
		t.Fatalf("%d stop buttons, want one per share", len(stops))
	}
	onHeadlessLoop(func() bool { stops[0].(*widget.Button).OnClick(); return true })

	waitHeadless(t, "the indicator gone", func() bool { return !chrome.Visible() })
	f.mu.Lock()
	stopped := slices.Clone(f.stopped)
	f.mu.Unlock()
	if !slices.Equal(stopped, []dbus.ObjectPath{chromeShare.Session}) {
		t.Errorf("stopped %v, want the share's session", stopped)
	}
	waitHeadless(t, "the empty dropdown", func() bool { return len(treetest.WithClass(v, "screen-share-stop")) == 0 })
}

func TestTheIndicatorStaysWhileAnotherShareRuns(t *testing.T) {
	f, shares := servePortalShares(t)
	ctx := newTestContext(t, config.Defaults())
	ctx.ScreenShares = shares
	m, err := newScreenShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sm := m.(*countIndicator)
	defer sm.Stop()
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { sm.setChrome(chrome); return true })
	other := screenshare.Share{
		Session: "/s/2", Kind: screenshare.KindScreenCast,
		Targets: []screenshare.Target{{Kind: screenshare.TargetWindow, Name: "lys-wm-3", Title: "Notes"}},
	}
	f.set(chromeShare, other)
	waitHeadless(t, "two shares", func() bool { return sm.count == 2 })
	f.set(other)
	waitHeadless(t, "one share", func() bool { return sm.count == 1 })
	if !onHeadlessLoop(chrome.Visible) {
		t.Error("the indicator went while a share still runs")
	}
	if got := shareTargets(other); got != "Window \u201c\u2068Notes\u2069\u201d" {
		t.Errorf("window target %q, want its title", got)
	}
	if got := shareApp(other); got != "An app" {
		t.Errorf("an app without an id reads %q", got)
	}
}
