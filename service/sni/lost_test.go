package sni

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const itemName = "org.kde.StatusNotifierItem-233-1"

// playedHost is a host on a connection of bus whose signal loop the
// test plays: it hears of every name the bus loses and starts real
// reads, and the test takes each loss and each answer off the loop's
// channels and hands them to the loop's handlers in the order it
// chooses.
func playedHost(t *testing.T, bus *dbustest.Bus, store *Store) *Host {
	t.Helper()
	h := idleHost(bus.Conn(t), store, liveness, func(err error) { t.Errorf("reported %v", err) })
	if err := h.subscribe(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return h
}

func (h *Host) answerOf(t *testing.T, address string) reading {
	t.Helper()
	h.track(address)
	answer := await(t, h.results, "answer of "+address)
	if answer.err != nil {
		t.Fatalf("read %s: %v", address, answer.err)
	}
	return answer
}

func (h *Host) lossOf(t *testing.T, name string) nameLoss {
	t.Helper()
	loss := await(t, h.vanished, "word that "+name+" lost its owner")
	if loss.name != name {
		t.Fatalf("%s lost its owner, want word of %s", loss.name, name)
	}
	return loss
}

func ownName(t *testing.T, conn *dbus.Conn, name string, flags dbus.RequestNameFlags, want dbus.RequestNameReply) {
	t.Helper()
	if reply, err := conn.RequestName(name, flags); err != nil || reply != want {
		t.Fatalf("request %s: %v %v, want %v", name, reply, err, want)
	}
}

// An item that answered and then left the bus is not stored when the
// host hears of the loss first: no later loss would forget it. The
// answer of an item whose name was lost before it answered, and of one
// that stayed while another connection left, is stored.
func TestAnAnswerIsOrderedAgainstTheLossOfItsName(t *testing.T) {
	t.Run("the item's connection left after it answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		store := NewStore()
		h := playedHost(t, bus, store)
		item := startItem(t, bus, "gone")
		unique := item.conn.Names()[0]
		answer := h.answerOf(t, unique+"/StatusNotifierItem")
		if err := item.conn.Close(); err != nil {
			t.Fatal(err)
		}
		loss := h.lossOf(t, unique)
		if loss.announced <= answer.answered {
			t.Fatalf("the loss is sequenced %d, the answer before it %d", loss.announced, answer.answered)
		}
		h.nameLost(loss)
		h.finish(answer)
		if items := store.Items(); len(items) != 0 || len(h.owners) != 0 {
			t.Errorf("the host tracks %+v of %v, an item that left the bus", items, h.owners)
		}
	})
	t.Run("the item's bus name was released after it answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		store := NewStore()
		h := playedHost(t, bus, store)
		item := startItem(t, bus, "released")
		ownName(t, item.conn, itemName, dbus.NameFlagDoNotQueue, dbus.RequestNameReplyPrimaryOwner)
		answer := h.answerOf(t, itemName+"/StatusNotifierItem")
		if answer.owner != item.conn.Names()[0] {
			t.Fatalf("the item's owner reads %q, want %q", answer.owner, item.conn.Names()[0])
		}
		if _, err := item.conn.ReleaseName(itemName); err != nil {
			t.Fatal(err)
		}
		h.nameLost(h.lossOf(t, itemName))
		h.finish(answer)
		if items := store.Items(); len(items) != 0 || len(h.owners) != 0 {
			t.Errorf("the host tracks %+v of %v, an item whose name has no owner", items, h.owners)
		}
	})
	t.Run("the connection owning the item's bus name left after it answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		store := NewStore()
		h := playedHost(t, bus, store)
		item := startItem(t, bus, "handed over")
		ownName(t, item.conn, itemName, 0, dbus.RequestNameReplyPrimaryOwner)
		ownName(t, bus.Conn(t), itemName, 0, dbus.RequestNameReplyInQueue)
		unique := item.conn.Names()[0]
		answer := h.answerOf(t, itemName+"/StatusNotifierItem")
		if err := item.conn.Close(); err != nil {
			t.Fatal(err)
		}
		h.nameLost(h.lossOf(t, unique))
		h.finish(answer)
		if items := store.Items(); len(items) != 0 || len(h.owners) != 0 {
			t.Errorf("the host tracks %+v of %v, read from a connection that left the bus", items, h.owners)
		}
	})
	t.Run("the item's bus name was lost before it answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		store := NewStore()
		h := playedHost(t, bus, store)
		conn := bus.Conn(t)
		ownName(t, conn, itemName, dbus.NameFlagDoNotQueue, dbus.RequestNameReplyPrimaryOwner)
		frozen := freeze(t, conn, map[string]dbus.Variant{"Id": dbus.MakeVariant("taken again")})
		h.track(itemName + "/StatusNotifierItem")
		await(t, frozen.asked, "read of the item")
		if _, err := conn.ReleaseName(itemName); err != nil {
			t.Fatal(err)
		}
		loss := h.lossOf(t, itemName)
		h.nameLost(loss)
		ownName(t, conn, itemName, dbus.NameFlagDoNotQueue, dbus.RequestNameReplyPrimaryOwner)
		frozen.thaw()
		answer := await(t, h.results, "answer of the item")
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if loss.announced >= answer.answered {
			t.Fatalf("the loss is sequenced %d, the answer after it %d", loss.announced, answer.answered)
		}
		h.finish(answer)
		if items := store.Items(); len(items) != 1 || items[0].ID != "taken again" || h.owners[items[0].Key()] != conn.Names()[0] {
			t.Errorf("the host tracks %+v of %v, want the item that answered under the name it took again", items, h.owners)
		}
	})
	t.Run("another connection left after the item answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		store := NewStore()
		h := playedHost(t, bus, store)
		item := startItem(t, bus, "staying")
		other := bus.Conn(t)
		gone := other.Names()[0]
		answer := h.answerOf(t, item.conn.Names()[0]+"/StatusNotifierItem")
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
		h.nameLost(h.lossOf(t, gone))
		h.finish(answer)
		if items := store.Items(); len(items) != 1 || items[0].ID != "staying" {
			t.Errorf("the host tracks %+v, want the item that stayed", items)
		}
	})
}

// playedWatcher is playedHost serving the watcher role: the test calls
// the registrations and hands the losses to the loop's handler itself.
func playedWatcher(t *testing.T, bus *dbustest.Bus) (*Host, *watcher) {
	t.Helper()
	h := playedHost(t, bus, NewStore())
	h.watcher = newWatcher(h)
	return h, h.watcher
}

func dropped(t *testing.T, h *Host, w *watcher, service string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.register(service)
	}()
	select {
	case <-done:
	case got := <-h.tracks:
		t.Errorf("the registration of %s tracks %q, an item that left the bus", service, got)
		<-done
	}
	if items := w.registeredItems(); len(items) != 0 {
		t.Errorf("the watcher lists %q, an item that left the bus", items)
	}
}

func (w *watcher) registeredItems() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.items)
}

// A registration is ordered against the loss of its bus name: one
// handled after the loss is dropped whether the bus answered before or
// after it, and a loss announced before the bus answered, of the name's
// previous owner, leaves the registration of its new owner.
func TestARegistrationIsOrderedAgainstTheLossOfItsName(t *testing.T) {
	t.Run("the item left the bus while its registration was checked", func(t *testing.T) {
		bus := dbustest.Start(t)
		h, w := playedWatcher(t, bus)
		item := startItem(t, bus, "gone")
		unique := item.conn.Names()[0]
		w.ownerOf = func(name string) (dbus.Sequence, error) {
			answered, err := w.askOwner(name)
			if err != nil {
				t.Fatalf("the owner of %s: %v", name, err)
			}
			if err := item.conn.Close(); err != nil {
				t.Fatal(err)
			}
			loss := h.lossOf(t, unique)
			if loss.announced <= answered {
				t.Fatalf("the loss is sequenced %d, the answer before it %d", loss.announced, answered)
			}
			h.nameLost(loss)
			return answered, nil
		}
		dropped(t, h, w, unique+"/StatusNotifierItem")
	})
	t.Run("the item left the bus before its registration was handled", func(t *testing.T) {
		bus := dbustest.Start(t)
		h, w := playedWatcher(t, bus)
		item := startItem(t, bus, "gone first")
		unique := item.conn.Names()[0]
		if err := item.conn.Close(); err != nil {
			t.Fatal(err)
		}
		h.nameLost(h.lossOf(t, unique))
		dropped(t, h, w, unique+"/StatusNotifierItem")
	})
	t.Run("the name's previous owner left before the bus answered", func(t *testing.T) {
		bus := dbustest.Start(t)
		h, w := playedWatcher(t, bus)
		previous := bus.Conn(t)
		ownName(t, previous, itemName, dbus.NameFlagDoNotQueue, dbus.RequestNameReplyPrimaryOwner)
		if err := previous.Close(); err != nil {
			t.Fatal(err)
		}
		loss := h.lossOf(t, itemName)
		item := startItem(t, bus, "taken over")
		ownName(t, item.conn, itemName, dbus.NameFlagDoNotQueue, dbus.RequestNameReplyPrimaryOwner)
		w.ownerOf = func(name string) (dbus.Sequence, error) {
			answered, err := w.askOwner(name)
			if loss.announced >= answered {
				t.Fatalf("the loss is sequenced %d, the answer after it %d", loss.announced, answered)
			}
			h.nameLost(loss)
			return answered, err
		}
		service := itemName + "/StatusNotifierItem"
		registered := make(chan struct{})
		go func() {
			defer close(registered)
			w.register(service)
		}()
		if got := await(t, h.tracks, "the registered item to track"); got != service {
			t.Errorf("tracking %q, want %q", got, service)
		}
		<-registered
		if items := w.registeredItems(); !slices.Equal(items, []string{service}) {
			t.Errorf("the watcher lists %q, want the item of the name's new owner", items)
		}
	})
}
