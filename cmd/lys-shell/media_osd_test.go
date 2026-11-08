package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/osd"
)

type osdWindow struct{}

func (osdWindow) Close()                      {}
func (osdWindow) SetAnchor(app.Anchor) error  { return nil }
func (osdWindow) SetMargin(app.Margins) error { return nil }
func (osdWindow) SetLayer(app.Layer) error    { return nil }

type nowPlayingOSD struct {
	mu     sync.Mutex
	root   widget.Widget
	shown  []string
	shows  chan struct{}
	loop   chan func()
	osd    *osd.Osd
	cancel context.CancelFunc
}

func startNowPlayingOSD(t *testing.T, mode config.OsdMedia, tracks osd.Tracks) *nowPlayingOSD {
	t.Helper()
	cfg := config.Defaults()
	cfg.Animations.Enabled = false
	cfg.Osd.Media = mode
	h := &nowPlayingOSD{shows: make(chan struct{}, 16), loop: make(chan func(), 64)}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for {
			select {
			case fn := <-h.loop:
				fn()
			case <-ctx.Done():
				return
			}
		}
	}()
	ready := make(chan func(), 1)
	h.osd = osd.New(osd.Deps{
		Open: func(lc app.LayerConfig) (osd.Window, error) {
			h.root = lc.Root
			return osdWindow{}, nil
		},
		Outputs: func() []*app.Output { return []*app.Output{{Name: "DP-1"}} },
		Invoke:  func(fn func()) { h.loop <- fn },
		After: func(d time.Duration, fn func()) {
			if d == time.Duration(cfg.Osd.Duration)*time.Millisecond {
				h.mu.Lock()
				h.shown = append(h.shown, treetest.WithClass(h.root, "osd-media-title")[0].(*widget.Label).Text())
				h.mu.Unlock()
				h.shows <- struct{}{}
				return
			}
			ready <- fn
		},
		Font: treetest.Font(t),
	}, cfg)
	(<-ready)()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		h.osd.Watch(ctx, nil, nil, nil, tracks, nil)
	}()
	t.Cleanup(func() { cancel(); <-watchDone; <-loopDone })
	return h
}

func (h *nowPlayingOSD) waitShow(t *testing.T) {
	t.Helper()
	select {
	case <-h.shows:
	case <-time.After(5 * time.Second):
		t.Fatal("the now-playing OSD never showed")
	}
}

func (h *nowPlayingOSD) titles() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.shown)
}

func startSkippingPlayer(t *testing.T) *prop.Properties {
	t.Helper()
	conn := dbustest.SessionConn(t)
	f := &fakePlayer{}
	path := dbus.ObjectPath("/org/mpris/MediaPlayer2")
	if err := conn.Export(f, path, "org.mpris.MediaPlayer2.Player"); err != nil {
		t.Fatal(err)
	}
	props, err := prop.Export(conn, path, prop.Map{
		"org.mpris.MediaPlayer2": {"Identity": {Value: "VLC", Emit: prop.EmitFalse}},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: "Playing", Emit: prop.EmitTrue},
			"CanGoNext":      {Value: true, Emit: prop.EmitFalse},
			"Metadata":       {Value: osdTrack("First"), Emit: prop.EmitTrue},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName("org.mpris.MediaPlayer2.vlc", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim player: %v %v", reply, err)
	}
	return props
}

func osdTrack(title string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"xesam:title":   dbus.MakeVariant(title),
		"xesam:artist":  dbus.MakeVariant([]string{"Band"}),
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/t/" + title)),
	}
}

func mediaNext(t *testing.T) {
	t.Helper()
	if _, stderr, code := runCaptured(t, false, "media", "next"); code != 0 {
		t.Fatalf("media next: %d %s", code, stderr)
	}
}

func TestMediaNextShowsTheNowPlayingOSD(t *testing.T) {
	dbustest.Session(t)
	player := startSkippingPlayer(t)
	c := serveMedia(t, nil)
	waitFor(t, func() bool { return activeName(c) == "org.mpris.MediaPlayer2.vlc" })
	h := startNowPlayingOSD(t, config.OsdMediaRequested, c)

	mediaNext(t)
	player.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", osdTrack("Second"))
	h.waitShow(t)

	player.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", osdTrack("Third"))
	waitFor(t, func() bool { p, _ := c.Active(); return p.Title == "Third" })
	mediaNext(t)
	player.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", osdTrack("Fourth"))
	h.waitShow(t)

	if got := h.titles(); !slices.Equal(got, []string{"Second", "Fourth"}) {
		t.Errorf("the OSD showed %q, want the two tracks media next skipped to and not the playlist advance", got)
	}
}

func TestEveryTrackChangeShowsWithOsdMediaAlways(t *testing.T) {
	dbustest.Session(t)
	player := startSkippingPlayer(t)
	c := serveMedia(t, nil)
	waitFor(t, func() bool { return activeName(c) == "org.mpris.MediaPlayer2.vlc" })
	h := startNowPlayingOSD(t, config.OsdMediaAlways, c)

	player.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", osdTrack("Second"))
	h.waitShow(t)
	if got := h.titles(); !slices.Equal(got, []string{"Second"}) {
		t.Errorf("osd.media always showed %q, want the playlist advance", got)
	}
}
