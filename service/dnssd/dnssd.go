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
	"log"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
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
	watch    *dbusx.Watch
	done     chan struct{}
	stopOnce sync.Once
	stopErr  error
}

// browserItem is what a browser's ItemNew and ItemRemove carry: the
// interface and protocol a service is announced on, and the service.
type browserItem struct {
	Interface int32  `dbus:"interface"`
	Protocol  int32  `dbus:"protocol"`
	Name      string `dbus:"name"`
	Type      string `dbus:"type"`
	Domain    string `dbus:"domain"`
	Flags     uint32 `dbus:"flags"`
}

func (i browserItem) instance() instance { return instance{i.Interface, i.Protocol} }

func (i browserItem) key() string {
	return Service{Name: i.Name, Type: i.Type, Domain: i.Domain}.Key()
}

var (
	itemNew    = dbusx.NewSignal[browserItem](BrowserIface, "ItemNew")
	itemRemove = dbusx.NewSignal[browserItem](BrowserIface, "ItemRemove")
)

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
	b := &Browser{conn: c.conn, done: make(chan struct{})}
	server := c.conn.Object(BusName, serverPath)
	created := make(chan struct{})
	watch, err := dbusx.WatchSignals(context.WithoutCancel(ctx), c.conn, dbusx.FromOwner(BusName),
		func(err error) { log.Printf("dnssd: %v", err) },
		b.routes(created, server, found, removed)...)
	if err != nil {
		return nil, fmt.Errorf("dnssd: watching browsers: %w", err)
	}
	b.watch = watch
	err = b.create(ctx, server, types)
	close(created)
	if err != nil {
		return nil, errors.Join(err, b.Stop())
	}
	return b, nil
}

// create makes a browser of each type. Avahi names a browser
// /Client<n>/ServiceBrowser<m>, n its own number for the connection,
// so no namespace holds a browse's browsers before the first is made:
// the watch hears every browser and takes the items of these alone.
func (b *Browser) create(ctx context.Context, server dbus.BusObject, types []string) error {
	for _, t := range types {
		var path dbus.ObjectPath
		if err := server.CallWithContext(ctx, ServerIface+".ServiceBrowserNew", 0, ifaceUnspec, protoUnspec, t, "", uint32(0)).Store(&path); err != nil {
			return fmt.Errorf("dnssd: browsing %s: %w", t, err)
		}
		b.paths = append(b.paths, path)
	}
	return nil
}

// instance is one interface and protocol a service is announced on,
// as Avahi's browser items name them.
type instance struct{ iface, proto int32 }

// routes take what the browsers announce once created is closed. A
// service is announced once per interface and protocol, each resolved
// to its own address; found is told the best of them whenever one
// comes or goes, and removed once the last went.
func (b *Browser) routes(created <-chan struct{}, server dbus.BusObject, found func(Service), removed func(string)) []dbusx.Route {
	services := map[string]map[instance]Service{}
	return []dbusx.Route{
		dbusx.OnUnder(itemNew, "/", func(path dbus.ObjectPath, item browserItem, _ dbus.Sequence) error {
			<-created
			if !b.ours(path) {
				return nil
			}
			s, ok := b.resolve(server, item)
			if !ok {
				return nil
			}
			key := item.key()
			if services[key] == nil {
				services[key] = map[instance]Service{}
			}
			services[key][item.instance()] = s
			found(best(services[key]))
			return nil
		}),
		dbusx.OnUnder(itemRemove, "/", func(path dbus.ObjectPath, item browserItem, _ dbus.Sequence) error {
			<-created
			if !b.ours(path) {
				return nil
			}
			key := item.key()
			delete(services[key], item.instance())
			if len(services[key]) == 0 {
				delete(services, key)
				removed(key)
				return nil
			}
			found(best(services[key]))
			return nil
		}),
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
func (b *Browser) resolve(server dbus.BusObject, item browserItem) (Service, bool) {
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
	err := server.CallWithContext(ctx, ServerIface+".ResolveService", 0, item.Interface, item.Protocol, item.Name, item.Type, item.Domain, item.Protocol, uint32(0)).
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
// a browser Avahi would not free is the error. Calling it again
// reports the same.
func (b *Browser) Stop() error {
	b.stopOnce.Do(func() {
		close(b.done)
		var errs []error
		if err := b.watch.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("dnssd: stop following browsers: %w", err))
		}
		for _, p := range b.paths {
			ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
			if err := b.conn.Object(BusName, p).CallWithContext(ctx, BrowserIface+".Free", 0).Err; err != nil {
				errs = append(errs, fmt.Errorf("dnssd: freeing %s: %w", p, err))
			}
			cancel()
		}
		b.stopErr = errors.Join(errs...)
	})
	return b.stopErr
}
