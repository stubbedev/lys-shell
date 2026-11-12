package dnssd_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/dnssd"
	"github.com/stubbedev/lys-shell/service/dnssd/dnssdtest"
)

var printer = dnssd.Service{
	Name: "Laser", Type: "_ipps._tcp", Domain: "local", Host: "laser.local", Address: netip.MustParseAddr("192.0.2.7"), Port: 631,
	TXT: map[string]string{"ty": "Example Laser", "rp": "ipp/print"},
}

// A browse reports the services announced before and after it started,
// resolved, and the ones that go; Stop frees every browser it made.
func TestBrowseFindsResolvesAndFrees(t *testing.T) {
	a := dnssdtest.Start(t)
	a.Announce(t, printer)
	found := make(chan dnssd.Service, 4)
	removed := make(chan string, 4)
	b, err := dnssd.New(a.Bus.Conn(t)).Browse(context.Background(), []string{"_ipps._tcp", "_ipp._tcp"},
		func(s dnssd.Service) { found <- s }, func(k string) { removed <- k })
	if err != nil {
		t.Fatal(err)
	}
	wait := func() dnssd.Service {
		t.Helper()
		select {
		case s := <-found:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("nothing found")
			return dnssd.Service{}
		}
	}
	if s := wait(); s.Key() != printer.Key() || s.Host != "laser.local" || s.Address != printer.Address || s.Port != 631 || s.TXT["ty"] != "Example Laser" {
		t.Errorf("found %+v", s)
	}
	later := dnssd.Service{Name: "Inkjet", Type: "_ipp._tcp", Domain: "local", Host: "ink.local", Address: netip.MustParseAddr("192.0.2.8"), Port: 631}
	a.Announce(t, later)
	if s := wait(); s.Name != "Inkjet" {
		t.Errorf("found %+v, want the later one", s)
	}
	a.Withdraw(t, later)
	select {
	case k := <-removed:
		if k != later.Key() {
			t.Errorf("removed %q, want the inkjet", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the withdrawn service was not reported")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if a.Created.Load() != 2 || a.Freed.Load() != 2 {
		t.Errorf("%d browsers made, %d freed", a.Created.Load(), a.Freed.Load())
	}
}

// A bus without Avahi is ErrNoAvahi.
func TestBrowseWithoutAvahi(t *testing.T) {
	bus := dbustest.Start(t)
	_, err := dnssd.New(bus.Conn(t)).Browse(context.Background(), []string{"_ipp._tcp"}, func(dnssd.Service) {}, func(string) {})
	if !errors.Is(err, dnssd.ErrNoAvahi) {
		t.Errorf("browsing without Avahi = %v", err)
	}
}

// A service announced at several addresses is found at the best one
// left as each comes and goes, IPv4 over routable IPv6 over
// link-local, and removed once the last went; a link-local IPv6
// address carries the interface it was found on as its zone.
func TestBrowseFollowsEveryAddress(t *testing.T) {
	a := dnssdtest.Start(t)
	found := make(chan dnssd.Service, 8)
	removed := make(chan string, 8)
	b, err := dnssd.New(a.Bus.Conn(t)).Browse(context.Background(), []string{"_ipps._tcp"},
		func(s dnssd.Service) { found <- s }, func(k string) { removed <- k })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Stop(); err != nil {
			t.Error(err)
		}
	}()
	expect := func(want netip.Addr) {
		t.Helper()
		select {
		case s := <-found:
			if s.Key() != printer.Key() || s.Address != want || s.Port != 631 || s.TXT["ty"] != "Example Laser" {
				t.Errorf("found %+v, want it at %v", s, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("nothing found, want %v", want)
		}
	}
	at := func(addr string) dnssd.Service {
		s := printer
		s.Address = netip.MustParseAddr(addr)
		return s
	}
	linkLocal, routable, v4 := at("fe80::7%lo"), at("2001:db8::7"), at("192.0.2.7")
	a.Announce(t, linkLocal)
	expect(linkLocal.Address)
	a.Announce(t, v4)
	expect(v4.Address)
	a.Announce(t, routable)
	expect(v4.Address)
	a.Withdraw(t, v4)
	expect(routable.Address)
	a.Withdraw(t, routable)
	expect(linkLocal.Address)
	select {
	case k := <-removed:
		t.Fatalf("%q removed while reachable at %v", k, linkLocal.Address)
	default:
	}
	a.Withdraw(t, linkLocal)
	select {
	case k := <-removed:
		if k != printer.Key() {
			t.Errorf("removed %q", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the service gone from every address was not removed")
	}
}

// A browse hears Avahi alone: another connection's word in its name,
// at the browse's own browser, is ignored even on a connection whose
// other rules let it in.
func TestBrowseIgnoresAnotherConnection(t *testing.T) {
	a := dnssdtest.Start(t)
	a.Announce(t, printer)
	conn := a.Bus.Conn(t)
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(dnssd.BrowserIface)); err != nil {
		t.Fatal(err)
	}
	found := make(chan dnssd.Service, 4)
	removed := make(chan string, 4)
	b, err := dnssd.New(conn).Browse(context.Background(), []string{printer.Type},
		func(s dnssd.Service) { found <- s }, func(k string) { removed <- k })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Stop(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-found:
	case <-time.After(5 * time.Second):
		t.Fatal("the announced printer was not found")
	}
	browsers := a.Browsing(printer.Type)
	if len(browsers) != 1 {
		t.Fatalf("browsers %v, want one", browsers)
	}
	impostor := a.Bus.Conn(t)
	if err := impostor.Emit(browsers[0], dnssd.BrowserIface+".ItemRemove", int32(2), int32(0), printer.Name, printer.Type, printer.Domain, uint32(0)); err != nil {
		t.Fatal(err)
	}
	if err := impostor.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatal(err)
	}
	later := dnssd.Service{Name: "Inkjet", Type: printer.Type, Domain: "local", Host: "ink.local", Address: netip.MustParseAddr("192.0.2.8"), Port: 631}
	a.Announce(t, later)
	select {
	case s := <-found:
		if s.Key() != later.Key() {
			t.Errorf("found %+v, want the inkjet", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the later service was not found")
	}
	select {
	case k := <-removed:
		t.Errorf("%q removed on another connection's word", k)
	default:
	}
}

// A browse keeps none of the descriptors Avahi's signals carry: one
// that does not decode as a browser item has them closed.
func TestBrowseClosesTheDescriptorsOfTheSignalsItReads(t *testing.T) {
	a := dnssdtest.Start(t)
	b, err := dnssd.New(a.Bus.Conn(t)).Browse(context.Background(), []string{"_ipps._tcp"}, func(dnssd.Service) {}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Stop(); err != nil {
			t.Error(err)
		}
	}()
	pipe := dbustest.NewPipe(t)
	a.Emit(t, "/Client0/ServiceBrowser9", dnssd.BrowserIface+".ItemNew", pipe.Write)
	pipe.RequireEveryCopyClosed(t)
}
