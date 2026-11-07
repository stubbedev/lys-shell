// Package dnssdtest is a fake Avahi on a private bus: the services a
// test announces are reported to every browser of their type and
// resolve to what the test set, and the browsers made and freed are
// counted.
package dnssdtest

import (
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/dnssd"
)

// Avahi is the fake.
type Avahi struct {
	// Bus is the private bus the fake owns Avahi's name on.
	Bus *dbustest.Bus
	// Created and Freed count the browsers made and freed.
	Created, Freed dbustest.Var[int]
	conn           *dbus.Conn
	mu             sync.Mutex
	services       []dnssd.Service
	browsers       map[dbus.ObjectPath]string
}

// Start runs the fake on a private bus.
func Start(t *testing.T) *Avahi {
	t.Helper()
	a := &Avahi{Bus: dbustest.Start(t), browsers: map[dbus.ObjectPath]string{}}
	a.conn = a.Bus.Conn(t)
	if err := a.conn.Export(server{a}, "/", dnssd.ServerIface); err != nil {
		t.Fatalf("dnssdtest: export the server: %v", err)
	}
	if reply, err := a.conn.RequestName(dnssd.BusName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("dnssdtest: own %s: %v (%v)", dnssd.BusName, reply, err)
	}
	return a
}

// Announce adds a service, reporting it to the browsers of its type.
func (a *Avahi) Announce(t *testing.T, s dnssd.Service) {
	t.Helper()
	a.mu.Lock()
	a.services = append(a.services, s)
	var paths []dbus.ObjectPath
	for p, typ := range a.browsers {
		if typ == s.Type {
			paths = append(paths, p)
		}
	}
	a.mu.Unlock()
	for _, p := range paths {
		if err := a.item(p, "ItemNew", s); err != nil {
			t.Errorf("dnssdtest: ItemNew %s: %v", s.Name, err)
		}
	}
}

// Withdraw removes a service, reporting it gone to the browsers of its
// type.
func (a *Avahi) Withdraw(t *testing.T, s dnssd.Service) {
	t.Helper()
	a.mu.Lock()
	a.services = slices.DeleteFunc(a.services, func(o dnssd.Service) bool { return o.Key() == s.Key() })
	var paths []dbus.ObjectPath
	for p, typ := range a.browsers {
		if typ == s.Type {
			paths = append(paths, p)
		}
	}
	a.mu.Unlock()
	for _, p := range paths {
		if err := a.item(p, "ItemRemove", s); err != nil {
			t.Errorf("dnssdtest: ItemRemove %s: %v", s.Name, err)
		}
	}
}

func (a *Avahi) item(path dbus.ObjectPath, member string, s dnssd.Service) error {
	return a.conn.Emit(path, dnssd.BrowserIface+"."+member, int32(2), int32(0), s.Name, s.Type, s.Domain, uint32(0))
}

type server struct{ a *Avahi }

// ServiceBrowserNew makes a browser and reports the services of its
// type to it.
func (s server) ServiceBrowserNew(_, _ int32, typ, _ string, _ uint32) (dbus.ObjectPath, *dbus.Error) {
	a := s.a
	n := a.Created.Update(func(n int) int { return n + 1 })
	path := dbus.ObjectPath("/Client1/ServiceBrowser" + strconv.Itoa(n))
	if err := a.conn.Export(browser{a, path}, path, dnssd.BrowserIface); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	a.mu.Lock()
	a.browsers[path] = typ
	var announced []dnssd.Service
	for _, svc := range a.services {
		if svc.Type == typ {
			announced = append(announced, svc)
		}
	}
	a.mu.Unlock()
	for _, svc := range announced {
		if err := a.item(path, "ItemNew", svc); err != nil {
			return "", dbus.MakeFailedError(err)
		}
	}
	return path, nil
}

// ResolveService answers an announced service.
func (s server) ResolveService(iface, proto int32, name, typ, domain string, _ int32, _ uint32) (int32, int32, string, string, string, string, int32, string, uint16, [][]byte, uint32, *dbus.Error) {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	for _, svc := range s.a.services {
		if svc.Name != name || svc.Type != typ || svc.Domain != domain {
			continue
		}
		var txt [][]byte
		for k, v := range svc.TXT {
			txt = append(txt, []byte(k+"="+v))
		}
		return iface, proto, name, typ, domain, svc.Host, 0, svc.Address, svc.Port, txt, 0, nil
	}
	return 0, 0, "", "", "", "", 0, "", 0, nil, 0, dbus.NewError("org.freedesktop.Avahi.TimeoutError", []any{"Timeout reached"})
}

type browser struct {
	a    *Avahi
	path dbus.ObjectPath
}

// Free frees the browser.
func (b browser) Free() *dbus.Error {
	b.a.Freed.Update(func(n int) int { return n + 1 })
	b.a.mu.Lock()
	delete(b.a.browsers, b.path)
	b.a.mu.Unlock()
	if err := b.a.conn.Export(nil, b.path, dnssd.BrowserIface); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}
