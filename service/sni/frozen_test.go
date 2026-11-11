package sni

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const liveness = 10 * time.Second

func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(liveness):
		t.Fatalf("no %s", what)
		panic("unreachable")
	}
}

// frozenProps is the Properties object of a tray application that has
// stopped answering: each GetAll is announced on asked and answered
// with props once thaw is called, at the end of the test at the latest.
type frozenProps struct {
	asked chan struct{}
	thaw  func()
	props map[string]dbus.Variant
}

// freeze replaces the Properties object of an item on conn with one
// that does not answer.
func freeze(t *testing.T, conn *dbus.Conn, props map[string]dbus.Variant) *frozenProps {
	t.Helper()
	thawed := make(chan struct{})
	f := &frozenProps{asked: make(chan struct{}, 16), thaw: sync.OnceFunc(func() { close(thawed) }), props: props}
	t.Cleanup(f.thaw)
	if err := conn.Export(frozenGetAll(func() map[string]dbus.Variant {
		f.asked <- struct{}{}
		<-thawed
		return f.props
	}), "/StatusNotifierItem", PropsIface); err != nil {
		t.Fatal(err)
	}
	return f
}

type frozenGetAll func() map[string]dbus.Variant

func (answer frozenGetAll) GetAll(string) (map[string]dbus.Variant, *dbus.Error) {
	return answer(), nil
}

// An item that stops answering holds up nothing but its own snapshot:
// while its read is under way the host re-reads the other items, takes
// new ones, forgets the ones that leave, and closes.
func TestAnItemThatNeverAnswersDoesNotStallTheHost(t *testing.T) {
	bus := dbustest.Start(t)
	store := NewStore()
	host, err := NewHostOn(bus.Conn(t), store)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = host.Close()
		}
	}()
	stuck, lively, leaving := startItem(t, bus, "stuck"), startItem(t, bus, "lively"), startItem(t, bus, "leaving")
	for _, item := range []*fakeItem{stuck, lively, leaving} {
		item.register(t)
	}
	waitItems(t, store, func(i []Item) bool { return len(i) == 3 })

	frozen := freeze(t, stuck.conn, nil)
	if err := stuck.conn.Emit("/StatusNotifierItem", ItemIface+".NewIcon"); err != nil {
		t.Fatal(err)
	}
	await(t, frozen.asked, "read of the item that changed its icon")
	for range 3 {
		if err := stuck.conn.Emit("/StatusNotifierItem", ItemIface+".NewIcon"); err != nil {
			t.Fatal(err)
		}
	}

	lively.props.SetMust(ItemIface, "Title", "retitled")
	if err := lively.conn.Emit("/StatusNotifierItem", ItemIface+".NewTitle"); err != nil {
		t.Fatal(err)
	}
	titleOf := func(items []Item, id string) string {
		for _, it := range items {
			if it.ID == id {
				return it.Title
			}
		}
		return ""
	}
	waitItems(t, store, func(i []Item) bool { return titleOf(i, "lively") == "retitled" })

	startItem(t, bus, "late").register(t)
	waitItems(t, store, func(i []Item) bool { return titleOf(i, "late") == "late title" })

	if err := leaving.conn.Close(); err != nil {
		t.Fatal(err)
	}
	items := waitItems(t, store, func(i []Item) bool { return len(i) == 3 && titleOf(i, "leaving") == "" })
	if got := titleOf(items, "stuck"); got != "stuck title" {
		t.Errorf("the item that stopped answering reads %q, want the snapshot it had", got)
	}

	done := make(chan error, 1)
	go func() { done <- host.Close() }()
	closed = true
	if err := await(t, done, "end of Close while an item's read is under way"); err != nil {
		t.Errorf("Close: %v", err)
	}
	select {
	case <-frozen.asked:
		t.Error("the item that stopped answering was asked again while its read was under way")
	default:
	}
}

// The host gives an item the item timeout to answer, then says which
// item did not and which call it left unanswered, and keeps the item
// out of the tray.
func TestTheHostReportsTheItemThatNeverAnswers(t *testing.T) {
	bus := dbustest.Start(t)
	store := NewStore()
	failures := make(chan error, 16)
	host, err := newHost(bus.Conn(t), store, 20*time.Millisecond, func(err error) { failures <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	stuck := startItem(t, bus, "stuck")
	frozen := freeze(t, stuck.conn, nil)
	stuck.register(t)
	await(t, frozen.asked, "read of the registered item")

	failure := await(t, failures, "report of the item that never answered")
	if !errors.Is(failure, context.DeadlineExceeded) {
		t.Errorf("the report is %v, want a deadline that passed", failure)
	}
	address := stuck.conn.Names()[0] + "/StatusNotifierItem"
	for _, part := range []string{address, PropsIface + ".GetAll", "20ms"} {
		if !strings.Contains(failure.Error(), part) {
			t.Errorf("the report %q does not name %s", failure, part)
		}
	}
	if items := store.Items(); len(items) != 0 {
		t.Errorf("the item that never answered is in the tray: %+v", items)
	}
	select {
	case again := <-failures:
		t.Errorf("a second report for one read: %v", again)
	case <-frozen.asked:
		t.Error("the host asked the item again by itself")
	default:
	}
}

// blockedWatcher is an external watcher that never answers a host's
// registration.
type blockedWatcher struct{ thaw chan struct{} }

func (w blockedWatcher) RegisterStatusNotifierHost(string) *dbus.Error {
	<-w.thaw
	return nil
}

// A watcher that never answers the host's registration fails the host
// within the timeout, naming the call, and the host leaves the shared
// connection as it found it.
func TestAWatcherThatNeverAnswersFailsTheHost(t *testing.T) {
	bus := dbustest.Start(t)
	watcher := bus.Conn(t)
	thaw := make(chan struct{})
	t.Cleanup(func() { close(thaw) })
	if err := watcher.Export(blockedWatcher{thaw}, WatcherPath, WatcherIface); err != nil {
		t.Fatal(err)
	}
	if reply, err := watcher.RequestName(WatcherName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", WatcherName, reply, err)
	}
	conn := bus.Conn(t)
	host, err := newHost(conn, NewStore(), 20*time.Millisecond, func(err error) { t.Errorf("reported %v", err) })
	if err == nil {
		_ = host.Close()
		t.Fatal("a host started against a watcher that never answered it")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "RegisterStatusNotifierHost") {
		t.Errorf("the failure is %v, want the registration's deadline", err)
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Errorf("the failed host left the match rules %q", rules)
	}
}
