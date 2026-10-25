package bar

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/bitwarden"
	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/recorder"
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
	dir := os.Getenv(previewDirEnv)
	btOff := bluetooth.State{Available: true, Primary: &bluetooth.Adapter{}}
	for _, p := range []struct {
		name, dropdown string
		build          func(*testing.T, *ModuleContext) widget.Widget
	}{
		{"audio", "audio", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			_, ctx.Pulse = audioFixture(t)
			return newPanelBox(audioDropdown(*ctx))
		}},
		{"battery", "battery", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Battery = newFakeBattery(presentBattery())
			return newPanelBox(batteryDropdown(*ctx))
		}},
		{"bitwarden-vault", "bitwarden", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			v := &fakeVault{status: bitwarden.Status{State: bitwarden.StateUnlocked, Items: 2}, items: bitwardenPreviewItems()}
			ctx.Bitwarden = testBitwardenHost(v, ctx.Config, &[]copied{})
			view := bitwardenDropdown(*ctx).(*bitwardenView)
			widget.SetSurfaceHost(view.Box, focusHost{})
			return newPanelBox(view)
		}},
		{"bluetooth", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			return newPanelBox(newBtDropdown(*ctx, newFakeBluetooth(btWorld())).root)
		}},
		{"bluetooth-off", "bluetooth", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			return newPanelBox(newBtDropdown(*ctx, newFakeBluetooth(btOff)).root)
		}},
		{"brightness", "brightness", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Brightness = &fakeBrightnessSource{devices: []brightness.Device{
				{Name: "intel_backlight", Type: brightness.TypeFirmware, Brightness: 50, Max: 100},
				{Name: "ddcci5", Type: brightness.TypeDDC, Brightness: 10, Max: 100},
			}}
			return newPanelBox(brightnessDropdown(*ctx))
		}},
		{"calendar", "calendar", func(_ *testing.T, ctx *ModuleContext) widget.Widget { return newPanelBox(calendarDropdown(*ctx)) }},
		{"dashboard", "dashboard", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Notifications = notifications.NewService()
			ctx.IdleInhibit = idleinhibit.NewState(0)
			ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying, CanGoNext: true})
			return newPanelBox(dashboardDropdown(*ctx))
		}},
		{"mail", "mail", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			mc := ctx.Config.Mail
			mc.Accounts = []config.MailAccount{{Name: "Work", Query: "tag:work"}, {Name: "Personal", Query: "tag:me"}}
			ctx.Mail = seededMail(t, mc, 3)
			return newPanelBox(mailDropdown(*ctx))
		}},
		{"media", "media", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			ctx.Media = newFakeMedia(mpris.Player{
				BusName: "org.mpris.MediaPlayer2.vlc", Identity: "VLC", Title: "Song", Artist: "Band", State: mpris.StatePlaying,
				Length: time.Minute, CanGoNext: true, CanSeek: true,
			})
			return newPanelBox(mediaDropdown(*ctx))
		}},
		{"network", "network", func(_ *testing.T, ctx *ModuleContext) widget.Widget {
			wifi := &fakeWifiCtl{aps: []network.AccessPoint{
				netAP("home", 80, network.SecurityWPA2, "/ap/1"), netAP("cafe", 60, network.SecurityNone, "/ap/2"),
			}}
			ctx.Network = &fakeNetworkSource{snap: network.Snapshot{
				WifiEnabled: true, HasWifi: true, Wifi: network.StatusConnected, WifiSSID: "mine", WifiStrength: 90, WifiFrequency: 5180, WifiIP4: "192.168.1.9",
			}, ticks: make(chan struct{}, 2)}
			return newPanelBox(newNetworkView(*ctx, netDeps{wifiCtl: wifi}))
		}},
		{"notification", "notification", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			svc := newNotifTestService(t)
			svc.Notify("mail", 0, "", "Mail", "<b>body</b> text", nil, 0)
			svc.Notify("chat", 0, "", "Ping", "are you there?", []string{"reply", "Reply", "default", "Open"}, 0)
			ctx.Notifications = svc
			return newPanelBox(notificationDropdown(*ctx))
		}},
		{"recorder", "recorder", func(t *testing.T, ctx *ModuleContext) widget.Widget {
			rc, _, _ := recorderTestCtx(t, []recorder.DeviceChoice{{ID: "", Label: "Automatic"}})
			ctx.Config, ctx.SetConfig, ctx.Recorder = rc.Config, rc.SetConfig, rc.Recorder
			return newPanelBox(recorderDropdown(*ctx))
		}},
		{"treeman", "treeman", func(_ *testing.T, ctx *ModuleContext) widget.Widget { return newPanelBox(treemanDropdown(*ctx)) }},
	} {
		t.Run(p.name, func(t *testing.T) {
			cfg := config.Defaults()
			ctx := styledContext(t, cfg)
			full := computeStyle(cfg, ctx.Style.palette)
			ctx.Style = &full
			data, w, h := paintPreview(ctx, p.build(t, &ctx), p.dropdown)
			if !paintsBeyondBackdrop(data) {
				t.Fatalf("the %s dropdown paints nothing over the backdrop", p.name)
			}
			if dir != "" {
				writePreview(t, filepath.Join(dir, p.name+".png"), data, w, h)
			}
		})
	}
}

// paintPreview lays out content in the registry's popover tree at the
// dropdown's configured size, anchored to a styled bar, and paints it
// over the backdrop.
func paintPreview(ctx ModuleContext, content widget.Widget, name string) ([]byte, int, int) {
	bar := widget.NewBox(widget.Row, 0, 0)
	ctx.Theme.Attach(bar)
	anchor := widget.NewBox(widget.Row, 0, 0)
	bar.Append(anchor, false)
	bar.Measure(widget.Constraints{Max: widget.Size{W: 600, H: 40}})
	bar.Arrange(render.Rect{W: 600, H: 40})
	w, h, _ := dropdownDims(name, ctx.Config)
	root, rev := dropdownTree(content, ctx.Config, w, h)
	rev.SetRevealed(true)
	rev.Finish()
	widget.SetStyleAnchor(root, anchor)
	limit := widget.Constraints{Max: widget.Size{W: 800, H: 1000}}
	sz := root.Measure(limit)
	root.Arrange(render.Rect{W: sz.W, H: sz.H})
	widget.CollectDamage(root)
	sz = root.Measure(limit)
	root.Arrange(render.Rect{W: sz.W, H: sz.H})
	data := make([]byte, render.Stride(sz.W)*sz.H)
	for i := 3; i < len(data); i += 4 {
		data[i] = previewBackdrop
	}
	root.Paint(render.New(data, render.Stride(sz.W), sz.W, sz.H))
	return data, sz.W, sz.H
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
func bitwardenPreviewItems() []bitwarden.Item {
	return []bitwarden.Item{
		{ID: "1", Type: bitwarden.ItemLogin, Name: "Example Mail", Username: "ada@example.com", Password: "pw"},
		{ID: "2", Type: bitwarden.ItemCard, Name: "Travel card", Card: bitwarden.Card{Number: "4111", Code: "123"}},
	}
}
