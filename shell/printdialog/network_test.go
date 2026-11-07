package printdialog

import (
	"bytes"
	"log"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/ipp"
	"github.com/stubbedev/lys-shell/internal/ipp/ipptest"
	"github.com/stubbedev/lys-shell/service/dnssd"
	"github.com/stubbedev/lys-shell/service/dnssd/dnssdtest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

const laserUUID = "6e0a8f10-1234-4abc-9def-00a0c0ffee01"

// laser is a driverless printer announced over ipps on IPv4 loopback,
// its own attributes offering A5 only.
func laser(t *testing.T) (dnssd.Service, *ipptest.Server) {
	t.Helper()
	return laserAt(t, "127.0.0.1:0")
}

// laserAt is laser listening at addr, announced at its address.
func laserAt(t *testing.T, addr string) (dnssd.Service, *ipptest.Server) {
	t.Helper()
	device := ipptest.StartDeviceAt(t, addr, true, ipp.String(ipp.TagName, "printer-name", "laser"),
		ipp.Enum("printer-state", ipp.StateIdle),
		ipp.String(ipp.TagKeyword, "media-supported", "iso_a5_148x210mm"),
		ipp.String(ipp.TagKeyword, "media-default", "iso_a5_148x210mm"))
	at, err := netip.ParseAddrPort(device.Addr)
	if err != nil {
		t.Fatal(err)
	}
	return dnssd.Service{
		Name: "Example Laser", Type: ippsService, Domain: "local", Host: at.Addr().String(), Address: at.Addr(), Port: at.Port(),
		TXT: map[string]string{"ty": "Example Laser", "note": "Lab", "rp": "ipp/print", "UUID": laserUUID, "pdl": "application/octet-stream,image/pwg-raster"},
	}, device
}

// overNetwork points the harness at a fake scheduler and a fake Avahi,
// reading devices over IPP.
func overNetwork(t *testing.T, hs *harness) (*ipptest.Server, *fakeAvahi) {
	t.Helper()
	srv := overIPP(t, hs, time.Hour)
	srv.SetPrinter("office", idlePrinter()...)
	avahi := &fakeAvahi{dnssdtest.Start(t)}
	client := dnssd.New(avahi.Bus.Conn(t))
	hs.h.d.DNSSD = func() *dnssd.Client { return client }
	hs.h.d.Device = ipp.DeviceAttributes
	return srv, avahi
}

// fakeAvahi is the fake Avahi a test browses.
type fakeAvahi struct{ *dnssdtest.Avahi }

// waitLabel waits until the dialog shows a label reading text.
func (hs *harness) waitLabel(t *testing.T, c app.LayerConfig, text string) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(2 * time.Millisecond) {
		var labels []string
		hs.do(func() { labels = treetest.Labels(c.Root) })
		if slices.Contains(labels, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q among %q", text, labels)
		}
	}
}

// A driverless printer found over DNS-SD, at an IPv4 or an IPv6
// address, is listed, offers what it reports itself, and prints
// through the temporary queue CUPS makes for it; closing the dialog
// frees every browser.
func TestNetworkPrintersAreListedAndPrintedTo(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(addr, func(t *testing.T) { listedAndPrintedTo(t, addr) })
	}
}

func listedAndPrintedTo(t *testing.T, addr string) {
	hs := newHarness(t)
	srv, avahi := overNetwork(t, hs)
	svc, device := laserAt(t, addr)
	avahi.Announce(t, svc)
	got, c := hs.prepare(t)
	hs.waitLabel(t, c, "Example Laser (network)")
	hs.waitLabel(t, c, "Lab")
	dropdowns := treetest.All[*widget.Dropdown](c.Root)
	hs.do(func() {
		i := slices.IndexFunc(hs.h.sheet.queues, func(q queue) bool { return q.kind == queueNetwork })
		treetest.First[*widget.ListBox](t, c.Root).Select(i)
	})
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(2 * time.Millisecond) {
		var papers []string
		hs.do(func() { papers = dropdowns[dropPaper].Items() })
		if slices.Equal(papers, []string{"A5"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the network printer offers %v, want its own A5", papers)
		}
	}
	if ops := device.Ops(); !slices.Contains(ops, ipp.OpGetPrinterAttributes) {
		t.Errorf("the printer itself was asked %v", ops)
	}
	hs.do(func() { treetest.Button(t, c.Root, "Print").OnClick() })
	r := <-got
	if !r.ok || !slices.Contains(r.settings, Setting{"printer", "Example Laser (network)"}) {
		t.Fatalf("prepare = %+v", r)
	}
	avahi.waitFreed(t)
	if avahi.Created.Load() != 2 {
		t.Errorf("%d browsers made, want one each for ipps and ipp", avahi.Created.Load())
	}
	if !hs.h.Print(testCaller, "Doc", document(t, "%PDF net"), r.token) {
		t.Fatal("printing to the network printer failed")
	}
	ops := srv.Ops()
	create := slices.Index(ops, ipp.OpCUPSCreateLocalPrinter)
	if create < 0 || slices.Index(ops[create:], ipp.OpPrintJob) < 0 {
		t.Errorf("CUPS was asked %v, want Create-Local-Printer then Print-Job", ops)
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 || jobs[0].Printer != "Example_Laser" || string(jobs[0].Document) != "%PDF net" {
		t.Fatalf("jobs %+v", jobs)
	}
	if uri := srv.Created("Example_Laser"); uri != "ipps://"+net.JoinHostPort(svc.Host, strconv.Itoa(int(svc.Port)))+"/ipp/print" {
		t.Errorf("the queue was made for %q", uri)
	}
}

// A network printer that is a CUPS queue already is listed once, as
// the queue; one that needs a driver is not listed.
func TestNetworkPrintersAreNotListedTwice(t *testing.T) {
	hs := newHarness(t)
	srv, avahi := overNetwork(t, hs)
	srv.SetPrinter("lab-laser", idlePrinter(ipp.String(ipp.TagURI, "printer-uuid", "urn:uuid:"+strings.ToUpper(laserUUID)))...)
	svc, _ := laser(t)
	avahi.Announce(t, svc)
	needsDriver := svc
	needsDriver.Name, needsDriver.TXT = "Old Inkjet", map[string]string{"ty": "Old Inkjet", "pdl": "application/vnd.hp-PCL"}
	avahi.Announce(t, needsDriver)
	got, c := hs.prepare(t)
	for deadline := time.Now().Add(treetest.Liveness); ; time.Sleep(2 * time.Millisecond) {
		var found int
		hs.do(func() { found = len(hs.h.sheet.network) })
		if found == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the laser was never found")
		}
	}
	hs.do(func() {
		if labels := treetest.Labels(c.Root); slices.Contains(labels, "Example Laser (network)") || slices.Contains(labels, "Old Inkjet (network)") {
			t.Errorf("listed twice or needing a driver: %q", labels)
		}
		var names []string
		for _, q := range hs.h.sheet.queues {
			names = append(names, q.name)
		}
		if !slices.Equal(names, []string{"office", "lab-laser", fileQueue}) {
			t.Errorf("queues %v", names)
		}
		treetest.Button(t, c.Root, "Cancel").OnClick()
	})
	<-got
	avahi.waitFreed(t)
}

// waitFreed waits until every browser made was freed.
func (a *fakeAvahi) waitFreed(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); a.Freed.Load() != a.Created.Load(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d browsers made, %d freed after the dialog closed", a.Created.Load(), a.Freed.Load())
		}
	}
}

// logLines captures the log for the test.
type logLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logLines) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.buf.String(), sub)
}

// Without Avahi on the system bus the list is the CUPS queues and
// Print to File, and one line says so.
func TestNoAvahiListsTheQueues(t *testing.T) {
	hs := newHarness(t)
	srv := overIPP(t, hs, time.Hour)
	srv.SetPrinter("office", idlePrinter()...)
	client := dnssd.New(dbustest.Start(t).Conn(t))
	hs.h.d.DNSSD = func() *dnssd.Client { return client }
	logs := &logLines{}
	prev := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(prev) })
	got, c := hs.prepare(t)
	for deadline := time.Now().Add(treetest.Liveness); logs.count("no Avahi") == 0; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("nothing said Avahi is missing")
		}
	}
	hs.do(func() {
		var names []string
		for _, q := range hs.h.sheet.queues {
			names = append(names, q.name)
		}
		if !slices.Equal(names, []string{"office", fileQueue}) {
			t.Errorf("queues %v", names)
		}
		treetest.Button(t, c.Root, "Cancel").OnClick()
	})
	<-got
	if n := logs.count("print:"); n != 1 {
		t.Errorf("%d print log lines, want the one about Avahi:\n%s", n, logs.buf.String())
	}
}

// A link-local IPv6 printer is read at a URI carrying its zone,
// escaped, and the printer answers there; CUPS reaches it by host
// name.
func TestLinkLocalPrintersAreReadThroughTheirZone(t *testing.T) {
	svc, device := laserAt(t, "[::1]:0")
	svc.Host = "laser.local."
	ll := svc
	ll.Address = netip.MustParseAddr("fe80::7%lo")
	q, ok := networkQueue(ll)
	port := strconv.Itoa(int(svc.Port))
	if !ok || q.query != "ipps://[fe80::7%25lo]:"+port+"/ipp/print" || q.device != "ipps://laser.local:"+port+"/ipp/print" {
		t.Fatalf("the link-local printer is read at %q and printed to at %q", q.query, q.device)
	}
	svc.Address = netip.MustParseAddr("::1%lo")
	q, _ = networkQueue(svc)
	p, err := ipp.DeviceAttributes(t.Context(), q.query)
	if err != nil {
		t.Fatalf("reading %s: %v", q.query, err)
	}
	if !slices.Equal(p.Media, []string{"iso_a5_148x210mm"}) || !slices.Contains(device.Ops(), ipp.OpGetPrinterAttributes) {
		t.Errorf("the printer at %s answered %+v", q.query, p)
	}
}
