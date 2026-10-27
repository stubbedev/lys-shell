package bar

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// fakeTray records the tray calls and serves a scripted menu.
type fakeTray struct {
	mu     sync.Mutex
	calls  []string
	menu   sni.MenuItem
	err    error
	wanted map[int]int
}

func (f *fakeTray) record(s string) error {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
	return nil
}

func (f *fakeTray) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeTray) Activate(_ context.Context, it sni.Item) error {
	return f.record("activate " + it.ID)
}

func (f *fakeTray) SecondaryActivate(_ context.Context, it sni.Item) error {
	return f.record("secondary " + it.ID)
}

func (f *fakeTray) ContextMenu(_ context.Context, it sni.Item) error {
	return f.record("context-menu " + it.ID)
}

func (f *fakeTray) OpenMenu(_ context.Context, it sni.Item) (sni.MenuItem, error) {
	return f.serveMenu("open-menu " + it.ID)
}

func (f *fakeTray) MenuLayout(_ context.Context, it sni.Item) (sni.MenuItem, error) {
	return f.serveMenu("menu-layout " + it.ID)
}

func (f *fakeTray) SubmenuOpening(_ context.Context, it sni.Item, id int32) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "submenu "+it.ID+" "+strconv.Itoa(int(id)))
	return false, f.err
}

func (f *fakeTray) serveMenu(call string) (sni.MenuItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.menu, f.err
}

func (f *fakeTray) setMenu(menu sni.MenuItem) {
	f.mu.Lock()
	f.menu = menu
	f.mu.Unlock()
}

func (f *fakeTray) MenuClicked(_ context.Context, it sni.Item, id int32) error {
	return f.record("clicked " + it.ID + " " + strconv.Itoa(int(id)))
}

func (f *fakeTray) WatchMenu(string) (<-chan struct{}, func()) {
	feed := make(chan struct{})
	return feed, sync.OnceFunc(func() { close(feed) })
}

func (f *fakeTray) WantPixmapSize(px int) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wanted == nil {
		f.wanted = make(map[int]int)
	}
	f.wanted[px]++
	return sync.OnceFunc(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.wanted[px]--; f.wanted[px] == 0 {
			delete(f.wanted, px)
		}
	})
}

// wantedSizes lists the pixmap sizes wanted now, ascending.
func (f *fakeTray) wantedSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.wanted))
}

func waitTray(t *testing.T, f *fakeTray, n int) []string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(f.Calls()) < n && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	return f.Calls()
}

// openTrayMenu right-clicks the item on the loop and waits for its
// menu to open.
func openTrayMenu(t *testing.T, m *systrayModule, key string) {
	t.Helper()
	onHeadlessLoop(func() bool { m.rightClick(key); return true })
	waitHeadless(t, "the menu of "+key, func() bool { return m.menu != nil && m.menu.key == key })
}

func newTrayForTest(t *testing.T, cfg *config.Config, items ...*sni.Item) (*systrayModule, *fakeTray, *sni.Store) {
	t.Helper()
	store := sni.NewStore()
	for _, it := range items {
		store.Put(it)
	}
	tray := &fakeTray{}
	ctx := newTestContext(t, cfg)
	ctx.SNI, ctx.Tray = store, tray
	module, err := Create("systray", ctx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return module.(*systrayModule), tray, store
}

func TestSystrayPatternsAreCaseSensitiveGlobs(t *testing.T) {
	it := sni.Item{ID: "com.discord.Discord", Title: "Discord"}
	if !systrayBlacklisted([]string{"*discord*"}, it) {
		t.Error("*discord* should match the id")
	}
	if !systrayBlacklisted([]string{"Discord"}, it) {
		t.Error("an exact title should match")
	}
	if systrayBlacklisted([]string{"*DISCORD*"}, it) {
		t.Error("patterns are case-sensitive")
	}
	o, ok := systrayOverride([]config.TrayItemOverride{{Name: "nope"}, {Name: "*Discord", Icon: new("x")}}, it)
	if !ok || o.Icon == nil || *o.Icon != "x" {
		t.Errorf("override = %+v, %v", o, ok)
	}
}

// solidPixmap is a size x size ARGB32 pixmap of one opaque color.
func solidPixmap(size int32, argb uint32) sni.Pixmap {
	data := make([]byte, size*size*4)
	for i := 0; i < len(data); i += 4 {
		data[i], data[i+1], data[i+2], data[i+3] = byte(argb>>24), byte(argb>>16), byte(argb>>8), byte(argb)
	}
	return sni.Pixmap{Width: size, Height: size, Data: data}
}

// The tray draws the pixmap sni.BestPixmap picks for its icon box in
// device pixels, the size it asks the host to keep: at 1x the 20 px
// box picks the 22 px pixmap the old fixed 24 px target missed for
// the 24 px one, at 1.5x the 30 px box the 32 px one, and at 3x the
// 60 px box the 64 px one.
func TestSystrayDrawsTheSharedPick(t *testing.T) {
	sizes := []int32{16, 22, 24, 32, 48, 64}
	var pixmaps []sni.Pixmap
	for _, s := range sizes {
		pixmaps = append(pixmaps, solidPixmap(s, 0xff000000|uint32(s)))
	}
	it := sni.Item{IconPixmap: pixmaps}
	cfg := config.DefaultsSystray()
	for _, tc := range []struct {
		scale deviceScale
		want  int32
	}{{deviceScale{1, 1}, 22}, {deviceScale{3, 2}, 32}, {deviceScale{3, 1}, 64}} {
		device := tc.scale.box(20)
		shared, _ := sni.BestPixmap(pixmaps, device)
		got := resolveTrayLook(cfg, it, device).icon
		if got.kind != trayIconPixmap || got.pixmap.Width != shared.Width || got.pixmap.Width != tc.want {
			t.Errorf("at %v: drew %dx%d, sni.BestPixmap picks %d, want %d", tc.scale, got.pixmap.Width, got.pixmap.Height, shared.Width, tc.want)
		}
	}
}

// trayFrame is the tray painted on a canvas at scale.
type trayFrame struct {
	data  []byte
	w     int
	scale deviceScale
}

// center is the pixel at the middle of r, in logical pixels.
func (f trayFrame) center(r render.Rect) render.Color {
	x, y := (2*r.X+r.W)*f.scale.num/(2*f.scale.denom), (2*r.Y+r.H)*f.scale.num/(2*f.scale.denom)
	return pixelAt(f.data, f.w, x, y)
}

// paintTrayFrame lays the tray out and paints it on a canvas at scale.
// It runs on the loop.
func paintTrayFrame(m *systrayModule, scale deviceScale) trayFrame {
	const w, h = 200, 40
	widget.SizeWithin(m.root, widget.Size{W: w, H: h})
	m.root.Arrange(render.Rect{W: w, H: h})
	dw, dh := scale.box(w), scale.box(h)
	data := make([]byte, render.Stride(dw)*dh)
	m.root.Paint(render.NewScaled(data, render.Stride(dw), dw, dh, scale.num, scale.denom))
	return trayFrame{data: data, w: dw, scale: scale}
}

// paintTray paints the tray at scale, returning the pixel at the first
// item's icon center.
func paintTray(m *systrayModule, scale deviceScale) render.Color {
	return onHeadlessLoop(func() render.Color {
		f := paintTrayFrame(m, scale)
		var b render.Rect
		for _, e := range m.entries {
			b = e.icon.Bounds()
		}
		return f.center(b)
	})
}

// forgotten records the rasters a tray forgets.
type forgotten struct {
	mu  sync.Mutex
	ids []render.ImageID
}

func (f *forgotten) forget(id render.ImageID) {
	f.mu.Lock()
	f.ids = append(f.ids, id)
	f.mu.Unlock()
}

func (f *forgotten) has(id render.ImageID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.ids, id)
}

func (f *forgotten) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ids)
}

func newPixmapTray(t *testing.T, it *sni.Item) (*systrayModule, *fakeTray, *sni.Store, *forgotten) {
	t.Helper()
	m, tray, store := newTrayForTest(t, config.Defaults(), it)
	f := &forgotten{}
	onHeadlessLoop(func() bool { m.forget = f.forget; return true })
	return m, tray, store, f
}

func pixmapGlyph(t *testing.T, m *systrayModule, key string) *trayPixmap {
	t.Helper()
	return onHeadlessLoop(func() *trayPixmap {
		e, ok := m.entries[key]
		if !ok {
			t.Fatalf("no entry %s", key)
		}
		g, ok := e.icon.(*trayPixmap)
		if !ok {
			t.Fatalf("%s: icon %T, want a pixmap", key, e.icon)
		}
		return g
	})
}

const (
	red  = 0xffff0000
	blue = 0xff0000ff
)

// A scale change re-rasterizes the drawn pixmap at the new scale in the
// same frame, so the icon never goes blank, asks the host for the new
// device size instead of the old, and the store's next snapshot swaps
// in the pixmap picked for it, forgetting each replaced raster.
func TestSystrayScaleChangeRepicksWithoutABlankIcon(t *testing.T) {
	it := &sni.Item{Bus: ":1.1", Path: "/a", ID: "app", IconPixmap: []sni.Pixmap{solidPixmap(20, red), solidPixmap(40, blue)}}
	m, tray, store, f := newPixmapTray(t, it)
	if got := tray.wantedSizes(); !slices.Equal(got, []int{20}) {
		t.Fatalf("wanted %v at 1x, want [20]", got)
	}
	if c := paintTray(m, deviceScale{1, 1}); c != render.RGBA(255, 0, 0, 255) {
		t.Fatalf("1x icon center = %#x, want the 20 px red pixmap", uint32(c))
	}
	first := pixmapGlyph(t, m, it.Key())
	firstID := onHeadlessLoop(func() render.ImageID { return first.ic.Still().ID })

	if c := paintTray(m, deviceScale{2, 1}); c != render.RGBA(255, 0, 0, 255) {
		t.Fatalf("2x icon center = %#x right after the scale change, want the red pixmap re-rasterized, not blank", uint32(c))
	}
	if !f.has(firstID) {
		t.Error("the 1x raster was not forgotten when the 2x one replaced it")
	}
	if got := tray.wantedSizes(); !slices.Equal(got, []int{40}) {
		t.Fatalf("wanted %v at 2x, want [40] alone", got)
	}
	second := onHeadlessLoop(func() render.ImageID { return first.ic.Still().ID })

	store.Put(it)
	waitHeadless(t, "the 40 px pixmap", func() bool {
		g, ok := m.entries[it.Key()].icon.(*trayPixmap)
		return ok && g.pixmap.Width == 40
	})
	if c := paintTray(m, deviceScale{2, 1}); c != render.RGBA(0, 0, 255, 255) {
		t.Errorf("2x icon center = %#x after the refresh, want the 40 px blue pixmap", uint32(c))
	}
	if !f.has(second) {
		t.Error("the replaced glyph's raster was not forgotten")
	}
}

// The same scale draws the same raster: nothing is re-rasterized or
// forgotten from frame to frame.
func TestSystrayPixmapRasterIsStableAcrossFrames(t *testing.T) {
	it := &sni.Item{Bus: ":1.1", Path: "/a", ID: "app", IconPixmap: []sni.Pixmap{solidPixmap(20, red)}}
	m, _, _, f := newPixmapTray(t, it)
	paintTray(m, deviceScale{1, 1})
	g := pixmapGlyph(t, m, it.Key())
	id := onHeadlessLoop(func() render.ImageID { return g.ic.Still().ID })
	paintTray(m, deviceScale{1, 1})
	if got := onHeadlessLoop(func() render.ImageID { return g.ic.Still().ID }); got != id || f.count() != 0 {
		t.Errorf("raster %d -> %d, %d forgotten; want one raster kept", id, got, f.count())
	}
}

// A removed item's raster is forgotten, and so are the rest when the
// module retires, which also releases its wanted size.
func TestSystrayForgetsRemovedAndRetiredRasters(t *testing.T) {
	a := &sni.Item{Bus: ":1.1", Path: "/a", ID: "a", IconPixmap: []sni.Pixmap{solidPixmap(20, red)}}
	b := &sni.Item{Bus: ":1.2", Path: "/b", ID: "b", IconPixmap: []sni.Pixmap{solidPixmap(20, blue)}}
	m, tray, store, f := newPixmapTray(t, a)
	store.Put(b)
	waitHeadless(t, "both items", func() bool { return len(m.entries) == 2 })
	idA := onHeadlessLoop(func() render.ImageID { return m.entries[a.Key()].icon.(*trayPixmap).ic.Still().ID })
	idB := onHeadlessLoop(func() render.ImageID { return m.entries[b.Key()].icon.(*trayPixmap).ic.Still().ID })

	store.Remove(a.Bus, a.Path)
	waitHeadless(t, "a's removal", func() bool { return len(m.entries) == 1 })
	if !f.has(idA) {
		t.Error("the removed item's raster was not forgotten")
	}
	if f.has(idB) {
		t.Error("the remaining item's raster was forgotten")
	}

	onHeadlessLoop(func() bool { m.ctx.gen.retire(); return true })
	deadline := time.Now().Add(2 * time.Second)
	for (!f.has(idB) || len(tray.wantedSizes()) != 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !f.has(idB) || len(tray.wantedSizes()) != 0 {
		t.Errorf("after retire: forgot b %v, wanted %v; want the raster forgotten and no size wanted", f.has(idB), tray.wantedSizes())
	}
}

func TestResolveTrayIcon(t *testing.T) {
	cfg := config.DefaultsSystray()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app-icon.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	pixmap := sni.Pixmap{Width: 1, Height: 1, Data: []byte{0xff, 0xff, 0, 0}}
	for _, tc := range []struct {
		name string
		it   sni.Item
		cfg  config.SystrayConfig
		kind trayIconKind
		want string
	}{
		{"theme path file", sni.Item{IconName: "app-icon", IconThemePath: dir}, cfg, trayIconFile, filepath.Join(dir, "app-icon.png")},
		{"named", sni.Item{IconName: "nm-applet"}, cfg, trayIconNamed, "nm-applet"},
		{"file path", sni.Item{IconName: filepath.Join(dir, "app-icon.png")}, cfg, trayIconFile, filepath.Join(dir, "app-icon.png")},
		{"pixmap", sni.Item{IconPixmap: []sni.Pixmap{pixmap}}, cfg, trayIconPixmap, ""},
		{"fallback", sni.Item{}, cfg, trayIconFallback, trayFallbackIcon},
		{"override icon wins", sni.Item{ID: "app", IconName: "nm-applet"}, config.SystrayConfig{Overrides: []config.TrayItemOverride{{Name: "app", Icon: new("custom")}}}, trayIconNamed, "custom"},
	} {
		got := resolveTrayLook(tc.cfg, tc.it, 20).icon
		if got.kind != tc.kind || (tc.want != "" && got.name != tc.want) {
			t.Errorf("%s: = %+v, want %d %q", tc.name, got, tc.kind, tc.want)
		}
	}
	hooks := trayPixmapHooks{scaled: func(deviceScale) {}, forget: func(render.ImageID) {}}
	// A pixmap icon is a raster at the icon size.
	icon := buildTrayIcon(trayIconSource{kind: trayIconPixmap, pixmap: pixmap}, 20, deviceScale{1, 1}, hooks)
	if sz := widget.SizeWithin(icon, widget.Size{W: 100, H: 100}); sz.W != 20 {
		t.Errorf("pixmap icon = %v, want 20px", sz)
	}
	// A short pixmap is the fallback glyph, not a crash.
	short := buildTrayIcon(trayIconSource{kind: trayIconPixmap, pixmap: sni.Pixmap{Width: 4, Height: 4}}, 20, deviceScale{1, 1}, hooks)
	if icon, ok := short.(*widget.Icon); !ok || icon.Name() != trayFallbackIcon {
		t.Errorf("short pixmap = %T, want the fallback glyph", short)
	}
}

func TestSystrayClicksDriveTheItem(t *testing.T) {
	cfg := config.Defaults()
	plain := &sni.Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "plain"}
	menuOnly := &sni.Item{Bus: ":1.2", Path: "/StatusNotifierItem", ID: "menu-only", ItemIsMenu: true}
	m, tray, _ := newTrayForTest(t, cfg, plain, menuOnly)
	if got := len(m.root.Children()); got != 2 {
		t.Fatalf("buttons = %d, want 2", got)
	}
	m.entries[plain.Key()].button.ClickAt(widget.Point{})
	m.entries[menuOnly.Key()].button.ClickAt(widget.Point{})
	router, p := treetest.Pointer(m.root, m.entries[plain.Key()].button, 500, 40)
	router.Press(widget.BTNMiddle, p, 0)
	calls := waitTray(t, tray, 3)
	want := map[string]bool{"activate plain": true, "context-menu menu-only": true, "secondary plain": true}
	for _, c := range calls {
		if !want[c] {
			t.Errorf("unexpected call %q", c)
		}
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing %v", want)
	}
}

func TestSystrayMenuRows(t *testing.T) {
	cfg := config.Defaults()
	it := &sni.Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "app", MenuPath: "/MenuBar"}
	m, tray, _ := newTrayForTest(t, cfg, it)
	nodes := []sni.MenuItem{
		{ID: 9, Separator: true, Visible: true}, // a leading separator drops
		{ID: 1, Label: "_Mute", Visible: true, Enabled: true, Toggle: sni.ToggleCheckmark, ToggleState: sni.ToggleChecked},
		{ID: 2, Separator: true, Visible: true},
		{ID: 3, Separator: true, Visible: true}, // doubled separators collapse
		{ID: 4, Label: "More", Visible: true, Enabled: true, Children: []sni.MenuItem{
			{ID: 5, Label: "Quit", Visible: true, Enabled: false, Shortcut: [][]string{{"Control", "q"}}},
		}},
		{ID: 6, Label: "Hidden", Visible: false, Enabled: true},
		{ID: 7, Separator: true, Visible: true}, // a trailing separator drops
	}
	rows := m.menuRows(*it, nodes)
	if len(rows) != 3 {
		t.Fatalf("rows = %d (%+v), want mute, separator, more", len(rows), rows)
	}
	if rows[0].Label != "Mute" || rows[0].Kind != widget.ItemCheck || !rows[0].Checked {
		t.Errorf("mute = %+v", rows[0])
	}
	if rows[1].Kind != widget.ItemSeparator {
		t.Errorf("row 1 = %+v, want the section separator", rows[1])
	}
	quit := rows[2].Items[0]
	if !quit.Disabled || quit.Accel != "Ctrl+q" {
		t.Errorf("quit = %+v, want disabled with Ctrl+q", quit)
	}
	rows[0].OnClick()
	if calls := waitTray(t, tray, 1); len(calls) != 1 || calls[0] != "clicked app 1" {
		t.Errorf("calls = %v, want the clicked event for id 1", calls)
	}
}

func TestSystrayRightClickFallsBackWithoutAMenu(t *testing.T) {
	cfg := config.Defaults()
	it := &sni.Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "bare"}
	m, tray, _ := newTrayForTest(t, cfg, it)
	// An empty menu: the item's own ContextMenu.
	onHeadlessLoop(func() bool { m.rightClick(it.Key()); return true })
	if calls := waitTray(t, tray, 2); len(calls) != 2 || calls[0] != "open-menu bare" || calls[1] != "context-menu bare" {
		t.Errorf("calls = %v, want the context-menu fallback", calls)
	}
	if onHeadlessLoop(func() bool { return m.menu != nil }) {
		t.Error("an empty menu opened")
	}
	// A real menu builds its rows (headless: no popover).
	tray.setMenu(sni.MenuItem{Children: []sni.MenuItem{{ID: 1, Label: "Open", Visible: true, Enabled: true}}})
	openTrayMenu(t, m, it.Key())
	if onHeadlessLoop(func() int { return len(m.menu.items) }) != 1 {
		t.Error("a published menu built no rows")
	}
}

func TestSystrayFollowsTheStore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Systray.Blacklist = []string{"hidden*"}
	m, _, store := newTrayForTest(t, cfg)
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { m.setChrome(chrome); return true })
	if chrome.Visible() {
		t.Error("an empty tray should hide its module wrapper")
	}
	store.Put(&sni.Item{Bus: ":1.1", Path: "/a", ID: "shown", IconName: "one"})
	store.Put(&sni.Item{Bus: ":1.2", Path: "/b", ID: "hidden-app"})
	waitHeadless(t, "the one button not blacklisted", func() bool { return len(m.root.Children()) == 1 })
	if !onHeadlessLoop(chrome.Visible) {
		t.Fatal("a tray with a button should show its module wrapper")
	}
	if !onHeadlessLoop(m.root.Visible) {
		t.Error("the item box itself stays visible, as in Rust")
	}
}

func TestLoadFileAppliesSystray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	good := "modules:\n  systray:\n    icon-scale: 1.5\n    item-gap: 6px\n    blacklist: [\"*steam*\"]\n" +
		"    overrides:\n      - name: \"*discord*\"\n        icon: discord-symbolic\n        color: red\n"
	if err := osWrite(path, good); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	s := c.Systray
	if s.IconScale.Value != 1.5 || s.ItemGap.Unit != config.SizePixels || len(s.Blacklist) != 1 {
		t.Errorf("systray = %+v", s)
	}
	if len(s.Overrides) != 1 || s.Overrides[0].Icon == nil || *s.Overrides[0].Icon != "discord-symbolic" || s.Overrides[0].Color == nil {
		t.Errorf("overrides = %+v", s.Overrides)
	}
	for _, bad := range []string{
		"modules:\n  systray:\n    overrides:\n      - icon: x\n",
		"modules:\n  systray:\n    overrides:\n      - name: x\n        color: bogus\n",
	} {
		if err := osWrite(path, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Errorf("%q: want a load error", bad)
		}
	}
}

// fakeAccels is an application accelerator table.
type fakeAccels struct {
	bound map[string]widget.Activatable
	taken map[string]bool
}

func newFakeAccels() *fakeAccels {
	return &fakeAccels{bound: map[string]widget.Activatable{}, taken: map[string]bool{}}
}

func (f *fakeAccels) AddAccel(keys string, action widget.Activatable) error {
	if f.taken[keys] || f.bound[keys] != nil {
		return errors.New("taken")
	}
	f.bound[keys] = action
	return nil
}

func (f *fakeAccels) RemoveAccel(keys string) error {
	if f.bound[keys] == nil {
		return errors.New("not bound")
	}
	delete(f.bound, keys)
	return nil
}

func (f *fakeAccels) press(keys string) bool {
	a, ok := f.bound[keys]
	if ok {
		a.Activate()
	}
	return ok
}

// TestSystrayMenuShortcutsFire pins register_accelerators: an open
// menu's enabled leaves fire their clicked event on their shortcut, a
// disabled or hidden one does not, a rebuilt menu rebinds, and a taken
// key is skipped without losing the rest.
func TestSystrayMenuShortcutsFire(t *testing.T) {
	cfg := config.Defaults()
	it := &sni.Item{Bus: ":1.1", Path: "/StatusNotifierItem", ID: "app", MenuPath: "/MenuBar"}
	m, tray, _ := newTrayForTest(t, cfg, it)
	accels := newFakeAccels()
	accels.taken["<Alt>x"] = true
	m.accels = accels
	menu := sni.MenuItem{Children: []sni.MenuItem{
		{ID: 1, Label: "Open", Visible: true, Enabled: true, Shortcut: [][]string{{"Control", "o"}}},
		{ID: 2, Label: "More", Visible: true, Enabled: true, Children: []sni.MenuItem{
			{ID: 3, Label: "Quit", Visible: true, Enabled: true, Shortcut: [][]string{{"Control", "Shift", "q"}}},
			{ID: 4, Label: "Off", Visible: true, Enabled: false, Shortcut: [][]string{{"Control", "d"}}},
		}},
		{ID: 5, Label: "Gone", Visible: false, Enabled: true, Shortcut: [][]string{{"Control", "g"}}},
		{ID: 6, Label: "Taken", Visible: true, Enabled: true, Shortcut: [][]string{{"Alt", "x"}}},
	}}
	tray.setMenu(menu)
	openTrayMenu(t, m, it.Key())
	if !accels.press("<Control><Shift>q") {
		t.Fatal("the nested Quit shortcut is not bound")
	}
	if calls := waitTray(t, tray, 2); len(calls) != 2 || calls[1] != "clicked app 3" {
		t.Errorf("calls = %v, want the clicked event for id 3", calls)
	}
	for _, keys := range []string{"<Control>d", "<Control>g", "<Alt>x"} {
		if _, ok := accels.bound[keys]; ok && keys != "<Alt>x" {
			t.Errorf("%s bound: a disabled or hidden row took a shortcut", keys)
		}
	}
	if accels.bound["<Control>o"] == nil {
		t.Error("a taken key cost the other bindings")
	}

	// A rebuilt menu drops what it no longer has.
	m.bindAccels(*it, menu.Children[:1])
	if accels.bound["<Control><Shift>q"] != nil || accels.bound["<Control>o"] == nil {
		t.Errorf("after the rebuild: %v", accels.bound)
	}
	m.unbindAccels()
	if len(accels.bound) != 0 {
		t.Errorf("after close: %v still bound", accels.bound)
	}
	for _, s := range [][]string{{"Control", "Shift", "q"}, {"Alt", "F5"}} {
		if _, err := app.ParseAccel(trayAccelSpec([][]string{s})); err != nil {
			t.Errorf("lys-ui cannot parse %q: %v", trayAccelSpec([][]string{s}), err)
		}
	}
	if trayAccelSpec([][]string{{"Hyper", "Super", "F5"}}) != "<Super>F5" || trayAccelSpec(nil) != "" {
		t.Error("trayAccelSpec: unknown modifiers drop, an empty shortcut is none")
	}
}

// A tray menu item's icon name resolves through GTK's dash fallbacks,
// as the GTK menu's GThemedIcon does.
func TestTrayMenuIconFallsBackAlongTheName(t *testing.T) {
	treetest.IconTheme(t, "org.example")
	if ink := treetest.Ink(trayMenuIcon(sni.MenuItem{IconName: "org.example-app"}, 24)); ink == 0 {
		t.Error("a menu icon with a present dash-shortened form drew nothing")
	}
}

// A tray icon sits in the middle of the bar's height, as the module
// icons beside it do, while its slot keeps the icon size instead of
// stretching to the bar.
func TestSystrayIconIsVerticallyCentered(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	named := &sni.Item{Bus: ":1.1", Path: "/a", ID: "named", IconName: "steam"}
	pixmap := &sni.Item{Bus: ":1.2", Path: "/b", ID: "pixmap", IconPixmap: []sni.Pixmap{{Width: 1, Height: 1, Data: []byte{0xff, 0xff, 0, 0}}}}
	store := sni.NewStore()
	store.Put(named)
	store.Put(pixmap)
	ctx.SNI, ctx.Tray = store, &fakeTray{}
	ctx.Attachers = &[]interface{ Attach(app.Host) }{}
	layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "clock"}, {Module: "systray"}}}
	_, h, _ := paintBar(t, ctx, layout, 800)
	var m *systrayModule
	for _, a := range *ctx.Attachers {
		if s, ok := a.(*systrayModule); ok {
			m = s
		}
	}
	if m == nil || len(m.entries) != 2 {
		t.Fatalf("tray module = %v, want one with two items", m)
	}
	px := m.iconPx()
	for _, key := range []string{named.Key(), pixmap.Key()} {
		e := m.entries[key]
		btn, slot, icon := e.button.Bounds(), e.slot.Bounds(), e.icon.Bounds()
		if btn.H <= px {
			t.Fatalf("%s: button height %d leaves no room to center a %dpx icon (bar %d)", key, btn.H, px, h)
		}
		if slot.W != px || slot.H != px {
			t.Errorf("%s: slot = %+v, want %dx%d", key, slot, px, px)
		}
		if d := (2*icon.Y + icon.H) - (2*btn.Y + btn.H); d < -1 || d > 1 {
			t.Errorf("%s: icon %+v is not centered in its button %+v", key, icon, btn)
		}
	}
}
