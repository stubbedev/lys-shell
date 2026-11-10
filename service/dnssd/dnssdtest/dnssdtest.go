// Package dnssdtest is a fake Avahi on a private bus: the services a
// test announces are reported to every browser of their type, once per
// address, on the interface and protocol the address names, and
// resolve to what the test set; the browsers made and freed are
// counted.
package dnssdtest

import (
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/dnssd"
)

// Avahi's protocols, and the interface an address without a zone is
// announced on.
const (
	protoInet    = int32(0)
	protoInet6   = int32(1)
	defaultIface = int32(2)
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

// Emit sends the signal name from Avahi's object at path, carrying
// args.
func (a *Avahi) Emit(t *testing.T, path dbus.ObjectPath, name string, args ...any) {
	t.Helper()
	if err := a.conn.Emit(path, name, args...); err != nil {
		t.Fatalf("dnssdtest: emit %s at %s: %v", name, path, err)
	}
}

// instance is the interface and protocol s is announced on: its
// address's zone (an interface name) or defaultIface, and its family.
func instance(s dnssd.Service) (int32, int32, error) {
	iface := defaultIface
	if zone := s.Address.Zone(); zone != "" {
		ifc, err := net.InterfaceByName(zone)
		if err != nil {
			return 0, 0, err
		}
		if iface, err = intconv.Exact[int32](ifc.Index); err != nil {
			return 0, 0, err
		}
	}
	if s.Address.Is6() {
		return iface, protoInet6, nil
	}
	return iface, protoInet, nil
}

// Announce adds a service at one address, reporting it to the browsers
// of its type; a service announced at several addresses is one per
// interface and protocol, as Avahi's are.
func (a *Avahi) Announce(t *testing.T, s dnssd.Service) {
	t.Helper()
	a.mu.Lock()
	a.services = append(a.services, s)
	paths := a.browsing(s.Type)
	a.mu.Unlock()
	for _, p := range paths {
		if err := a.item(p, "ItemNew", s); err != nil {
			t.Errorf("dnssdtest: ItemNew %s: %v", s.Name, err)
		}
	}
}

// Withdraw removes a service at the address s names, reporting it
// gone from there to the browsers of its type.
func (a *Avahi) Withdraw(t *testing.T, s dnssd.Service) {
	t.Helper()
	a.mu.Lock()
	a.services = slices.DeleteFunc(a.services, func(o dnssd.Service) bool { return o.Key() == s.Key() && o.Address == s.Address })
	paths := a.browsing(s.Type)
	a.mu.Unlock()
	for _, p := range paths {
		if err := a.item(p, "ItemRemove", s); err != nil {
			t.Errorf("dnssdtest: ItemRemove %s: %v", s.Name, err)
		}
	}
}

// browsing are the browsers of typ, the lock held.
func (a *Avahi) browsing(typ string) []dbus.ObjectPath {
	var paths []dbus.ObjectPath
	for p, bt := range a.browsers {
		if bt == typ {
			paths = append(paths, p)
		}
	}
	return paths
}

func (a *Avahi) item(path dbus.ObjectPath, member string, s dnssd.Service) error {
	iface, proto, err := instance(s)
	if err != nil {
		return err
	}
	return a.conn.Emit(path, dnssd.BrowserIface+"."+member, iface, proto, s.Name, s.Type, s.Domain, uint32(0))
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

// ResolveService answers the service announced on the interface and
// protocol asked, its address without the zone as Avahi gives it; an
// address of another protocol than aproto times out.
func (s server) ResolveService(iface, proto int32, name, typ, domain string, aproto int32, _ uint32) (int32, int32, string, string, string, string, int32, string, uint16, [][]byte, uint32, *dbus.Error) {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	for _, svc := range s.a.services {
		at, family, err := instance(svc)
		if err != nil {
			return 0, 0, "", "", "", "", 0, "", 0, nil, 0, dbus.MakeFailedError(err)
		}
		if svc.Name != name || svc.Type != typ || svc.Domain != domain || at != iface || family != proto || family != aproto {
			continue
		}
		var txt [][]byte
		for k, v := range svc.TXT {
			txt = append(txt, []byte(k+"="+v))
		}
		return iface, proto, name, typ, domain, svc.Host, family, svc.Address.WithZone("").String(), svc.Port, txt, 0, nil
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
