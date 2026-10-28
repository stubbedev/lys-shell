package sni

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
)

// fakeMenu is a DBusMenu with one check row, a submenu, and a
// separator; it records AboutToShow and Event calls.
type fakeMenu struct {
	mu      sync.Mutex
	shown   []int32
	clicked []int32
}

func node(id int32, props map[string]dbus.Variant, children ...dbus.Variant) dbus.Variant {
	if children == nil {
		children = []dbus.Variant{}
	}
	return dbus.MakeVariant(struct {
		ID       int32
		Props    map[string]dbus.Variant
		Children []dbus.Variant
	}{id, props, children})
}

func (m *fakeMenu) GetLayout(parent, depth int32, props []string) (uint32, struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}, *dbus.Error,
) {
	root := struct {
		ID       int32
		Props    map[string]dbus.Variant
		Children []dbus.Variant
	}{0, map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, []dbus.Variant{
		node(1, map[string]dbus.Variant{"label": dbus.MakeVariant("_Mute"), "toggle-type": dbus.MakeVariant("checkmark"), "toggle-state": dbus.MakeVariant(int32(1))}),
		node(2, map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}),
		node(3, map[string]dbus.Variant{"label": dbus.MakeVariant("More"), "children-display": dbus.MakeVariant("submenu")},
			node(4, map[string]dbus.Variant{"label": dbus.MakeVariant("Quit"), "shortcut": dbus.MakeVariant([][]string{{"Control", "q"}}), "enabled": dbus.MakeVariant(false)})),
		node(5, map[string]dbus.Variant{"label": dbus.MakeVariant("Hidden"), "visible": dbus.MakeVariant(false)}),
	}}
	return 7, root, nil
}

func (m *fakeMenu) AboutToShow(id int32) (bool, *dbus.Error) {
	m.mu.Lock()
	m.shown = append(m.shown, id)
	m.mu.Unlock()
	return false, nil
}

func (m *fakeMenu) Event(id int32, event string, _ dbus.Variant, _ uint32) *dbus.Error {
	if event == "clicked" {
		m.mu.Lock()
		m.clicked = append(m.clicked, id)
		m.mu.Unlock()
	}
	return nil
}

// fakeItem is one tray item: the SNI properties plus its menu.
type fakeItem struct {
	conn  *dbus.Conn
	props *prop.Properties
	menu  *fakeMenu
}

func startItem(t *testing.T, bus *dbustest.Bus, id string) *fakeItem {
	t.Helper()
	conn := bus.Conn(t)
	it := &fakeItem{conn: conn, menu: &fakeMenu{}}
	props, err := prop.Export(conn, "/StatusNotifierItem", prop.Map{ItemIface: {
		"Id":         {Value: id, Emit: prop.EmitConst},
		"Title":      {Value: id + " title", Emit: prop.EmitTrue},
		"Status":     {Value: "Active", Emit: prop.EmitTrue},
		"IconName":   {Value: "first-icon", Emit: prop.EmitFalse},
		"ItemIsMenu": {Value: false, Emit: prop.EmitConst},
		"Menu":       {Value: dbus.ObjectPath("/MenuBar"), Emit: prop.EmitConst},
		"IconPixmap": {Value: []struct {
			W, H int32
			Data []byte
		}{{1, 1, []byte{0xff, 0xff, 0, 0}}}, Emit: prop.EmitFalse},
	}})
	if err != nil {
		t.Fatal(err)
	}
	it.props = props
	if err := conn.Export(it.menu, "/MenuBar", MenuIface); err != nil {
		t.Fatal(err)
	}
	return it
}

func (it *fakeItem) register(t *testing.T) {
	t.Helper()
	if err := it.conn.Object(WatcherName, WatcherPath).Call(WatcherIface+".RegisterStatusNotifierItem", 0, "/StatusNotifierItem").Err; err != nil {
		t.Fatalf("register: %v", err)
	}
}

func waitItems(t *testing.T, s *Store, cond func([]Item) bool) []Item {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		items := s.Items()
		if cond(items) {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; items = %+v", items)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHostAsWatcherTracksItemsAndMenus(t *testing.T) {
	bus := dbustest.Start(t)
	store := NewStore()
	host, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatalf("NewHostOn: %v", err)
	}
	defer host.Close()
	if host.watcher == nil {
		t.Fatal("no watcher on the bus: the host must take the role")
	}
	defer host.WantPixmapSize(24)()

	item := startItem(t, bus, "app")
	item.register(t)
	items := waitItems(t, store, func(i []Item) bool { return len(i) == 1 })
	got := items[0]
	if got.ID != "app" || got.Bus != item.conn.Names()[0] || got.Path != "/StatusNotifierItem" || got.MenuPath != "/MenuBar" {
		t.Fatalf("tracked = %+v", got)
	}
	if len(got.IconPixmap) != 1 || got.IconPixmap[0].Width != 1 {
		t.Errorf("pixmaps = %+v", got.IconPixmap)
	}
	// The watcher answers the property clients read.
	var regs []string
	if err := item.conn.Object(WatcherName, WatcherPath).Call(PropsIface+".Get", 0, WatcherIface, "RegisteredStatusNotifierItems").Store(&regs); err != nil || len(regs) != 1 {
		t.Errorf("RegisteredStatusNotifierItems = %v, %v", regs, err)
	}

	// NewIcon re-reads the snapshot.
	item.props.SetMust(ItemIface, "IconName", "second-icon")
	if err := item.conn.Emit("/StatusNotifierItem", ItemIface+".NewIcon"); err != nil {
		t.Fatal(err)
	}
	waitItems(t, store, func(i []Item) bool { return len(i) == 1 && i[0].IconName == "second-icon" })

	// The menu: AboutToShow, then the parsed layout.
	actions := host.Actions()
	menu, err := actions.OpenMenu(context.Background(), got)
	if err != nil {
		t.Fatalf("OpenMenu: %v", err)
	}
	if len(item.menu.shownCalls()) != 1 || item.menu.shownCalls()[0] != 0 {
		t.Errorf("AboutToShow calls = %v, want the root", item.menu.shownCalls())
	}
	// A re-read is the layout alone: AboutToShow is for showing.
	relaid, err := actions.MenuLayout(context.Background(), got)
	if err != nil {
		t.Fatalf("MenuLayout: %v", err)
	}
	if len(relaid.Children) != 4 || len(item.menu.shownCalls()) != 1 {
		t.Errorf("MenuLayout: %d children, AboutToShow calls %v; want the layout and no AboutToShow", len(relaid.Children), item.menu.shownCalls())
	}
	if len(menu.Children) != 4 {
		t.Fatalf("root children = %d, want 4", len(menu.Children))
	}
	mute, sep, more, hidden := menu.Children[0], menu.Children[1], menu.Children[2], menu.Children[3]
	if mute.Label != "_Mute" || mute.Toggle != ToggleCheckmark || mute.ToggleState != ToggleChecked || !mute.Enabled {
		t.Errorf("mute = %+v", mute)
	}
	if !sep.Separator || hidden.Visible {
		t.Errorf("separator %+v / hidden %+v", sep, hidden)
	}
	if len(more.Children) != 1 || more.Children[0].Label != "Quit" || more.Children[0].Enabled || len(more.Children[0].Shortcut) != 1 {
		t.Errorf("submenu = %+v", more.Children)
	}
	if err := actions.MenuClicked(context.Background(), got, 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		n := len(item.menu.clickedCalls())
		if n == 1 || time.Now().After(deadline) {
			if n != 1 {
				t.Error("the clicked event never arrived")
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A LayoutUpdated reaches an open menu's feed, debounced.
	feed, stop := host.WatchMenu(got.Key())
	for range 3 {
		_ = item.conn.Emit("/MenuBar", MenuIface+".LayoutUpdated", uint32(8), int32(0))
	}
	select {
	case <-feed:
	case <-time.After(2 * time.Second):
		t.Fatal("no menu tick for LayoutUpdated")
	}
	stop()
	stop()
	select {
	case _, open := <-feed:
		if open {
			t.Error("a tick after stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop left the menu feed open")
	}

	// The item's connection leaving unregisters it.
	_ = item.conn.Close()
	waitItems(t, store, func(i []Item) bool { return len(i) == 0 })
}

// An item without a menu has none to open, read or click: each call
// says so instead of calling an empty object path.
func TestMenuCallsWithoutAMenuFail(t *testing.T) {
	bus := dbustest.Start(t)
	host, err := NewHostOn(bus.Conn(t), NewStore())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	actions := host.Actions()
	bare := Item{Bus: ":1.1", Path: "/StatusNotifierItem"}
	if _, err := actions.OpenMenu(context.Background(), bare); err == nil {
		t.Error("OpenMenu on an item without a menu succeeded")
	}
	if _, err := actions.MenuLayout(context.Background(), bare); err == nil {
		t.Error("MenuLayout on an item without a menu succeeded")
	}
	if err := actions.MenuClicked(context.Background(), bare, 1); err == nil {
		t.Error("MenuClicked on an item without a menu succeeded")
	}
}

func TestHostRecoversOrphanedItems(t *testing.T) {
	bus := dbustest.Start(t)
	// An item already on the bus from an earlier watcher's era.
	item := startItem(t, bus, "orphan")
	store := NewStore()
	host, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	items := waitItems(t, store, func(i []Item) bool { return len(i) == 1 })
	if items[0].ID != "orphan" || items[0].Bus != item.conn.Names()[0] {
		t.Errorf("recovered = %+v", items[0])
	}
}

func TestHostAgainstAnExternalWatcher(t *testing.T) {
	bus := dbustest.Start(t)
	// The first host owns the watcher; a second one hosts against it.
	first, err := NewHostOn(bus.Conn(t), NewStore())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	store := NewStore()
	second, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.watcher != nil {
		t.Fatal("the second host took the watcher role from the first")
	}
	item := startItem(t, bus, "shared")
	item.register(t)
	waitItems(t, store, func(i []Item) bool { return len(i) == 1 && i[0].ID == "shared" })
}

// A bare bus name and that name at the default path are one item: the
// orphan scan's bare registration racing the item's own path-form one
// must not list it twice, in either order.
func TestWatcherRegistersTheDefaultPathOnce(t *testing.T) {
	bus := dbustest.Start(t)
	store := NewStore()
	host, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	a := startItem(t, bus, "a")
	a.register(t) // name/StatusNotifierItem
	waitItems(t, store, func(i []Item) bool { return len(i) == 1 })
	host.watcher.register(a.conn.Names()[0]) // the scan's bare name
	b := startItem(t, bus, "b")
	host.watcher.register(b.conn.Names()[0]) // bare first
	b.register(t)                            // then the path form
	waitItems(t, store, func(i []Item) bool { return len(i) == 2 })
	var regs []string
	if err := a.conn.Object(WatcherName, WatcherPath).Call(PropsIface+".Get", 0, WatcherIface, "RegisteredStatusNotifierItems").Store(&regs); err != nil || len(regs) != 2 {
		t.Errorf("registered = %v, %v; want one entry per item", regs, err)
	}
	// A different path on the same name is a second item.
	host.watcher.register(a.conn.Names()[0] + "/Other")
	host.watcher.mu.Lock()
	n := len(host.watcher.items)
	host.watcher.mu.Unlock()
	if n != 3 {
		t.Errorf("items = %d, want a third for the other path", n)
	}
}

// wirePixmap is a(iiay)'s element.
type wirePixmap struct {
	W, H int32
	Data []byte
}

// wirePixmaps publishes one size x size pixmap per size, each filled
// with fill, after a 20 x 20 one whose data is too short to draw.
func wirePixmaps(fill byte, sizes ...int32) []wirePixmap {
	out := []wirePixmap{{20, 20, []byte{1, 2, 3, 4}}}
	for _, s := range sizes {
		data := make([]byte, s*s*4)
		for i := range data {
			data[i] = fill
		}
		out = append(out, wirePixmap{s, s, data})
	}
	return out
}

var pixmapProps = []string{"IconPixmap", "AttentionIconPixmap", "OverlayIconPixmap"}

func itemPixmaps(it Item) map[string][]Pixmap {
	return map[string][]Pixmap{
		"IconPixmap":          it.IconPixmap,
		"AttentionIconPixmap": it.AttentionIconPixmap,
		"OverlayIconPixmap":   it.OverlayIconPixmap,
	}
}

// pixmapsAre reports whether every pixmap property of the one item
// holds exactly widths, each pixmap filled with fill.
func pixmapsAre(items []Item, fill byte, widths ...int32) bool {
	if len(items) != 1 {
		return false
	}
	for _, ps := range itemPixmaps(items[0]) {
		if !slices.Equal(pixmapWidths(ps), widths) {
			return false
		}
		for _, p := range ps {
			if p.Data[0] != fill {
				return false
			}
		}
	}
	return true
}

// The host keeps, of IconPixmap, AttentionIconPixmap and
// OverlayIconPixmap alike, only the pixmaps BestPixmap picks for the
// wanted sizes: none while nothing is wanted, one per distinct pick
// while several are, re-read as soon as the wanted set changes and on
// NewIcon. A pixmap too short to draw is never kept, though it is the
// closest.
func TestHostKeepsOnlyTheWantedPixmaps(t *testing.T) {
	bus := dbustest.Start(t)
	store := NewStore()
	host, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	conn := bus.Conn(t)
	all := wirePixmaps(0xaa, 16, 22, 24, 32, 48, 64)
	pm := prop.Map{ItemIface: {"Id": {Value: "app", Emit: prop.EmitConst}}}
	for _, name := range pixmapProps {
		pm[ItemIface][name] = &prop.Prop{Value: all, Emit: prop.EmitFalse}
	}
	props, err := prop.Export(conn, "/StatusNotifierItem", pm)
	if err != nil {
		t.Fatal(err)
	}
	item := &fakeItem{conn: conn, props: props}
	item.register(t)
	waitItems(t, store, func(i []Item) bool { return len(i) == 1 })
	waitPixmaps := func(fill byte, widths ...int32) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !pixmapsAre(store.Items(), fill, widths...) {
			if time.Now().After(deadline) {
				got := map[string][]int32{}
				for name, ps := range itemPixmaps(store.Items()[0]) {
					got[name] = pixmapWidths(ps)
				}
				t.Fatalf("kept %v, want %v filled with %#x", got, widths, fill)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitPixmaps(0xaa)

	release20 := host.WantPixmapSize(20)
	waitPixmaps(0xaa, 22)
	release40 := host.WantPixmapSize(40)
	waitPixmaps(0xaa, 22, 32)
	release40()
	waitPixmaps(0xaa, 22)

	for _, name := range pixmapProps {
		props.SetMust(ItemIface, name, wirePixmaps(0xbb, 16, 20, 64))
	}
	if err := conn.Emit("/StatusNotifierItem", ItemIface+".NewIcon"); err != nil {
		t.Fatal(err)
	}
	waitPixmaps(0xbb, 20)

	release20()
	waitPixmaps(0xbb)
}

func TestWantPixmapSizeRejectsNonPositive(t *testing.T) {
	h := &Host{pixmapSizes: make(map[int]int), retrack: make(chan struct{}, 1)}
	for _, px := range []int{0, -24} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WantPixmapSize(%d) did not panic", px)
				}
			}()
			h.WantPixmapSize(px)
		}()
	}
	if len(h.pixmapSizes) != 0 {
		t.Errorf("sizes = %v after rejected wants", h.pixmapSizes)
	}
}

// shownCalls and clickedCalls read the menu's call log under its lock:
// the bus handlers write it on another goroutine.
func (m *fakeMenu) shownCalls() []int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.shown)
}

func (m *fakeMenu) clickedCalls() []int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.clicked)
}
