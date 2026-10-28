package bar

import (
	"slices"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/sni"
)

const (
	echoItemPath = "/StatusNotifierItem"
	echoMenuPath = "/MenuBar"
)

// echoMenuNode is a DBusMenu layout node, (ia{sv}av).
type echoMenuNode struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

// echoMenu is a DBusMenu of numbered rows. With echo set it is an app
// that rebuilds its menu on demand: every AboutToShow adds a row and
// sends LayoutUpdated, as a menu that grows each time it is asked to
// show. With subRows set, row 1 is a submenu of that many rows, which
// its own AboutToShow grows by one and answers with needUpdate (no
// signal). It counts its AboutToShow and GetLayout calls and records
// the ids shown.
type echoMenu struct {
	conn    *dbus.Conn
	echo    bool
	rows    dbustest.Var[int32]
	subRows dbustest.Var[int32]
	shown   dbustest.Var[int]
	shownID dbustest.Var[[]int32]
	reads   dbustest.Var[int]
}

func (m *echoMenu) AboutToShow(id int32) (bool, *dbus.Error) {
	m.shown.Update(func(n int) int { return n + 1 })
	m.shownID.Update(func(ids []int32) []int32 { return append(slices.Clone(ids), id) })
	if id != 0 && m.subRows.Load() > 0 {
		m.subRows.Update(func(n int32) int32 { return n + 1 })
		return true, nil
	}
	if !m.echo {
		return false, nil
	}
	if err := m.relayout(func(n int32) int32 { return n + 1 }); err != nil {
		return false, dbus.MakeFailedError(err)
	}
	return true, nil
}

// relayout changes the rows and sends LayoutUpdated, as an app whose
// menu changes on its own.
func (m *echoMenu) relayout(rows func(int32) int32) error {
	n := m.rows.Update(rows)
	return m.conn.Emit(echoMenuPath, sni.MenuIface+".LayoutUpdated", uint32(n), int32(0))
}

func (m *echoMenu) GetLayout(int32, int32, []string) (uint32, echoMenuNode, *dbus.Error) {
	m.reads.Update(func(n int) int { return n + 1 })
	n := m.rows.Load()
	rows := make([]dbus.Variant, 0, n)
	for id := range n {
		label := "row " + strconv.Itoa(int(id))
		node := echoMenuNode{id + 1, map[string]dbus.Variant{"label": dbus.MakeVariant(label)}, []dbus.Variant{}}
		if sub := m.subRows.Load(); id == 0 && sub > 0 {
			node.Props["children-display"] = dbus.MakeVariant("submenu")
			for sid := range sub {
				node.Children = append(node.Children, dbus.MakeVariant(echoMenuNode{100 + sid, map[string]dbus.Variant{"label": dbus.MakeVariant("sub " + strconv.Itoa(int(sid)))}, []dbus.Variant{}}))
			}
		}
		rows = append(rows, dbus.MakeVariant(node))
	}
	return uint32(n), echoMenuNode{0, map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, rows}, nil
}

func (m *echoMenu) Event(int32, string, dbus.Variant, uint32) *dbus.Error { return nil }

// startEchoTray runs the tray module on the live SNI host over a
// private bus, with one item publishing an echoMenu of one row.
func startEchoTray(t *testing.T, echo bool) (*systrayModule, *echoMenu, string) {
	t.Helper()
	bus := dbustest.Start(t)
	store := sni.NewStore()
	host, err := sni.NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatalf("NewHostOn: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	ctx := newTestContext(t, config.Defaults())
	ctx.SNI, ctx.Tray = store, NewTrayService(host)
	var module Module
	onHeadlessLoop(func() bool { module, err = Create("systray", ctx); return true })
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := module.(*systrayModule)

	conn := bus.Conn(t)
	menu := &echoMenu{conn: conn, echo: echo}
	menu.rows.Store(1)
	if _, err := prop.Export(conn, echoItemPath, prop.Map{sni.ItemIface: {
		"Id":     {Value: "echo", Emit: prop.EmitConst},
		"Status": {Value: "Active", Emit: prop.EmitConst},
		"Menu":   {Value: dbus.ObjectPath(echoMenuPath), Emit: prop.EmitConst},
	}}); err != nil {
		t.Fatalf("export the item: %v", err)
	}
	if err := conn.Export(menu, echoMenuPath, sni.MenuIface); err != nil {
		t.Fatalf("export the menu: %v", err)
	}
	if err := conn.Object(sni.WatcherName, sni.WatcherPath).Call(sni.WatcherIface+".RegisterStatusNotifierItem", 0, echoItemPath).Err; err != nil {
		t.Fatalf("register: %v", err)
	}
	key := conn.Names()[0] + echoItemPath
	waitHeadless(t, "the echo item's button", func() bool { _, ok := m.entries[key]; return ok })
	return m, menu, key
}

// menuLabels are the open menu's row labels, read on the loop.
func menuLabels(m *systrayModule) []string {
	if m.menu == nil {
		return nil
	}
	labels := make([]string, 0, len(m.menu.items))
	for _, it := range m.menu.items {
		labels = append(labels, it.Label)
	}
	return labels
}

// Opening a menu is one AboutToShow. The app answers it with a longer
// layout and LayoutUpdated; the open menu re-reads that layout once and
// settles on it, never asking AboutToShow again (which would make the
// app grow the menu again, forever).
func TestSystrayMenuOpensWithOneAboutToShow(t *testing.T) {
	m, menu, key := startEchoTray(t, true)
	openTrayMenu(t, m, key)
	waitHeadless(t, "the re-read of the answered layout", func() bool {
		return menu.reads.Load() >= 2 && len(menuLabels(m)) == 2
	})
	if n := menu.shown.Load(); n != 1 {
		t.Errorf("AboutToShow calls = %d, want 1 for one opening", n)
	}
	if got := onHeadlessLoop(func() []string { return menuLabels(m) }); len(got) != 2 || got[1] != "row 1" {
		t.Errorf("rows = %q, want the two of the answered layout", got)
	}
	if n := menu.reads.Load(); n != 2 {
		t.Errorf("GetLayout calls = %d, want the opening's and the one re-read", n)
	}
}

// A LayoutUpdated while the menu is open re-reads the rows with
// GetLayout alone: no AboutToShow, the menu is already shown.
func TestSystrayMenuLayoutUpdateSkipsAboutToShow(t *testing.T) {
	m, menu, key := startEchoTray(t, false)
	openTrayMenu(t, m, key)
	if err := menu.relayout(func(int32) int32 { return 4 }); err != nil {
		t.Fatalf("LayoutUpdated: %v", err)
	}
	waitHeadless(t, "the four rows of the new layout", func() bool { return len(menuLabels(m)) == 4 })
	if n := menu.shown.Load(); n != 1 {
		t.Errorf("AboutToShow calls = %d, want only the opening's", n)
	}
}

// Opening the menu is AboutToShow for the root alone; a submenu sends
// its own as it opens, and when the app answers that it needs an
// update the layout is read again, so the submenu shows its new rows.
func TestSystraySubmenuOpenSendsItsAboutToShow(t *testing.T) {
	m, menu, key := startEchoTray(t, false)
	menu.subRows.Store(1)
	openTrayMenu(t, m, key)
	waitHeadless(t, "the submenu row", func() bool {
		return m.menu != nil && len(m.menu.items) == 1 && len(m.menu.items[0].Items) == 1
	})
	if ids := menu.shownID.Load(); !slices.Equal(ids, []int32{0}) {
		t.Fatalf("opening the menu sent AboutToShow for %v, want the root alone", ids)
	}
	onHeadlessLoop(func() bool {
		if open := m.menu.items[0].OnOpen; open != nil {
			open()
		}
		return true
	})
	waitHeadless(t, "the submenu's AboutToShow", func() bool { return len(menu.shownID.Load()) == 2 })
	if ids := menu.shownID.Load(); ids[1] != 1 {
		t.Errorf("the submenu sent AboutToShow for %d, want its row's id 1", ids[1])
	}
	waitHeadless(t, "the submenu's new rows", func() bool {
		return m.menu != nil && len(m.menu.items[0].Items) == 2
	})
}
