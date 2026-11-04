package logind

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// fakeLogindSession is the session object of a fake logind: the real
// path, SetLockedHint, and the LockedHint property.
type fakeLogindSession struct {
	mu     sync.Mutex
	locked bool
}

func (s *fakeLogindSession) SetLockedHint(locked bool) *dbus.Error {
	s.mu.Lock()
	s.locked = locked
	s.mu.Unlock()
	return nil
}

// fakeProps serves org.freedesktop.DBus.Properties.Get for LockedHint.
type fakeProps struct{ s *fakeLogindSession }

func (p fakeProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != SessionIf || name != "LockedHint" {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return dbus.MakeVariant(p.s.locked), nil
}

// fakeManager resolves "auto" to the real session path.
type fakeManager struct{}

const fakeSessionPath = dbus.ObjectPath("/org/freedesktop/login1/session/_31")

func (fakeManager) GetSession(name string) (dbus.ObjectPath, *dbus.Error) {
	if name != "auto" {
		return "", dbus.NewError("org.freedesktop.login1.NoSuchSession", nil)
	}
	return fakeSessionPath, nil
}

func startFakeLogind(t *testing.T) (*dbustest.Bus, *dbus.Conn, *fakeLogindSession) {
	t.Helper()
	bus := dbustest.Start(t)
	server := bus.Conn(t)
	sess := &fakeLogindSession{}
	for _, e := range []struct {
		v    any
		path dbus.ObjectPath
		as   string
	}{
		{fakeManager{}, ManagerPath, ManagerIf},
		{sess, fakeSessionPath, SessionIf},
		{fakeProps{s: sess}, fakeSessionPath, "org.freedesktop.DBus.Properties"},
	} {
		if err := server.Export(e.v, e.path, e.as); err != nil {
			t.Fatal(err)
		}
	}
	if reply, err := server.RequestName(Service, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim %s: %v %v", Service, reply, err)
	}
	return bus, server, sess
}

// TestLogindHint: the session resolves through GetSession("auto"), and
// the hint round-trips.
func TestLogindHint(t *testing.T) {
	bus, _, sess := startFakeLogind(t)
	l, err := NewClient(t.Context(), bus.Conn(t))
	if err != nil {
		t.Fatalf("NewLogind: %v", err)
	}
	if l.Path() != fakeSessionPath {
		t.Errorf("session path = %s, want the real path behind auto", l.Path())
	}
	if locked, err := l.LockedHint(t.Context()); err != nil || locked {
		t.Fatalf("LockedHint = %v, %v", locked, err)
	}
	if err := l.SetLockedHint(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	sess.mu.Lock()
	got := sess.locked
	sess.mu.Unlock()
	if !got {
		t.Error("SetLockedHint did not reach logind")
	}
	if locked, err := l.LockedHint(t.Context()); err != nil || !locked {
		t.Errorf("LockedHint after set = %v, %v", locked, err)
	}
}

// TestLogindSignals: Lock and Unlock from the session's path drive the
// callbacks; the same signals from another session do not.
func TestLogindSignals(t *testing.T) {
	bus, server, _ := startFakeLogind(t)
	l, err := NewClient(t.Context(), bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- l.Listen(ctx, func() { events <- "lock" }, func() { events <- "unlock" })
	}()
	// Let the match rule land before emitting.
	time.Sleep(100 * time.Millisecond)
	emit := func(path dbus.ObjectPath, member string) {
		if err := server.Emit(path, SessionIf+"."+member); err != nil {
			t.Fatal(err)
		}
	}
	emit("/org/freedesktop/login1/session/_99", "Lock") // someone else's session
	emit(fakeSessionPath, "Lock")
	emit(fakeSessionPath, "Unlock")
	for _, want := range []string{"lock", "unlock"} {
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event = %s, want %s (another session's signal leaked?)", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s event", want)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Listen: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Listen outlived its context")
	}
}

type heldSession struct {
	entered chan struct{}
	release chan struct{}
}

func (s *heldSession) SetLockedHint(bool) *dbus.Error {
	s.entered <- struct{}{}
	<-s.release
	return nil
}

func (s *heldSession) Get(string, string) (dbus.Variant, *dbus.Error) {
	s.entered <- struct{}{}
	<-s.release
	return dbus.MakeVariant(true), nil
}

func TestLogindCallsEndWithTheirContext(t *testing.T) {
	bus := dbustest.Start(t)
	server := bus.Conn(t)
	held := &heldSession{entered: make(chan struct{}, 4), release: make(chan struct{})}
	t.Cleanup(func() { close(held.release) })
	const path = dbus.ObjectPath("/held")
	if err := server.Export(held, path, SessionIf); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(held, path, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	l := &Client{conn: bus.Conn(t), path: dbus.ObjectPath(path)}
	if _, err := server.RequestName(Service, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"SetLockedHint", func(ctx context.Context) error { return l.SetLockedHint(ctx, true) }},
		{"LockedHint", func(ctx context.Context) error { _, err := l.LockedHint(ctx); return err }},
	} {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- call.run(ctx) }()
		select {
		case <-held.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never reached logind", call.name)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s = %v, want its context's end", call.name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s outlived its context with logind silent", call.name)
		}
	}
}

func TestNewLogindWithoutLogind(t *testing.T) {
	bus := dbustest.Start(t)
	if _, err := NewClient(t.Context(), bus.Conn(t)); err == nil {
		t.Error("NewLogind succeeded with no logind on the bus")
	}
}

func TestSystemClientEndsWithItsContextOnASilentBus(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "silent")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+sock)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *Client, 1)
	go func() { done <- SystemClient(ctx, "test") }()
	if _, err := ln.Accept(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case l := <-done:
		if l != nil {
			t.Error("a client from a bus that never answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SystemClient outlived its context on a silent bus")
	}
}
