package dnssd_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-shell/service/dnssd"
	"github.com/stubbedev/lys-shell/service/dnssd/dnssdtest"
)

var printer = dnssd.Service{
	Name: "Laser", Type: "_ipps._tcp", Domain: "local", Host: "laser.local", Address: "192.0.2.7", Port: 631,
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
	if s := wait(); s.Key() != printer.Key() || s.Host != "laser.local" || s.Address != "192.0.2.7" || s.Port != 631 || s.TXT["ty"] != "Example Laser" {
		t.Errorf("found %+v", s)
	}
	later := dnssd.Service{Name: "Inkjet", Type: "_ipp._tcp", Domain: "local", Host: "ink.local", Port: 631}
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
