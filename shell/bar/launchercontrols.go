package bar

import (
	"context"
	"log"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-desktop/service/pulse"

	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

// dndSwitch is the notifications' do-not-disturb.
type dndSwitch interface {
	DND() bool
	SetDND(on bool)
}

// wifiSwitch is the Wi-Fi radio.
type wifiSwitch interface {
	Enabled(ctx context.Context) (bool, error)
	SetEnabled(ctx context.Context, on bool) error
}

// recorderSwitch is the screen recorder.
type recorderSwitch interface {
	Snapshot() recorder.Change
	Start()
	Stop()
}

// controlSources are the services the search panel's controls drive; a
// nil one offers no rows.
type controlSources struct {
	dnd        dndSwitch
	wifi       wifiSwitch
	bluetooth  bluetooth.Source
	audio      pulse.Source
	media      mpris.Source
	screenshot func(mode, target string)
	readScreen func(kind screenshot.ReadKind)
	ocr        func() bool
	recorder   recorderSwitch
	pickColor  func()
	// wallpaper opens the wallpaper picker; nil without one.
	wallpaper func()
}

// controlSourcesOf picks the sources out of the running shell.
func controlSourcesOf(ctx ModuleContext) controlSources {
	s := controlSources{
		bluetooth: ctx.Bluetooth, audio: ctx.Pulse, media: ctx.Media,
		screenshot: ctx.Screenshot, pickColor: ctx.PickColor,
		readScreen: ctx.ReadScreen, ocr: screenshot.OCRAvailable,
	}
	if ctx.Notifications != nil {
		s.dnd = ctx.Notifications
	}
	if ctx.Networking != nil && ctx.Networking.Wifi != nil {
		s.wifi = ctx.Networking.Wifi
	}
	if ctx.Recorder != nil {
		s.recorder = ctx.Recorder
	}
	return s
}

// noErr adapts a call that cannot fail.
func noErr(fn func()) search.RunNow {
	return func(context.Context) error {
		fn()
		return nil
	}
}

// toggleTitle is the title and icon of a switch, saying what choosing it
// does.
func toggleTitle(on bool, onKey, offKey, onIcon, offIcon string) (string, string) {
	if on {
		return i18n.T(offKey), offIcon
	}
	return i18n.T(onKey), onIcon
}

// toggles are the quick toggles, each saying what choosing it does now.
func (s controlSources) toggles(ctx context.Context) []search.Control {
	var out []search.Control
	if s.wallpaper != nil {
		out = append(out, search.Control{
			ID: "wallpaper", Title: i18n.T("launcher-control-wallpaper"), Keywords: "wallpaper background desktop picture theme", Icon: "ld-image-symbolic",
			Effect: search.RunOnceClosed(s.wallpaper),
		})
	}
	if s.dnd != nil {
		on := s.dnd.DND()
		title, icon := toggleTitle(on, "launcher-control-dnd-on", "launcher-control-dnd-off", "ld-bell-off-symbolic", "ld-bell-symbolic")
		out = append(out, search.Control{
			ID: "dnd", Title: title, Keywords: "dnd notifications silence quiet focus mute", Icon: icon,
			Effect: noErr(func() { s.dnd.SetDND(!on) }),
		})
	}
	if s.wifi != nil {
		on, err := s.wifi.Enabled(ctx)
		if err != nil {
			log.Printf("launcher: reading the Wi-Fi state: %v", err)
		} else {
			title, icon := toggleTitle(on, "launcher-control-wifi-on", "launcher-control-wifi-off", "ld-wifi-symbolic", "ld-wifi-off-symbolic")
			out = append(out, search.Control{
				ID: "wifi", Title: title, Keywords: "wifi wireless network internet", Icon: icon,
				Effect: search.RunNow(func(ctx context.Context) error { return s.wifi.SetEnabled(ctx, !on) }),
			})
		}
	}
	if s.bluetooth != nil {
		if st := s.bluetooth.State(); st.Available {
			title, icon := toggleTitle(st.Enabled, "launcher-control-bluetooth-on", "launcher-control-bluetooth-off", "ld-bluetooth-symbolic", "ld-bluetooth-off-symbolic")
			change := s.bluetooth.Enable
			if st.Enabled {
				change = s.bluetooth.Disable
			}
			out = append(out, search.Control{
				ID: "bluetooth", Title: title, Keywords: "bluetooth wireless headset", Icon: icon, Effect: search.RunNow(change),
			})
		}
	}
	return append(out, s.audioOutputs(ctx)...)
}

// audioOutputs offers every output but the one in use.
func (s controlSources) audioOutputs(ctx context.Context) []search.Control {
	if s.audio == nil {
		return nil
	}
	current, err := s.audio.DefaultSink(ctx)
	currentKnown := err == nil
	var out []search.Control
	for _, d := range s.audio.OutputDevices() {
		if currentKnown && d.Key == current.Key {
			continue
		}
		name := d.Description
		if name == "" {
			name = d.Name
		}
		key := d.Key
		out = append(out, search.Control{
			ID: "audio-output:" + d.Name, Title: i18n.T("launcher-control-audio", i18n.Str("device", name)),
			Subtitle: i18n.T("launcher-control-audio-subtitle"), Keywords: "sound speaker headphones output device",
			Icon: "ld-volume-2-symbolic", Effect: search.RunNow(func(ctx context.Context) error { return s.audio.SetDefault(ctx, key) }),
		})
	}
	return out
}

// mediaControls drive the active player; with none there is nothing to
// drive and no rows.
func (s controlSources) mediaControls(context.Context) []search.Control {
	if s.media == nil {
		return nil
	}
	player, ok := s.media.Active()
	if !ok {
		return nil
	}
	subtitle := player.Identity
	if player.Title != "" {
		subtitle = player.Title
		if player.Artist != "" {
			subtitle += " · " + player.Artist
		}
	}
	bus := player.BusName
	row := func(id, titleKey, keywords, icon string, do func(context.Context, string) error) search.Control {
		return search.Control{
			ID: "media-" + id, Title: i18n.T(titleKey), Subtitle: subtitle, Keywords: "media music song " + keywords, Icon: icon,
			Effect: search.RunNow(func(ctx context.Context) error { return do(ctx, bus) }),
		}
	}
	titleKey, icon := "launcher-control-play", "ld-play-symbolic"
	if player.State == mpris.StatePlaying {
		titleKey, icon = "launcher-control-pause", "ld-pause-symbolic"
	}
	out := []search.Control{row("play-pause", titleKey, "play pause resume", icon, s.media.PlayPause)}
	if player.CanGoNext {
		out = append(out, row("next", "launcher-control-next", "skip forward", "ld-skip-forward-symbolic", s.media.Next))
	}
	if player.CanGoPrevious {
		out = append(out, row("previous", "launcher-control-previous", "back", "ld-skip-back-symbolic", s.media.Previous))
	}
	return out
}

// captureControls start what looks at the screen once the panel has left
// it.
func (s controlSources) captureControls(context.Context) []search.Control {
	var out []search.Control
	if s.screenshot != nil {
		shot := func(id, titleKey, icon, mode string) search.Control {
			return search.Control{
				ID: "screenshot-" + id, Title: i18n.T(titleKey), Keywords: "capture screen grab print", Icon: icon,
				Effect: search.RunOnceClosed(func() { s.screenshot(mode, "") }),
			}
		}
		out = append(out,
			shot("region", "launcher-control-screenshot-region", "ld-camera-symbolic", screenshot.ModeRegion),
			shot("output", "launcher-control-screenshot-output", "ld-monitor-symbolic", screenshot.ModeOutput),
			shot("window", "launcher-control-screenshot-window", "ld-app-window-symbolic", screenshot.ModeWindow),
		)
	}
	if s.readScreen != nil {
		if s.ocr() {
			out = append(out, search.Control{
				ID: "screenshot-text", Title: i18n.T("launcher-control-screenshot-text"), Keywords: "ocr copy text recognize capture screen", Icon: "ld-scan-text-symbolic",
				Effect: search.RunOnceClosed(func() { s.readScreen(screenshot.ReadText) }),
			})
		}
		out = append(out, search.Control{
			ID: "screenshot-qr", Title: i18n.T("launcher-control-screenshot-qr"), Keywords: "qr code scan barcode capture screen", Icon: "ld-scan-qr-code-symbolic",
			Effect: search.RunOnceClosed(func() { s.readScreen(screenshot.ReadQR) }),
		})
	}
	if s.recorder != nil {
		if s.recorder.Snapshot().Active {
			out = append(out, search.Control{
				ID: "record", Title: i18n.T("launcher-control-record-stop"), Keywords: "screen capture video", Icon: "ld-square-symbolic",
				Effect: noErr(s.recorder.Stop),
			})
		} else {
			out = append(out, search.Control{
				ID: "record", Title: i18n.T("launcher-control-record-start"), Keywords: "screen capture video", Icon: "ld-video-symbolic",
				Effect: search.RunOnceClosed(s.recorder.Start),
			})
		}
	}
	if s.pickColor != nil {
		out = append(out, search.Control{
			ID: "pick-color", Title: i18n.T("launcher-control-pick-color"), Keywords: "eyedropper hex rgb", Icon: "ld-palette-symbolic",
			Effect: search.RunOnceClosed(s.pickColor),
		})
	}
	return out
}
