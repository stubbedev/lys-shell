package bar

import (
	"testing"

	"github.com/godbus/dbus/v5/prop"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/service/sni"
)

const green = 0xff00ff00

var (
	redPx   = render.RGBA(255, 0, 0, 255)
	bluePx  = render.RGBA(0, 0, 255, 255)
	greenPx = render.RGBA(0, 255, 0, 255)
)

// glyphName is a theme or file glyph's name, empty for a pixmap or no
// glyph.
func glyphName(g trayGlyph) string {
	if ic, ok := g.(*widget.Icon); ok {
		return ic.Name()
	}
	return ""
}

// entryOf reads the item's entry on the loop.
func entryOf(t *testing.T, m *systrayModule, key string) trayEntry {
	t.Helper()
	return onHeadlessLoop(func() trayEntry {
		e, ok := m.entries[key]
		if !ok {
			t.Fatalf("no entry %s", key)
		}
		return *e
	})
}

// rasterID is a pixmap glyph's raster.
func rasterID(t *testing.T, g trayGlyph) render.ImageID {
	t.Helper()
	p, ok := g.(*trayPixmap)
	if !ok {
		t.Fatalf("glyph %T, want a pixmap", g)
	}
	return onHeadlessLoop(func() render.ImageID { return p.ic.Still().ID })
}

// A NeedsAttention item draws its attention icon, the name before the
// pixmap as for the normal icon, over an override's icon; Active and
// Passive items draw their normal icon though they publish one.
func TestResolveTrayLookAttention(t *testing.T) {
	cfg := config.DefaultsSystray()
	normal, attention := solidPixmap(20, red), solidPixmap(20, blue)
	override := config.SystrayConfig{Overrides: []config.TrayItemOverride{{Name: "app", Icon: new("custom")}}}
	for _, tc := range []struct {
		name      string
		it        sni.Item
		cfg       config.SystrayConfig
		kind      trayIconKind
		want      string
		pixel     sni.Pixmap
		attention bool
	}{
		{"attention name", sni.Item{Status: sni.StatusNeedsAttention, IconName: "app", AttentionName: "app-alert"}, cfg, trayIconNamed, "app-alert", sni.Pixmap{}, true},
		{"attention pixmap", sni.Item{Status: sni.StatusNeedsAttention, IconPixmap: []sni.Pixmap{normal}, AttentionIconPixmap: []sni.Pixmap{attention}}, cfg, trayIconPixmap, "", attention, true},
		{"attention name before pixmap", sni.Item{Status: sni.StatusNeedsAttention, AttentionName: "app-alert", AttentionIconPixmap: []sni.Pixmap{attention}}, cfg, trayIconNamed, "app-alert", sni.Pixmap{}, true},
		{"attention over override", sni.Item{ID: "app", Status: sni.StatusNeedsAttention, IconName: "app", AttentionName: "app-alert"}, override, trayIconNamed, "app-alert", sni.Pixmap{}, true},
		{"override while active", sni.Item{ID: "app", Status: sni.StatusActive, IconName: "app", AttentionName: "app-alert"}, override, trayIconNamed, "custom", sni.Pixmap{}, false},
		{"active keeps the icon", sni.Item{Status: sni.StatusActive, IconName: "app", AttentionName: "app-alert"}, cfg, trayIconNamed, "app", sni.Pixmap{}, false},
		{"passive keeps the icon", sni.Item{Status: sni.StatusPassive, IconPixmap: []sni.Pixmap{normal}, AttentionIconPixmap: []sni.Pixmap{attention}}, cfg, trayIconPixmap, "", normal, false},
		{"no attention icon keeps the name", sni.Item{Status: sni.StatusNeedsAttention, IconName: "app"}, cfg, trayIconNamed, "app", sni.Pixmap{}, false},
		{"no attention icon keeps the pixmap", sni.Item{Status: sni.StatusNeedsAttention, IconPixmap: []sni.Pixmap{normal}}, cfg, trayIconPixmap, "", normal, false},
		{"no icon at all", sni.Item{Status: sni.StatusNeedsAttention}, cfg, trayIconFallback, trayFallbackIcon, sni.Pixmap{}, false},
	} {
		got := resolveTrayLook(tc.cfg, tc.it, 20)
		if got.icon.kind != tc.kind || got.icon.name != tc.want || got.attention != tc.attention {
			t.Errorf("%s: icon %d %q attention %v, want %d %q attention %v", tc.name, got.icon.kind, got.icon.name, got.attention, tc.kind, tc.want, tc.attention)
		}
		if tc.kind == trayIconPixmap && got.icon.pixmap.Data[1] != tc.pixel.Data[1] {
			t.Errorf("%s: drew the wrong pixmap", tc.name)
		}
		if got.hasOverlay {
			t.Errorf("%s: an overlay without OverlayIconName or OverlayIconPixmap", tc.name)
		}
	}
}

// The overlay is OverlayIconName before OverlayIconPixmap, the pixmap
// sni.BestPixmap picks for the icon's device box, whatever the status.
func TestResolveTrayLookOverlay(t *testing.T) {
	cfg := config.DefaultsSystray()
	small, big := solidPixmap(10, green), solidPixmap(20, blue)
	for _, tc := range []struct {
		name string
		it   sni.Item
		kind trayIconKind
		want string
		px   int32
	}{
		{"name", sni.Item{OverlayIconName: "badge", OverlayIconPixmap: []sni.Pixmap{small}}, trayIconNamed, "badge", 0},
		{"pixmap", sni.Item{OverlayIconPixmap: []sni.Pixmap{small, big}}, trayIconPixmap, "", 20},
		{"over attention", sni.Item{Status: sni.StatusNeedsAttention, AttentionName: "alert", OverlayIconPixmap: []sni.Pixmap{small}}, trayIconPixmap, "", 10},
	} {
		got := resolveTrayLook(cfg, tc.it, 20)
		if !got.hasOverlay || got.overlay.kind != tc.kind || got.overlay.name != tc.want || got.overlay.pixmap.Width != tc.px {
			t.Errorf("%s: overlay %v %d %q %d px, want %d %q %d px", tc.name, got.hasOverlay, got.overlay.kind, got.overlay.name, got.overlay.pixmap.Width, tc.kind, tc.want, tc.px)
		}
	}
	plain := resolveTrayLook(cfg, sni.Item{IconName: "app"}, 20)
	if plain.hasOverlay {
		t.Errorf("an item without an overlay has one: %+v", plain.overlay)
	}
	if a, b := plain.signature(), resolveTrayLook(cfg, sni.Item{IconName: "app", OverlayIconName: "badge"}, 20).signature(); a == b {
		t.Errorf("adding an overlay keeps the signature %q", a)
	}
}

// Active, then NeedsAttention, then Active again swaps the drawn
// pixmap each time, forgetting the raster it replaces, and the
// override color tints the item's own icon but not its attention icon.
func TestSystrayStatusSwitchesTheIcon(t *testing.T) {
	cfg := config.Defaults()
	cfg.Systray.Overrides = []config.TrayItemOverride{{Name: "app", Color: &config.ColorValue{Kind: config.ColorCustom, Hex: "#00ff00"}}}
	active := sni.Item{Bus: ":1.1", Path: "/a", ID: "app", Status: sni.StatusActive, IconPixmap: []sni.Pixmap{solidPixmap(20, red)}, AttentionIconPixmap: []sni.Pixmap{solidPixmap(20, blue)}}
	m, _, store := newTrayForTest(t, cfg, new(active))
	f := &forgotten{}
	onHeadlessLoop(func() bool { m.forget = f.forget; return true })
	key := active.Key()

	if c := paintTray(m, deviceScale{1, 1}); c != greenPx {
		t.Fatalf("active icon center = %#x, want the red pixmap tinted green", uint32(c))
	}
	normalID := rasterID(t, entryOf(t, m, key).icon)

	needs := active
	needs.Status = sni.StatusNeedsAttention
	store.Put(&needs)
	waitHeadless(t, "the attention pixmap", func() bool {
		p, ok := m.entries[key].icon.(*trayPixmap)
		return ok && p.pixmap.Data[3] == 0xff && p.pixmap.Data[1] == 0
	})
	if c := paintTray(m, deviceScale{1, 1}); c != bluePx {
		t.Errorf("attention icon center = %#x, want the blue attention pixmap untinted", uint32(c))
	}
	if !f.has(normalID) {
		t.Error("the normal raster was not forgotten when the attention icon replaced it")
	}
	attentionID := rasterID(t, entryOf(t, m, key).icon)

	store.Put(new(active))
	waitHeadless(t, "the normal pixmap again", func() bool {
		p, ok := m.entries[key].icon.(*trayPixmap)
		return ok && p.pixmap.Data[1] == 0xff
	})
	if c := paintTray(m, deviceScale{1, 1}); c != greenPx {
		t.Errorf("active icon center = %#x after attention, want the tinted red pixmap", uint32(c))
	}
	if !f.has(attentionID) {
		t.Error("the attention raster was not forgotten when the item went Active")
	}
}

// The overlay draws at half the icon size in its bottom-right corner,
// over the normal icon and over the attention icon alike; without one
// the icon shows whole.
func TestSystrayCompositesTheOverlay(t *testing.T) {
	base := sni.Item{Bus: ":1.1", Path: "/a", ID: "app", IconPixmap: []sni.Pixmap{solidPixmap(20, red)}, AttentionIconPixmap: []sni.Pixmap{solidPixmap(20, blue)}, OverlayIconPixmap: []sni.Pixmap{solidPixmap(20, green)}}
	attention := base
	attention.Status = sni.StatusNeedsAttention
	plain := base
	plain.OverlayIconPixmap = nil
	for _, tc := range []struct {
		name  string
		it    sni.Item
		icon  render.Color
		badge bool
	}{
		{"normal", base, redPx, true},
		{"attention", attention, bluePx, true},
		{"no overlay", plain, redPx, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newTrayForTest(t, config.Defaults(), new(tc.it))
			px := m.iconPx()
			type painted struct {
				icon          render.Rect
				corner, inner render.Color
				overlay       trayGlyph
			}
			p := onHeadlessLoop(func() painted {
				f := paintTrayFrame(m, deviceScale{1, 1})
				e := m.entries[tc.it.Key()]
				b := e.icon.Bounds()
				quarter := func(dx, dy int) render.Rect {
					return render.Rect{X: b.X + dx*b.W/2, Y: b.Y + dy*b.H/2, W: b.W / 2, H: b.H / 2}
				}
				return painted{b, f.center(quarter(1, 1)), f.center(quarter(0, 0)), e.overlay}
			})
			icon, corner, inner, overlay := p.icon, p.corner, p.inner, p.overlay
			if inner != tc.icon {
				t.Errorf("top-left quarter = %#x, want the icon %#x", uint32(inner), uint32(tc.icon))
			}
			if !tc.badge {
				if overlay != nil || corner != tc.icon {
					t.Errorf("overlay %T, bottom-right = %#x; want no overlay and the icon", overlay, uint32(corner))
				}
				return
			}
			if overlay == nil || corner != greenPx {
				t.Fatalf("overlay %T, bottom-right quarter = %#x; want the green overlay", overlay, uint32(corner))
			}
			ob := onHeadlessLoop(overlay.Bounds)
			want := render.Rect{X: icon.X + icon.W - px/2, Y: icon.Y + icon.H - px/2, W: px / 2, H: px / 2}
			if icon.W != px || ob != want {
				t.Errorf("icon %+v, overlay %+v; want a %d px icon and the overlay at %+v", icon, ob, px, want)
			}
		})
	}
}

// An item removed while it needs attention forgets its attention
// raster and its overlay raster.
func TestSystrayForgetsAnAttentionItemOnRemoval(t *testing.T) {
	it := &sni.Item{Bus: ":1.1", Path: "/a", ID: "app", Status: sni.StatusNeedsAttention, IconPixmap: []sni.Pixmap{solidPixmap(20, red)}, AttentionIconPixmap: []sni.Pixmap{solidPixmap(20, blue)}, OverlayIconPixmap: []sni.Pixmap{solidPixmap(20, green)}}
	keep := &sni.Item{Bus: ":1.2", Path: "/b", ID: "other", IconPixmap: []sni.Pixmap{solidPixmap(20, red)}}
	m, _, store, f := newPixmapTray(t, it)
	store.Put(keep)
	waitHeadless(t, "both items", func() bool { return len(m.entries) == 2 })
	e := entryOf(t, m, it.Key())
	attentionID, overlayID := rasterID(t, e.icon), rasterID(t, e.overlay)
	keepID := rasterID(t, entryOf(t, m, keep.Key()).icon)

	store.Remove(it.Bus, it.Path)
	waitHeadless(t, "the removal", func() bool { return len(m.entries) == 1 })
	if !f.has(attentionID) || !f.has(overlayID) {
		t.Errorf("forgot attention %v, overlay %v; want both", f.has(attentionID), f.has(overlayID))
	}
	if f.has(keepID) || f.count() != 2 {
		t.Errorf("forgot %d rasters, the other item's among them: %v; want just the removed item's two", f.count(), f.has(keepID))
	}
}

// A real item on a private bus: NewStatus, NewAttentionIcon and
// NewOverlayIcon each refresh what the tray draws, and a property
// changed without its signal is not drawn until one arrives.
func TestSystrayFollowsTheItemSignals(t *testing.T) {
	bus := dbustest.Start(t)
	store := sni.NewStore()
	host, err := sni.NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	conn := bus.Conn(t)
	props, err := prop.Export(conn, "/StatusNotifierItem", prop.Map{sni.ItemIface: {
		"Id":                {Value: "app", Emit: prop.EmitConst},
		"Status":            {Value: "Active", Emit: prop.EmitFalse},
		"IconName":          {Value: "app-icon", Emit: prop.EmitFalse},
		"AttentionIconName": {Value: "app-alert", Emit: prop.EmitFalse},
		"OverlayIconName":   {Value: "", Emit: prop.EmitFalse},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Object(sni.WatcherName, sni.WatcherPath).Call(sni.WatcherIface+".RegisterStatusNotifierItem", 0, "/StatusNotifierItem").Err; err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx := newTestContext(t, config.Defaults())
	ctx.SNI, ctx.Tray = store, NewTrayService(host)
	module, err := Create("systray", ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := module.(*systrayModule)
	key := conn.Names()[0] + "/StatusNotifierItem"
	drawn := func() (string, string) {
		e, ok := m.entries[key]
		if !ok {
			return "", ""
		}
		return glyphName(e.icon), glyphName(e.overlay)
	}
	waitFor := func(what, icon, overlay string, signal string, body ...any) {
		t.Helper()
		if signal != "" {
			if err := conn.Emit("/StatusNotifierItem", sni.ItemIface+"."+signal, body...); err != nil {
				t.Fatal(err)
			}
		}
		waitHeadless(t, what, func() bool {
			i, o := drawn()
			return i == icon && o == overlay
		})
	}
	waitFor("the normal icon", "app-icon", "", "")

	props.SetMust(sni.ItemIface, "Status", "NeedsAttention")
	waitFor("the attention icon on NewStatus", "app-alert", "", "NewStatus", "NeedsAttention")

	props.SetMust(sni.ItemIface, "AttentionIconName", "app-alert-2")
	if i := onHeadlessLoop(func() string { i, _ := drawn(); return i }); i != "app-alert" {
		t.Fatalf("drew %q before NewAttentionIcon, want app-alert", i)
	}
	waitFor("the new attention icon on NewAttentionIcon", "app-alert-2", "", "NewAttentionIcon")

	props.SetMust(sni.ItemIface, "OverlayIconName", "badge")
	waitFor("the overlay on NewOverlayIcon", "app-alert-2", "badge", "NewOverlayIcon")

	props.SetMust(sni.ItemIface, "Status", "Active")
	waitFor("the normal icon under the overlay on NewStatus", "app-icon", "badge", "NewStatus", "Active")
}
