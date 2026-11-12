package bussignal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
)

const (
	liveness   = 10 * time.Second
	handedPath = dbus.ObjectPath("/dev/stubbe/lys/Handed")
	wellKnown  = "dev.stubbe.lys.Handed"
)

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
	if err := stop(); err != nil {
		t.Fatalf("stop the feed: %v", err)
	}
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
	if err := stop(); err != nil {
		t.Fatalf("stop the ended feed: %v", err)
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the ended feed left the match rules %q", rules)
	}
}

// A feed whose connection closed closes too, and its stop says so.
func TestTicksEndWithTheirConnectionAndStopSaysSo(t *testing.T) {
	bus := dbustest.Start(t)
	conn := bus.Conn(t)
	ticks, stop, err := bussignal.Ticks(t.Context(), conn, dbusx.FromOwner(wellKnown), "test", func(func()) []dbusx.Route {
		return []dbusx.Route{dbusx.On(handedSignal, handedPath, func(handed, dbus.Sequence) error { return nil })}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open := awaitOpen(t, ticks, "close of the feed whose connection closed"); open {
		t.Fatal("a tick from a feed whose connection closed")
	}
	if err := stop(); !errors.Is(err, dbusx.ErrDisconnected) {
		t.Fatalf("stop = %v, want %v", err, dbusx.ErrDisconnected)
	}
}
