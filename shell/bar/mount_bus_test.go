package bar

import (
	"bytes"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
)

func goroutineID() int64 {
	var buf [64]byte
	stack := buf[:runtime.Stack(buf[:], false)]
	stack = bytes.TrimPrefix(stack, []byte("goroutine "))
	id, err := strconv.ParseInt(string(stack[:bytes.IndexByte(stack, ' ')]), 10, 64)
	if err != nil {
		panic("bar: unreadable goroutine header: " + err.Error())
	}
	return id
}

type loopCallCounter struct {
	loop  atomic.Int64
	calls atomic.Int64
	names atomic.Pointer[[]string]
}

func (c *loopCallCounter) intercept(msg *dbus.Message) {
	if msg.Type != dbus.TypeMethodCall || msg.Flags&dbus.FlagNoReplyExpected != 0 || c.loop.Load() != goroutineID() {
		return
	}
	c.calls.Add(1)
	member, _ := msg.Headers[dbus.FieldMember].Value().(string)
	iface, _ := msg.Headers[dbus.FieldInterface].Value().(string)
	for {
		old := c.names.Load()
		next := append(append([]string(nil), deref(old)...), iface+"."+member)
		if c.names.CompareAndSwap(old, &next) {
			return
		}
	}
}

func deref(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func TestMountMakesNoBlockingBusCallOnTheLoop(t *testing.T) {
	isolateConfigDir(t)
	clearCompositorEnv(t)
	rem := widget.RootFontSize()
	t.Cleanup(func() {
		widget.SetRootFontSize(rem)
		widget.SetFaceResolver(nil)
	})
	sessionBus, systemBus := dbustest.Start(t), dbustest.Start(t)
	sessionBus.UseAsSessionBus(t)
	systemBus.UseAsSystemBus(t)
	counter := &loopCallCounter{}
	dial := func(bus *dbustest.Bus) *dbus.Conn {
		conn, err := dbus.Connect(bus.Address, dbus.WithOutgoingInterceptor(counter.intercept))
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	session, system := dial(sessionBus), dial(systemBus)
	t.Cleanup(func() { _ = session.Close() })
	a := apptest.Run(t)
	s := newShellStart(a, a.Session(), config.Defaults(), nil, session)
	apptest.On(a, func() bool { s.releaseHold = a.Hold(); return true })
	s.startServices(system)
	t.Cleanup(func() { apptest.On(a, func() bool { s.down.run(); return true }) })
	apptest.On(a, func() bool {
		counter.loop.Store(goroutineID())
		defer counter.loop.Store(0)
		s.mount()
		return true
	})
	if s.err != nil {
		t.Fatalf("mount: %v", s.err)
	}
	if n := counter.calls.Load(); n != 0 {
		t.Errorf("mount made %d blocking bus calls on the loop: %v", n, deref(counter.names.Load()))
	}
	apptest.On(a, func() bool {
		counter.loop.Store(goroutineID())
		defer counter.loop.Store(0)
		return session.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err == nil
	})
	if n := counter.calls.Load(); n != 1 {
		t.Errorf("a call on the loop counted %d times, want once: the counter proves nothing", n)
	}
}
