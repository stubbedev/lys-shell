package printdialog

import (
	"context"
	"errors"
	"log"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/service/dnssd"
)

// The DNS-SD types an IPP printer announces itself as, the secure one
// preferred.
const (
	ippsService = "_ipps._tcp"
	ippService  = "_ipp._tcp"
)

// driverlessFormats are the document formats a printer prints without
// a driver, one of which IPP Everywhere and AirPrint printers list in
// their TXT pdl.
var driverlessFormats = []string{"image/pwg-raster", "image/urf", "application/pdf"}

// fileQueueRow is Print to File's row.
func fileQueueRow() queue {
	return queue{kind: queueFile, name: fileQueue, caps: capabilitiesOf(nil)}
}

// browse looks for driverless printers on the network while the sheet
// is open; without Avahi the list is the scheduler's, and one line says
// why.
func (s *sheet) browse() {
	var client *dnssd.Client
	if s.h.d.DNSSD != nil {
		client = s.h.d.DNSSD()
	}
	if client == nil {
		log.Print("print: no system bus; network printers are not browsed")
		return
	}
	ctx := s.ctx
	go func() {
		b, err := client.Browse(ctx, []string{ippsService, ippService},
			func(svc dnssd.Service) { s.h.d.Invoke(func() { s.discovered(svc) }) },
			func(key string) { s.h.d.Invoke(func() { s.lost(key) }) })
		switch {
		case errors.Is(err, dnssd.ErrNoAvahi):
			log.Print("print: no Avahi on the system bus; network printers are not browsed")
			return
		case err != nil:
			log.Printf("print: browsing for network printers: %v", err)
			return
		}
		s.h.d.Invoke(func() {
			if s.ctx.Err() != nil {
				stopBrowsing(b)
				return
			}
			s.browser = b
		})
	}()
}

// stopBrowsing frees a browse's Avahi browsers off the loop.
func stopBrowsing(b *dnssd.Browser) {
	go func() {
		if err := b.Stop(); err != nil {
			log.Printf("print: %v", err)
		}
	}()
}

// networkQueue is a DNS-SD IPP printer as a row, false for one that
// needs a driver: its TXT ty as the name, its note as the detail, the
// device URI CUPS reaches it at by host name, and the URI its
// attributes are read at by address.
func networkQueue(svc dnssd.Service) (queue, bool) {
	pdl := strings.Split(svc.TXT["pdl"], ",")
	if !slices.ContainsFunc(driverlessFormats, func(f string) bool { return slices.Contains(pdl, f) }) {
		return queue{}, false
	}
	scheme := "ipp"
	if svc.Type == ippsService {
		scheme = "ipps"
	}
	rp := strings.TrimPrefix(svc.TXT["rp"], "/")
	if _, ok := svc.TXT["rp"]; !ok {
		rp = "ipp/print"
	}
	port := strconv.Itoa(int(svc.Port))
	at := func(host string) string {
		return (&url.URL{Scheme: scheme, Host: host, Path: "/" + rp}).String()
	}
	host := strings.TrimSuffix(svc.Host, ".")
	name := svc.TXT["ty"]
	if name == "" {
		name = svc.Name
	}
	return queue{
		kind: queueNetwork, name: name + " (network)", detail: svc.TXT["note"], caps: capabilitiesOf(nil),
		uuid: svc.TXT["UUID"], key: svc.Key(),
		device: at(net.JoinHostPort(host, port)),
		query:  at(netip.AddrPortFrom(svc.Address, svc.Port).String()),
	}, true
}

// sameUUID reports two printer UUIDs naming one printer, written
// either as a URN or bare.
func sameUUID(a, b string) bool {
	bare := func(u string) string { return strings.ToLower(strings.TrimPrefix(strings.ToLower(u), "urn:uuid:")) }
	return a != "" && b != "" && bare(a) == bare(b)
}

// samePrinter reports a network printer that is the queue q: its UUID,
// or the URI CUPS sends q's jobs to.
func samePrinter(n, q queue) bool {
	return sameUUID(n.uuid, q.uuid) || (q.device != "" && (q.device == n.device || q.device == n.query))
}

// discovered adds a network printer, unless a CUPS queue is it already;
// found over both ipp and ipps, the secure one stays.
func (s *sheet) discovered(svc dnssd.Service) {
	n, ok := networkQueue(svc)
	if s.ctx.Err() != nil || !ok {
		return
	}
	if i := slices.IndexFunc(s.network, func(o queue) bool { return o.key == n.key || sameUUID(o.uuid, n.uuid) }); i >= 0 {
		if strings.HasPrefix(s.network[i].device, "ipps:") && !strings.HasPrefix(n.device, "ipps:") {
			return
		}
		s.network[i] = n
	} else {
		s.network = append(s.network, n)
	}
	s.relist()
}

// lost drops a network printer that left.
func (s *sheet) lost(key string) {
	if s.ctx.Err() != nil {
		return
	}
	s.network = slices.DeleteFunc(s.network, func(q queue) bool { return q.key == key })
	s.relist()
}

// relist lists the queues again where the selection stands.
func (s *sheet) relist() {
	want := fileQueue
	if q, ok := s.selected(); ok {
		want = q.name
	}
	s.setQueues(s.compose(), want, s.loading)
}

// compose is the list: the CUPS queues, the network printers no queue
// is already, Print to File.
func (s *sheet) compose() []queue {
	out := slices.Clone(s.cupsQueues)
	for _, n := range s.network {
		if !slices.ContainsFunc(s.cupsQueues, func(q queue) bool { return samePrinter(n, q) }) {
			out = append(out, n)
		}
	}
	return append(out, fileQueueRow())
}

// readCaps reads a network printer's capabilities from the printer
// itself, off the loop, and offers them once read if it is still
// selected.
func (s *sheet) readCaps(q queue) {
	if s.h.d.Device == nil {
		return
	}
	device, key, query := s.h.d.Device, q.key, q.query
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, listTimeout)
		defer cancel()
		p, err := device(ctx, query)
		s.h.d.Invoke(func() {
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("print: reading %s: %v", query, err)
				return
			}
			caps := capabilitiesOf(&p)
			for _, list := range [][]queue{s.network, s.queues} {
				if i := slices.IndexFunc(list, func(o queue) bool { return o.key == key }); i >= 0 {
					list[i].caps, list[i].capsRead = caps, true
				}
			}
			if sel, ok := s.selected(); ok && sel.key == key {
				s.offer(caps, s.form())
			}
		})
	}()
}

// localQueueName is a CUPS queue name for a network printer's label:
// letters, digits, - and _ only.
func localQueueName(label string) string {
	label = strings.TrimSuffix(label, " (network)")
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "network-printer"
	}
	return name
}
