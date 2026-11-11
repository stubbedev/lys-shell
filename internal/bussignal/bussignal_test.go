package bussignal_test

import (
	"context"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
	"github.com/stubbedev/lys-shell/internal/bussignal/bussignaltest"
)

const (
	liveness   = 10 * time.Second
	handedPath = dbus.ObjectPath("/dev/stubbe/lys/Handed")
	handedName = "dev.stubbe.lys.Handed.Descriptor"
	wellKnown  = "dev.stubbe.lys.Handed"
)

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

func listen(t *testing.T, conn *dbus.Conn, size int) chan *dbus.Signal {
	t.Helper()
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(handedPath)); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, size)
	conn.Signal(signals)
	return signals
}

func TestANameLosingItsOwnerIsRoutedAndOneGainingItIsNot(t *testing.T) {
	bus := dbustest.Start(t)
	lost := make(chan string, 4)
	watch, err := dbusx.WatchSignals(t.Context(), bus.Conn(t), dbusx.FromBus(),
		func(err error) { t.Errorf("the watch failed: %v", err) },
		bussignal.OnNameLost(func(name string, _ dbus.Sequence) error {
			lost <- name
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watch.Stop(); err != nil {
			t.Errorf("stop the watch: %v", err)
		}
	})

	peer := bus.Conn(t)
	unique := peer.Names()[0]
	if reply, err := peer.RequestName(wellKnown, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", wellKnown, reply, err)
	}
	if _, err := peer.ReleaseName(wellKnown); err != nil {
		t.Fatal(err)
	}
	if got := await(t, lost, "word of the released name"); got != wellKnown {
		t.Fatalf("lost %q first, want %q: the peer connecting and taking the name are not losses", got, wellKnown)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := await(t, lost, "word of the peer leaving"); got != unique {
		t.Fatalf("lost %q, want the unique name %q of the peer that left", got, unique)
	}
}

func busID(t *testing.T, conn *dbus.Conn) dbus.Sequence {
	t.Helper()
	call := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0)
	if call.Err != nil {
		t.Fatal(call.Err)
	}
	return call.ResponseSequence
}

// A loss carries its place among what the connection read: after the
// reply to a call answered before the peer left, before the reply to
// one made once the loss was known.
func TestANameLossIsSequencedAmongTheRepliesOfItsConnection(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	lost := make(chan dbus.Sequence, 4)
	watch, err := dbusx.WatchSignals(t.Context(), conn, dbusx.FromBus(),
		func(err error) { t.Errorf("the watch failed: %v", err) },
		bussignal.OnNameLost(func(_ string, announced dbus.Sequence) error {
			lost <- announced
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watch.Stop(); err != nil {
			t.Errorf("stop the watch: %v", err)
		}
	})
	peer := bus.Conn(t)
	before := busID(t, conn)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	announced := await(t, lost, "word of the peer leaving")
	after := busID(t, conn)
	if before >= announced || announced >= after {
		t.Errorf("the loss is sequenced %d, want it between the replies sequenced %d and %d", announced, before, after)
	}
}

func TestOnNameLostNarrowsToOneName(t *testing.T) {
	bus := dbustest.Start(t)
	lost := make(chan string, 4)
	watch, err := dbusx.WatchSignals(t.Context(), bus.Conn(t), dbusx.FromBus(),
		func(err error) { t.Errorf("the watch failed: %v", err) },
		bussignal.OnNameLost(func(name string, _ dbus.Sequence) error {
			lost <- name
			return nil
		}).WithArg0(wellKnown))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watch.Stop(); err != nil {
			t.Errorf("stop the watch: %v", err)
		}
	})
	other := bus.Conn(t)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	peer := bus.Conn(t)
	if reply, err := peer.RequestName(wellKnown, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", wellKnown, reply, err)
	}
	if _, err := peer.ReleaseName(wellKnown); err != nil {
		t.Fatal(err)
	}
	if got := await(t, lost, "word of the released name"); got != wellKnown {
		t.Fatalf("lost %q, want only %q: another peer left before it", got, wellKnown)
	}
}

func TestDiscardClosesTheDescriptorsOfASignal(t *testing.T) {
	bus := dbustest.Start(t)
	signals := listen(t, bus.Conn(t), 4)
	fd, closed := bussignaltest.Descriptor(t)
	if err := bus.Conn(t).Emit(handedPath, handedName, fd); err != nil {
		t.Fatal(err)
	}
	sig := await(t, signals, "signal carrying the descriptor")
	if got, ok := sig.Body[0].(dbus.UnixFD); !ok || got == fd || got < 0 {
		t.Fatalf("the signal carries %v, want a descriptor of the reader's own", sig.Body[0])
	}
	bussignal.Discard(sig)
	closed("the reader discarded the signal")
}

func TestRemoveClosesTheDescriptorsNobodyRead(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	signals := listen(t, conn, 8)
	marker := listen(t, conn, 8)
	sender := bus.Conn(t)
	fd, closed := bussignaltest.Descriptor(t)
	const sent = 3
	for range sent {
		if err := sender.Emit(handedPath, handedName, fd); err != nil {
			t.Fatal(err)
		}
	}
	for range sent {
		bussignal.Discard(await(t, marker, "signal on the second channel, by which the first holds it too"))
	}
	bussignal.Remove(conn, signals)
	if len(signals) != 0 {
		t.Fatalf("%d signals left on the removed channel", len(signals))
	}
	closed("the channel was removed with its signals unread")

	if err := sender.Emit(handedPath, handedName, "after"); err != nil {
		t.Fatal(err)
	}
	await(t, marker, "later signal on the channel still registered")
	select {
	case sig := <-signals:
		t.Fatalf("the removed channel was sent %s", sig.Name)
	default:
	}
}

type handed struct {
	Text string
}

var handedSignal = dbusx.NewSignal[handed]("dev.stubbe.lys.Handed", "Descriptor")

// A feed of ticks carries one for the signals its routes name, closes
// once it is stopped, and leaves no match rule on the connection.
func TestTicksFollowTheWatchAndCloseWithIt(t *testing.T) {
	bus := dbustest.Start(t)
	conn, service := bus.Conn(t), bus.Conn(t)
	if reply, err := service.RequestName(wellKnown, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", wellKnown, reply, err)
	}
	ticks, stop, err := bussignal.Ticks(t.Context(), conn, dbusx.FromOwner(wellKnown), "test", func(tick func()) []dbusx.Route {
		return []dbusx.Route{dbusx.On(handedSignal, handedPath, func(handed, dbus.Sequence) error {
			tick()
			return nil
		})}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticks:
		t.Fatal("a tick before any signal")
	default:
	}
	if err := handedSignal.Emit(service, handedPath, handed{Text: "changed"}); err != nil {
		t.Fatal(err)
	}
	if _, open := awaitOpen(t, ticks, "tick for the signal"); !open {
		t.Fatal("the feed closed while its watch ran")
	}
	stop()
	if _, open := awaitOpen(t, ticks, "close of the stopped feed"); open {
		t.Fatal("a tick from a stopped feed")
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the stopped feed left the match rules %q", rules)
	}
}

func awaitOpen[T any](t *testing.T, ch <-chan T, what string) (T, bool) {
	t.Helper()
	select {
	case v, open := <-ch:
		return v, open
	case <-time.After(liveness):
		t.Fatalf("no %s", what)
		panic("unreachable")
	}
}

func TestTicksEndWithTheirContext(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	ctx, cancel := context.WithCancel(t.Context())
	ticks, stop, err := bussignal.Ticks(ctx, conn, dbusx.FromOwner(wellKnown), "test", func(func()) []dbusx.Route {
		return []dbusx.Route{dbusx.On(handedSignal, handedPath, func(handed, dbus.Sequence) error { return nil })}
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, open := awaitOpen(t, ticks, "close of the feed whose context ended"); open {
		t.Fatal("a tick from a feed whose context ended")
	}
	stop()
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the ended feed left the match rules %q", rules)
	}
}
