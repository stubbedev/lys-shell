// Package dnssd browses DNS-SD services through Avahi's D-Bus API on
// the system bus, with no mDNS library: a browser per service type
// reports the services that come and go, each resolved to its host,
// port and TXT record.
package dnssd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Avahi's names.
const (
	BusName        = "org.freedesktop.Avahi"
	ServerIface    = BusName + ".Server"
	BrowserIface   = BusName + ".ServiceBrowser"
	serverPath     = dbus.ObjectPath("/")
	ifaceUnspec    = int32(-1)
	protoUnspec    = int32(-1)
	resolveTimeout = 5 * time.Second
)

// ErrNoAvahi is a system bus without Avahi on it.
var ErrNoAvahi = errors.New("dnssd: no Avahi on the system bus")

// Service is one resolved service: its instance name, type and domain,
// its host name, the address it is reached at (with its interface as
// the zone where link-local) and port,
// and its TXT record by key.
type Service struct {
	Name, Type, Domain string
	Host               string
	Address            netip.Addr
	Port               uint16
	TXT                map[string]string
}

// Key identifies a service instance across its announcements.
func (s Service) Key() string { return s.Type + "\x00" + s.Domain + "\x00" + s.Name }

// Client is an Avahi client on a system bus connection the caller owns.
type Client struct {
	conn *dbus.Conn
}

// New is a client on conn.
func New(conn *dbus.Conn) *Client { return &Client{conn: conn} }

// Browser is a browse running: Stop frees its Avahi browsers.
type Browser struct {
	conn     *dbus.Conn
	paths    []dbus.ObjectPath
	signals  chan *dbus.Signal
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// browserMatch is the signal rule a browse listens with, installed
// before its browsers exist, so their first items are not lost.
func browserMatch() []dbus.MatchOption {
	return []dbus.MatchOption{dbus.WithMatchSender(BusName), dbus.WithMatchInterface(BrowserIface)}
}

// Browse browses types, calling found with each service resolved and
// removed with the key of each that went, from one goroutine, until
// Stop. Without Avahi on the bus it is ErrNoAvahi.
func (c *Client) Browse(ctx context.Context, types []string, found func(Service), removed func(key string)) (*Browser, error) {
	var owned bool
	if err := c.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, BusName).Store(&owned); err != nil {
		return nil, fmt.Errorf("dnssd: asking for Avahi: %w", err)
	}
	if !owned {
		return nil, ErrNoAvahi
	}
	if err := c.conn.AddMatchSignal(browserMatch()...); err != nil {
		return nil, fmt.Errorf("dnssd: watching browsers: %w", err)
	}
	b := &Browser{conn: c.conn, signals: make(chan *dbus.Signal, 64), done: make(chan struct{})}
	c.conn.Signal(b.signals)
	server := c.conn.Object(BusName, serverPath)
	for _, t := range types {
		var path dbus.ObjectPath
		if err := server.CallWithContext(ctx, ServerIface+".ServiceBrowserNew", 0, ifaceUnspec, protoUnspec, t, "", uint32(0)).Store(&path); err != nil {
			return nil, errors.Join(fmt.Errorf("dnssd: browsing %s: %w", t, err), b.Stop())
		}
		b.paths = append(b.paths, path)
	}
	b.wg.Go(func() { b.run(server, found, removed) })
	return b, nil
}

// instance is one interface and protocol a service is announced on,
// as Avahi's browser items name them.
type instance struct{ iface, proto int32 }

// instanceOf reads a browser item's (interface, protocol, name, type,
// domain, flags) instance and the key of its service.
func instanceOf(body []any) (instance, string, bool) {
	if len(body) < 5 {
		return instance{}, "", false
	}
	iface, ok1 := body[0].(int32)
	proto, ok2 := body[1].(int32)
	name, ok3 := body[2].(string)
	typ, ok4 := body[3].(string)
	domain, ok5 := body[4].(string)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return instance{}, "", false
	}
	return instance{iface, proto}, Service{Name: name, Type: typ, Domain: domain}.Key(), true
}

// run hands over what the browsers announce until Stop. A service is
// announced once per interface and protocol, each resolved to its own
// address; found is told the best of them whenever one comes or goes,
// and removed once the last went.
func (b *Browser) run(server dbus.BusObject, found func(Service), removed func(string)) {
	services := map[string]map[instance]Service{}
	for {
		select {
		case <-b.done:
			return
		case sig, ok := <-b.signals:
			if !ok {
				return
			}
			if !b.ours(sig.Path) {
				continue
			}
			inst, key, ok := instanceOf(sig.Body)
			if !ok {
				continue
			}
			switch sig.Name {
			case BrowserIface + ".ItemNew":
				s, ok := b.resolve(server, inst, sig.Body)
				if !ok {
					continue
				}
				if services[key] == nil {
					services[key] = map[instance]Service{}
				}
				services[key][inst] = s
				found(best(services[key]))
			case BrowserIface + ".ItemRemove":
				delete(services[key], inst)
				if len(services[key]) == 0 {
					delete(services, key)
					removed(key)
					continue
				}
				found(best(services[key]))
			}
		}
	}
}

// best is the instance to reach a service at: an IPv4 address, else a
// routable IPv6 one, else a link-local one; the lowest interface and
// protocol break a tie.
func best(instances map[instance]Service) Service {
	rank := func(a netip.Addr) int {
		switch {
		case a.Is4():
			return 0
		case !a.IsLinkLocalUnicast():
			return 1
		}
		return 2
	}
	return instances[slices.MinFunc(slices.Collect(maps.Keys(instances)), func(a, b instance) int {
		return cmp.Or(cmp.Compare(rank(instances[a].Address), rank(instances[b].Address)), cmp.Compare(a.iface, b.iface), cmp.Compare(a.proto, b.proto))
	})]
}

func (b *Browser) ours(path dbus.ObjectPath) bool { return slices.Contains(b.paths, path) }

// resolve resolves a browser item with Avahi's ResolveService, on its
// interface and protocol, to an address of that protocol: a link-local
// IPv6 one carries its interface as its zone, without which it cannot
// be reached.
func (b *Browser) resolve(server dbus.BusObject, inst instance, body []any) (Service, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	go func() {
		select {
		case <-b.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var (
		iface, proto, aproto int32
		name, typ, domain    string
		host, address        string
		port                 uint16
		txt                  [][]byte
		flags                uint32
	)
	err := server.CallWithContext(ctx, ServerIface+".ResolveService", 0, inst.iface, inst.proto, body[2], body[3], body[4], inst.proto, uint32(0)).
		Store(&iface, &proto, &name, &typ, &domain, &host, &aproto, &address, &port, &txt, &flags)
	if err != nil {
		return Service{}, false
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return Service{}, false
	}
	if addr.Is6() && addr.IsLinkLocalUnicast() {
		ifc, err := net.InterfaceByIndex(int(iface))
		if err != nil {
			return Service{}, false
		}
		addr = addr.WithZone(ifc.Name)
	}
	return Service{Name: name, Type: typ, Domain: domain, Host: host, Address: addr, Port: port, TXT: txtRecord(txt)}, true
}

// txtRecord reads TXT strings, key=value each, the first of a key
// winning, as RFC 6763 says.
func txtRecord(txt [][]byte) map[string]string {
	out := make(map[string]string, len(txt))
	for _, entry := range txt {
		k, v, _ := strings.Cut(string(entry), "=")
		if k == "" {
			continue
		}
		if _, seen := out[k]; !seen {
			out[k] = v
		}
	}
	return out
}

// Stop frees the browsers and waits for the last service handed over;
// a browser Avahi would not free is the error.
func (b *Browser) Stop() error {
	var errs []error
	b.stopOnce.Do(func() {
		close(b.done)
		b.wg.Wait()
		for _, p := range b.paths {
			ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
			if err := b.conn.Object(BusName, p).CallWithContext(ctx, BrowserIface+".Free", 0).Err; err != nil {
				errs = append(errs, fmt.Errorf("dnssd: freeing %s: %w", p, err))
			}
			cancel()
		}
		if err := b.conn.RemoveMatchSignal(browserMatch()...); err != nil {
			errs = append(errs, fmt.Errorf("dnssd: unwatching browsers: %w", err))
		}
		b.conn.RemoveSignal(b.signals)
	})
	return errors.Join(errs...)
}
