package bar

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-secrets/vault"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/network/secrets"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/weather"
)

// previewDirEnv names the directory TestDropdownPreviews writes each
// dropdown's PNG to; unset, the previews only paint.
const previewDirEnv = "LYS_SHELL_PREVIEW"

// previewBackdrop is the opaque black the previews composite onto, the
// harness's empty desktop.
const previewBackdrop = 0xff

// Every dropdown builds from the test fakes inside the popover frame,
// styled by the bar's theme as the registry anchors it, and paints
// something: no dropdown leaves its card blank. With LYS_SHELL_PREVIEW
// set to a directory each one is written there as a PNG, the offscreen
// check for surfaces the harness cannot fill (devices, notifications).
func TestDropdownPreviews(t *testing.T) {
	// A preview is a still: every transition lands at once.
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	dir := os.Getenv(previewDirEnv)
	for _, p := range dropdownFixtures() {
		t.Run(p.name, func(t *testing.T) {
			ctx := fixtureContext(t)
			data, w, h, _ := paintPreview(ctx, p.build(t, &ctx), p.dropdown)
			if !paintsBeyondBackdrop(data) {
				t.Fatalf("the %s dropdown paints nothing over the backdrop", p.name)
			}
			if dir != "" {
				writePreview(t, filepath.Join(dir, p.name+".png"), data, w, h)
			}
		})
	}
}

// dropdownFixture is one dropdown state built from the test fakes:
// dropdown names its registry entry (its configured size), build its
// content as its builder returns it.
type dropdownFixture struct {
	name, dropdown string
	build          func(*testing.T, *ModuleContext) widget.Widget
}

// fixtureContext is the styled context a fixture builds in, with the
// full style the registry hands its dropdowns.
func fixtureContext(t *testing.T) ModuleContext {
	t.Helper()
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	full := computeStyle(cfg, ctx.Style.palette)
	ctx.Style = &full
	return ctx
}

// dropdownFixtures is every dropdown, and the pages and prompts of the
// ones that have them, filled from the test fakes.
func dropdownFixtures() []dropdownFixture {
	btOff := bluetooth.State{Available: true, Primary: &bluetooth.Adapter{}}
	return []dropdownFixture{
		{"audio", "audio", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			_, ctx.Pulse = audioFixture(t)
			return audioDropdown(*ctx)
		}},
		{"audio-picker", "audio", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			_, ctx.Pulse = audioFixture(t)
			v := audioDropdown(*ctx).(*audioView)
			v.showPage("output")
			return v
		}},
		{"battery", "battery", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Battery = newFakeBattery(presentBattery())
			return batteryDropdown(*ctx)
		}},
		{"bitwarden-vault", "bitwarden", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			v := &fakeVault{status: vault.Status{State: vault.StateUnlocked, Items: 2}, entries: bitwardenPreviewItems()}
			ctx.Bitwarden = testBitwardenHost(v, ctx.Config, &[]copied{})
			view := bitwardenDropdown(*ctx).(*bitwardenView)
			widget.SetSurfaceHost(view.Box, focusHost{})
			return view
		}},
		{"bitwarden-login", "bitwarden", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Bitwarden = testBitwardenHost(&fakeVault{}, ctx.Config, &[]copied{})
			view := bitwardenDropdown(*ctx).(*bitwardenView)
			widget.SetSurfaceHost(view.Box, focusHost{})
			return view
		}},
		{"bluetooth", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			return newBtDropdown(*ctx, newFakeBluetooth(btWorld())).root
		}},
		{"bluetooth-passkey", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			st := btWorld()
			st.Pairing = bluetooth.RequestPasskey{Device: pathSpeaker}
			return newBtDropdown(*ctx, newFakeBluetooth(st)).root
		}},
		{"bluetooth-confirm", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			st := btWorld()
			st.Pairing = bluetooth.RequestConfirmation{Device: pathHeadset, Passkey: 42}
			return newBtDropdown(*ctx, newFakeBluetooth(st)).root
		}},
		{"bluetooth-pin", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			st := btWorld()
			st.Pairing = bluetooth.RequestPinCode{Device: pathSpeaker}
			return newBtDropdown(*ctx, newFakeBluetooth(st)).root
		}},
		{"bluetooth-off", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			return newBtDropdown(*ctx, newFakeBluetooth(btOff)).root
		}},
		{"brightness", "brightness", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Brightness = &fakeBrightnessSource{devices: []brightness.Device{
				{Name: "intel_backlight", Type: brightness.TypeFirmware, Brightness: 50, Max: 100},
				{Name: "ddcci5", Type: brightness.TypeDDC, Brightness: 10, Max: 100},
			}}
			return brightnessDropdown(*ctx)
		}},
		{"calendar", "calendar", func(_ *testing.T, ctx *ModuleContext) widget.Widget { return calendarDropdown(*ctx) }},
		{"dashboard", "dashboard", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Notifications = notifications.NewService()
			ctx.IdleInhibit = idleinhibit.NewState(idleinhibit.Duration{})
			ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying, CanGoNext: true})
			return dashboardDropdown(*ctx)
		}},
		{"mail", "mail", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			mc := ctx.Config.Mail
			mc.Accounts = []config.MailAccount{{Name: "Work", Query: "tag:work"}, {Name: "Personal", Query: "tag:me"}}
			ctx.Mail = seededMail(t, mc, 3)
			return mailDropdown(*ctx)
		}},
		{"media", "media", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Media = newFakeMedia(mpris.Player{
				BusName: "org.mpris.MediaPlayer2.vlc", Identity: "VLC", Title: "Song", Artist: "Band", State: mpris.StatePlaying,
				Length: time.Minute, CanGoNext: true, CanSeek: true,
			})
			return mediaDropdown(*ctx)
		}},
		{"media-picker", "media", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Media = newFakeMedia(
				mpris.Player{BusName: "org.mpris.MediaPlayer2.vlc", Identity: "VLC", State: mpris.StatePlaying},
				mpris.Player{BusName: "org.mpris.MediaPlayer2.mpv", Identity: "mpv"},
			)
			v := mediaDropdown(*ctx).(*mediaView)
			v.showPicker()
			return v
		}},
		{"network", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			wifi := &fakeWifiCtl{aps: []network.AccessPoint{
				netAP("home", 80, network.SecurityWPA2, "/ap/1"), netAP("cafe", 60, network.SecurityNone, "/ap/2"),
			}}
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{
				WifiEnabled: true, HasWifi: true, Wifi: network.StatusConnected, WifiSSID: "mine", WifiStrength: 90, WifiFrequency: 5180, WifiIP4: "192.168.1.9",
			}, ticks: make(chan struct{}, 2)}
			return newNetworkView(*ctx, netDeps{wifiCtl: wifi})
		}},
		{"network-password", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			wifi := &fakeWifiCtl{aps: []network.AccessPoint{netAP("home", 80, network.SecurityWPA2, "/ap/1")}}
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{WifiEnabled: true, HasWifi: true}, ticks: make(chan struct{}, 2)}
			v := newNetworkView(*ctx, netDeps{wifiCtl: wifi})
			v.attachPopover(&fakePopover{})
			v.selectNetwork("home")
			return v
		}},
		{"network-secret", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			agent := &fakeAgent{req: &secrets.Request{
				UUID: "u1", Name: "Work VPN", Setting: "vpn", Message: "Enter the code from your authenticator",
				Fields: []secrets.Field{{Key: "password", Label: "Pass", Secret: true}, {Key: "otp", Label: "Code"}},
			}}
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{}, ticks: make(chan struct{}, 2)}
			v := newNetworkView(*ctx, netDeps{agent: agent})
			v.attachPopover(&fakePopover{})
			return v
		}},
		{"network-vpn", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			vpn := &fakeVPN{entries: []network.VPN{
				{UUID: "u1", Name: "Work", State: network.VPNConnected},
				{UUID: "u2", Name: "Home", State: network.VPNFailed, Detail: "wrong username or password"},
			}}
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{HasWired: true, Wired: network.StatusConnected, WiredSpeed: 1000, WiredIP4: "10.0.0.5"}, ticks: make(chan struct{}, 2)}
			v := newNetworkView(*ctx, netDeps{vpn: vpn, vpnKinds: testKinds})
			v.apply(v.read(context.Background()))
			return v
		}},
		{"network-vpn-edit", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{}, ticks: make(chan struct{}, 2)}
			v := newNetworkView(*ctx, netDeps{vpn: &fakeVPN{}, vpnKinds: testKinds})
			v.attachPopover(&fakePopover{})
			v.vpnAdd()
			return v
		}},
		{"notification", "notification", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			svc := newNotifTestService(t)
			svc.Notify("mail", 0, "", "Mail", "<b>body</b> text", nil, 0)
			svc.Notify("chat", 0, "", "Ping", "are you there?", []string{"reply", "Reply", "default", "Open"}, 0)
			ctx.Notifications = svc
			return notificationDropdown(*ctx)
		}},
		{"recorder", "recorder", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			rc, _, _ := recorderTestCtx(t, []recorder.DeviceChoice{{ID: "", Label: "Automatic"}})
			ctx.Config, ctx.SetConfig, ctx.Recorder = rc.Config, rc.SetConfig, rc.Recorder
			return recorderDropdown(*ctx)
		}},
		{"weather", "weather", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			now := time.Now()
			hourly := make([]weather.Hourly, 8)
			for i := range hourly {
				hourly[i] = weather.Hourly{Time: now.Add(time.Duration(i) * time.Hour), Temperature: weather.Temperature(13 - i/2), Condition: weather.CondClear}
			}
			daily := make([]weather.Daily, 5)
			for i := range daily {
				daily[i] = weather.Daily{Date: now.AddDate(0, 0, i), TempLow: 11, TempHigh: weather.Temperature(18 + i%3), Condition: weather.CondCloudy}
			}
			svc := fixedWeather(&weather.Weather{
				Zone: time.Local, UpdatedAt: now, Location: weather.Location{City: "San Francisco", Region: "California"},
				Astronomy: weather.Astronomy{Sunrise: weather.TimeOfDay{Hour: 7, Minute: 28}, Sunset: weather.TimeOfDay{Hour: 18, Minute: 18}},
				Current:   weather.Current{Temperature: 13, Condition: weather.CondClear, Humidity: 62, WindSpeed: 9},
				Hourly:    hourly, Daily: daily,
			}, nil)
			t.Cleanup(svc.Close)
			ctx.Weather = svc
			waitHeadless(t, "the loaded weather", func() bool { return svc.Status().Kind == weather.Loaded })
			return weatherDropdown(*ctx)
		}},
		{"treeman", "treeman", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Treeman = &fakeTreeman{status: treemanFixture()}
			return treemanDropdown(*ctx)
		}},
		{"treeman-detail", "treeman", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Treeman = &fakeTreeman{status: treemanFixture()}
			v := treemanDropdown(*ctx).(*treemanView)
			v.openDetail("/w/feat")
			return v
		}},
	}
}

// previewMonitorH is the logical height of the monitor a preview's
// dropdown opens on.
const previewMonitorH = 1080

// paintPreview lays out the dropdown name's built content in the
// registry's popover tree as the registry opens it on a
// previewMonitorH monitor: in its panel clamped to the monitor, at the
// dropdown's configured size, measured as the popover host measures
// its content, and anchored to a styled bar. It paints the tree over
// the backdrop and returns the pixels, their size and the tree.
func paintPreview(ctx ModuleContext, content widget.Widget, name string) ([]byte, int, int, *widget.Box) {
	bar := widget.NewBox(widget.Row, 0, 0)
	ctx.Theme.Attach(bar)
	anchor := widget.NewBox(widget.Row, 0, 0)
	bar.Append(anchor, false)
	widget.SizeWithin(bar, widget.Size{W: 600, H: 40})
	bar.Arrange(render.Rect{W: 600, H: 40})
	content = dropdownPanel(name, ctx.Config, content)
	if panel, ok := content.(*panelBox); ok {
		panel.clamp(previewMonitorH)
	}
	w, _, _ := dropdownDims(name, ctx.Config)
	root, rev := dropdownTree(content, ctx.Config)
	rev.SetRevealed(true)
	rev.Finish()
	widget.SetStyleAnchor(root, anchor)
	pc := dropdownPopover(ctx.Config, anchor, root, previewMonitorH, w)
	limit := widget.Size{W: pc.MaxWidth, H: pc.MaxHeight}
	sz := widget.SizeWithinAsChild(root, limit)
	widget.ArrangeAsChild(root, render.Rect{W: sz.W, H: sz.H})
	widget.CollectDamage(root)
	sz = widget.SizeWithinAsChild(root, limit)
	widget.ArrangeAsChild(root, render.Rect{W: sz.W, H: sz.H})
	data := make([]byte, render.Stride(sz.W)*sz.H)
	for i := 3; i < len(data); i += 4 {
		data[i] = previewBackdrop
	}
	root.Paint(render.New(data, render.Stride(sz.W), sz.W, sz.H))
	return data, sz.W, sz.H, root
}

// paintsBeyondBackdrop reports whether any pixel differs from the
// opaque black backdrop.
func paintsBeyondBackdrop(data []byte) bool {
	for i := 0; i+3 < len(data); i += 4 {
		if data[i] != 0 || data[i+1] != 0 || data[i+2] != 0 {
			return true
		}
	}
	return false
}

// writePreview encodes premultiplied BGRA pixels as a PNG.
func writePreview(t *testing.T, path string, data []byte, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stride := render.Stride(w)
	for y := range h {
		for x := range w {
			s, d := y*stride+x*4, y*img.Stride+x*4
			img.Pix[d], img.Pix[d+1], img.Pix[d+2], img.Pix[d+3] = data[s+2], data[s+1], data[s], data[s+3]
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// bitwardenPreviewItems is a small open vault: a login and a card.
func bitwardenPreviewItems() []fakeEntry {
	return []fakeEntry{
		entry(vault.Item{ID: "1", Type: vault.ItemLogin, Name: "Example Mail", Username: "ada@example.com"}, map[vault.Field]string{vault.FieldUsername: "ada@example.com", vault.FieldPassword: "pw"}),
		entry(vault.Item{ID: "2", Type: vault.ItemCard, Name: "Travel card"}, map[vault.Field]string{vault.FieldCardNumber: "4111", vault.FieldCardCode: "123"}),
	}
}
