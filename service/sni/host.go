package sni

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
)

// D-Bus names for the SNI protocol.
const (
	WatcherName  = "org.kde.StatusNotifierWatcher"
	WatcherPath  = "/StatusNotifierWatcher"
	WatcherIface = "org.kde.StatusNotifierWatcher"
	HostIface    = "org.kde.StatusNotifierHost"
	ItemIface    = "org.kde.StatusNotifierItem"
	PropsIface   = "org.freedesktop.DBus.Properties"
	// ProtocolVersion is the watcher's advertised protocol version.
	ProtocolVersion = int32(0)
)

// Discovery cadences (watcher/discovery.rs, core/item/monitoring.rs).
var (
	// orphanScanSchedule re-scans the bus for items that registered with
	// a previous watcher (a shell restart) and never re-register.
	orphanScanSchedule = []time.Duration{0, time.Second, 3 * time.Second, 8 * time.Second, 20 * time.Second}
	probeTimeout       = 500 * time.Millisecond
	// menuDebounce coalesces a burst of DBusMenu change signals into
	// one refetch.
	menuDebounce = 100 * time.Millisecond
)

const (
	busIface          = "org.freedesktop.DBus"
	errNameHasNoOwner = busIface + ".Error.NameHasNoOwner"
)

// answerTimeout is how long a peer, a tray item or an external watcher,
// has to answer a call of the host's: the D-Bus default.
const answerTimeout = 25 * time.Second

// Host plays the host role against a watcher (ours or an existing
// one), tracks items and their property changes, and hands out the
// item controls.
type Host struct {
	conn  *dbus.Conn
	store *Store

	owned   bool
	watcher *watcher // non-nil: we serve the watcher role ourselves
	// external is the watcher another process serves, which the signal
	// loop follows when watcher is nil.
	external externalWatcher
	// ctx ends with Close, and with it every call under way and
	// everything the host started, which work counts.
	ctx    context.Context
	cancel context.CancelFunc
	work   sync.WaitGroup
	// timeout is how long a peer has to answer, and failed is told what
	// could not be read of an item or of an external watcher, on the
	// goroutine that found out.
	timeout time.Duration
	failed  func(error)
	// names follows the bus names that lose their owner, each handed to
	// the signal loop through vanished.
	names    *dbusx.Watch
	vanished chan nameLoss
	feed     []dbusx.Route
	// tracks hands the signal loop the items to read from the goroutines
	// that find them, and results what was read of each: items are
	// tracked and forgotten on the signal loop alone.
	tracks  chan string
	results chan reading
	// reads are the reads under way, by item key, and owners maps each
	// tracked item's key to its connection's unique name, so a signal's
	// sender resolves to the items it belongs to. Both are the signal
	// loop's own.
	reads  map[string]*read
	owners map[string]string

	mu sync.Mutex
	// menus are the open menus' change feeds, by item key.
	menus map[string][]chan struct{}
	// pixmapSizes counts the consumers wanting each pixmap size, in
	// device pixels; retrack asks the signal loop to re-read every item
	// when the set of sizes changes.
	pixmapSizes map[int]int
	retrack     chan struct{}
	matched     int
	exported    bool
}

// NewHostOn runs the host on conn, which stays the caller's, and claims
// the roles: when no watcher is running, this process becomes the
// watcher and hosts against itself.
func NewHostOn(conn *dbus.Conn, store *Store) (*Host, error) {
	return newHost(conn, store, answerTimeout, func(err error) { log.Printf("sni: %v", err) })
}

func newHost(conn *dbus.Conn, store *Store, timeout time.Duration, failed func(error)) (*Host, error) {
	h := idleHost(conn, store, timeout, failed)
	signals := make(chan *dbus.Signal, 64)
	conn.Signal(signals)
	if err := h.claim(); err != nil {
		h.cancel()
		bussignal.Remove(conn, signals)
		return nil, errors.Join(err, h.release())
	}
	h.feed = h.externalFeed()
	h.work.Go(func() { h.run(signals) })
	if h.watcher == nil {
		h.work.Go(h.resync)
	} else {
		h.work.Go(h.orphanScan)
	}
	return h, nil
}

// idleHost is a host that has claimed no role and runs no signal loop
// yet.
func idleHost(conn *dbus.Conn, store *Store, timeout time.Duration, failed func(error)) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{
		conn:        conn,
		store:       store,
		ctx:         ctx,
		cancel:      cancel,
		timeout:     timeout,
		failed:      failed,
		vanished:    make(chan nameLoss),
		tracks:      make(chan string),
		results:     make(chan reading),
		reads:       make(map[string]*read),
		owners:      make(map[string]string),
		menus:       make(map[string][]chan struct{}),
		pixmapSizes: make(map[int]int),
		retrack:     make(chan struct{}, 1),
	}
}

// answer is the context of the calls of one exchange with a peer: it
// ends with the host, and once the peer has had the timeout to answer.
func (h *Host) answer() (context.Context, context.CancelFunc) {
	return context.WithTimeout(h.ctx, h.timeout)
}

func (h *Host) claim() error {
	if err := h.subscribe(); err != nil {
		return err
	}
	if err := h.claimWatcher(); err != nil {
		return err
	}
	if h.watcher == nil {
		return h.registerHost()
	}
	return nil
}

// Actions returns the item controls on the host's connection.
func (h *Host) Actions() *Actions { return &Actions{conn: h.conn} }

// claimWatcher exports the watcher object and requests its name when
// nobody owns it; h.watcher stays nil when a watcher already runs.
func (h *Host) claimWatcher() error {
	var owner string
	call := h.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, WatcherName)
	if err := call.Store(&owner); err == nil && owner != "" {
		h.external = externalWatcher{owner: owner, asOf: call.ResponseSequence}
		return nil
	}
	w := newWatcher(h)
	h.exported = true
	if err := h.conn.Export(w, WatcherPath, WatcherIface); err != nil {
		return fmt.Errorf("sni: export watcher: %w", err)
	}
	if err := h.conn.Export(w, WatcherPath, PropsIface); err != nil {
		return fmt.Errorf("sni: export watcher properties: %w", err)
	}
	reply, err := h.conn.RequestName(WatcherName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("sni: request watcher name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil
	}
	h.owned = true
	h.watcher = w
	return nil
}

// registerHost announces the host to an external watcher.
func (h *Host) registerHost() error {
	ctx, cancel := h.answer()
	defer cancel()
	const method = WatcherIface + ".RegisterStatusNotifierHost"
	if err := h.conn.Object(WatcherName, WatcherPath).CallWithContext(ctx, method, 0, h.conn.Names()[0]).Err; err != nil {
		return fmt.Errorf("sni: %s within %s: %w", method, h.timeout, err)
	}
	return nil
}

// subscribe matches the watcher's registration signals, every item's
// change signals and PropertiesChanged and the menus' change signals,
// and watches the bus names that lose their owner (for the watcher role
// and item exits).
func (h *Host) subscribe() error {
	for _, rule := range hostMatches() {
		if err := h.conn.AddMatchSignal(rule...); err != nil {
			return fmt.Errorf("sni: match: %w", err)
		}
		h.matched++
	}
	names, err := dbusx.WatchSignals(context.Background(), h.conn, dbusx.FromBus(),
		func(err error) { log.Printf("sni: %v", err) },
		bussignal.OnNameLost(func(name string, announced dbus.Sequence) error {
			select {
			case h.vanished <- nameLoss{name: name, announced: announced}:
			case <-h.ctx.Done():
			}
			return nil
		}))
	if err != nil {
		return fmt.Errorf("sni: %w", err)
	}
	h.names = names
	return nil
}

func hostMatches() [][]dbus.MatchOption {
	return [][]dbus.MatchOption{
		{dbus.WithMatchSender(WatcherName), dbus.WithMatchObjectPath(WatcherPath), dbus.WithMatchInterface(WatcherIface)},
		dbusx.OwnerMatch(WatcherName),
		{dbus.WithMatchInterface(ItemIface)},
		{dbus.WithMatchInterface(PropsIface), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, ItemIface)},
		{dbus.WithMatchInterface(MenuIface)},
	}
}

func (h *Host) release() error {
	var errs []error
	if h.names != nil {
		if err := h.names.Stop(); err != nil && !errors.Is(err, dbusx.ErrDisconnected) {
			errs = append(errs, fmt.Errorf("sni: %w", err))
		}
		h.names = nil
	}
	for _, rule := range hostMatches()[:h.matched] {
		if err := h.conn.RemoveMatchSignal(rule...); err != nil {
			errs = append(errs, fmt.Errorf("sni: remove match: %w", err))
		}
	}
	h.matched = 0
	if h.owned {
		if _, err := h.conn.ReleaseName(WatcherName); err != nil {
			errs = append(errs, fmt.Errorf("sni: release watcher name: %w", err))
		}
		h.owned = false
	}
	if h.exported {
		for _, iface := range []string{WatcherIface, PropsIface} {
			if err := h.conn.Export(nil, WatcherPath, iface); err != nil {
				errs = append(errs, fmt.Errorf("sni: unexport watcher: %w", err))
			}
		}
		h.exported = false
	}
	return errors.Join(errs...)
}

// run is the signal loop: it consumes the signals, the vanished names,
// the items to read and what was read of them until Close or the end
// of the connection, which ends the host's other work with it.
func (h *Host) run(signals chan *dbus.Signal) {
	defer bussignal.Remove(h.conn, signals)
	defer h.cancel()
	for {
		select {
		case <-h.ctx.Done():
			return
		case sig, ok := <-signals:
			if !ok {
				return
			}
			h.handle(sig)
		case loss := <-h.vanished:
			h.nameLost(loss)
		case reg := <-h.tracks:
			h.track(reg)
		case res := <-h.results:
			h.finish(res)
		case <-h.retrack:
			for _, it := range h.store.Items() {
				h.track(it.Bus + it.Path)
			}
		}
	}
}

// WantPixmapSize asks the host to keep, of every pixmap property, the
// pixmap BestPixmap picks for px device pixels, until release. Items
// are read again whenever the set of wanted sizes changes, so a new
// size is served at once and a dropped one frees its pixmaps. It
// panics on a non-positive size, a programming error.
func (h *Host) WantPixmapSize(px int) (release func()) {
	if px <= 0 {
		panic(fmt.Sprintf("sni: invalid pixmap size %d", px))
	}
	h.mu.Lock()
	h.pixmapSizes[px]++
	added := h.pixmapSizes[px] == 1
	h.mu.Unlock()
	if added {
		h.requestRetrack()
	}
	return sync.OnceFunc(func() {
		h.mu.Lock()
		h.pixmapSizes[px]--
		removed := h.pixmapSizes[px] == 0
		if removed {
			delete(h.pixmapSizes, px)
		}
		h.mu.Unlock()
		if removed {
			h.requestRetrack()
		}
	})
}

func (h *Host) requestRetrack() {
	select {
	case h.retrack <- struct{}{}:
	default:
	}
}

// wantedPixmapSizes lists the wanted sizes, ascending.
func (h *Host) wantedPixmapSizes() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	sizes := make([]int, 0, len(h.pixmapSizes))
	for px := range h.pixmapSizes {
		sizes = append(sizes, px)
	}
	slices.Sort(sizes)
	return sizes
}

// externalWatcher is the owner of the watcher name another process
// serves, "" while nobody does, as of the bus's answer sequenced asOf.
type externalWatcher struct {
	owner string
	asOf  dbus.Sequence
}

// externalFeed routes an external watcher's registrations, and its name
// passing from one owner to the next: a watcher that takes the name
// over is told of the host and asked for its items.
func (h *Host) externalFeed() []dbusx.Route {
	return []dbusx.Route{
		dbusx.OnOthers(WatcherIface, WatcherPath, func(sig *dbus.Signal) error {
			reg, _ := firstString(sig.Body)
			switch sig.Name {
			case WatcherIface + ".StatusNotifierItemRegistered":
				h.track(reg)
			case WatcherIface + ".StatusNotifierItemUnregistered":
				bus, path := ParseAddress(reg)
				h.forget(bus, path)
			}
			return sig.CloseFDs()
		}),
		dbusx.OnOwnerChange(WatcherName, func(_, owner string, announced dbus.Sequence) error {
			if announced <= h.external.asOf {
				return nil
			}
			h.external.owner = owner
			if owner != "" {
				h.work.Go(h.rejoin)
			}
			return nil
		}),
	}
}

// rejoin announces the host to a watcher that took the name over and
// reads the items registered with it.
func (h *Host) rejoin() {
	if err := h.registerHost(); err != nil {
		if h.ctx.Err() == nil {
			h.failed(err)
		}
		return
	}
	h.resync()
}

// handle routes one signal and takes over the descriptors it carries.
// An external watcher's feed comes from whichever connection owns the
// watcher name; our own watcher tracks directly. The items' signals
// come from any connection at any path, which no route names yet
// (lys-lib#106).
func (h *Host) handle(sig *dbus.Signal) {
	member := sig.Name[strings.LastIndex(sig.Name, ".")+1:]
	iface := strings.TrimSuffix(sig.Name, "."+member)
	if h.watcher == nil && (iface == WatcherIface || iface == busIface) {
		if err := dbusx.Dispatch(sig, h.external.owner, h.feed); err != nil {
			h.failed(err)
		}
		return
	}
	defer bussignal.Discard(sig)
	switch iface {
	case ItemIface, PropsIface:
		// NewIcon, NewAttentionIcon, NewOverlayIcon, NewStatus, NewTitle,
		// NewToolTip, NewMenu, ... and PropertiesChanged: re-read the
		// item's snapshot.
		for _, key := range h.itemsOf(sig.Sender, string(sig.Path)) {
			h.track(key)
		}
	case MenuIface:
		// LayoutUpdated, ItemsPropertiesUpdated: open menus refetch.
		for _, key := range h.menuItemsOf(sig.Sender, string(sig.Path)) {
			h.notifyMenu(key)
		}
	}
}

// nameLoss is the bus announcing that a name lost its owner, and the
// announcement's sequence on the host's connection.
type nameLoss struct {
	name      string
	announced dbus.Sequence
}

// nameLost drops what a connection that left the bus registered, and
// has each read under way know of the loss: the item it brings may
// have answered before its name was lost.
func (h *Host) nameLost(loss nameLoss) {
	for _, r := range h.reads {
		r.lost = append(r.lost, loss)
	}
	if h.watcher != nil {
		h.watcher.nameVanished(loss)
		return
	}
	h.forgetName(loss.name)
}

// firstString is a one-string signal body.
func firstString(body []any) (string, bool) {
	if len(body) != 1 {
		return "", false
	}
	s, ok := body[0].(string)
	return s, ok
}

// itemsOf lists the tracked items a signal from sender at path is
// about.
func (h *Host) itemsOf(sender, path string) []string {
	var keys []string
	for key, owner := range h.owners {
		if owner != sender {
			continue
		}
		if it, ok := h.store.Get(key); ok && it.Path == path {
			keys = append(keys, key)
		}
	}
	return keys
}

// menuItemsOf lists the tracked items whose menu object a signal came
// from.
func (h *Host) menuItemsOf(sender, path string) []string {
	var keys []string
	for key, owner := range h.owners {
		if owner != sender {
			continue
		}
		if it, ok := h.store.Get(key); ok && it.MenuPath == path {
			keys = append(keys, key)
		}
	}
	return keys
}

// resync reads an external watcher's RegisteredStatusNotifierItems.
func (h *Host) resync() {
	ctx, cancel := h.answer()
	defer cancel()
	var regs []string
	if err := h.conn.Object(WatcherName, WatcherPath).CallWithContext(ctx, PropsIface+".Get", 0, WatcherIface, "RegisteredStatusNotifierItems").Store(&regs); err != nil {
		if h.ctx.Err() == nil {
			h.failed(fmt.Errorf("read the items registered with %s within %s: %w", WatcherName, h.timeout, err))
		}
		return
	}
	for _, reg := range regs {
		h.requestTrack(reg)
	}
}

// orphanScan is discovery.rs spawn_orphan_scan: on the schedule,
// probe every other unique name for a StatusNotifierItem and register
// the ones that answer - items that registered with a watcher that has
// since gone away do not re-register on their own.
func (h *Host) orphanScan() {
	for _, delay := range orphanScanSchedule {
		select {
		case <-h.ctx.Done():
			return
		case <-time.After(delay):
		}
		var names []string
		if err := h.conn.BusObject().CallWithContext(h.ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
			return
		}
		own := h.conn.Names()[0]
		for _, name := range names {
			if !strings.HasPrefix(name, ":") || name == own || h.watcher.registered(name) {
				continue
			}
			if h.probe(name) {
				h.watcher.register(name)
			}
		}
	}
}

// probe reports whether name serves a StatusNotifierItem at the
// default path (its Id reads within the probe timeout).
func (h *Host) probe(name string) bool {
	ctx, cancel := context.WithTimeout(h.ctx, probeTimeout)
	defer cancel()
	var v dbus.Variant
	return h.conn.Object(name, "/StatusNotifierItem").CallWithContext(ctx, PropsIface+".Get", 0, ItemIface, "Id").Store(&v) == nil
}

// read is one read of an item under way. again says the item was to be
// read once more meanwhile: what this read brings may predate that.
// lost are the names that lost their owner meanwhile.
type read struct {
	again bool
	lost  []nameLoss
}

// lostSince reports whether one of names lost its owner after the
// message sequenced answered: a sequence orders what the host's
// connection read, replies and announcements alike.
func (r *read) lostSince(answered dbus.Sequence, names ...string) bool {
	return slices.ContainsFunc(r.lost, func(loss nameLoss) bool {
		return loss.announced > answered && slices.Contains(names, loss.name)
	})
}

// reading is what a read brought back: the item, the sequence of its
// answer and the unique name of the connection serving it, or why it
// could not be read.
type reading struct {
	of        *read
	bus, path string
	item      *Item
	answered  dbus.Sequence
	owner     string
	err       error
}

// requestTrack has the signal loop track an item, from any other
// goroutine.
func (h *Host) requestTrack(reg string) {
	select {
	case h.tracks <- reg:
	case <-h.ctx.Done():
	}
}

// track starts reading one item, off the signal loop, which finish
// hears from. An item has one read under way at most: asked for again
// meanwhile, it is read once more when that one ends.
func (h *Host) track(reg string) {
	bus, path := ParseAddress(reg)
	if r, reading := h.reads[bus+path]; reading {
		r.again = true
		return
	}
	r := &read{}
	h.reads[bus+path] = r
	sizes := h.wantedPixmapSizes()
	h.work.Go(func() {
		res := h.read(bus, path, sizes)
		res.of = r
		select {
		case h.results <- res:
		case <-h.ctx.Done():
		}
	})
}

// read fetches one item and the owner of its bus name, which have the
// timeout to answer.
func (h *Host) read(bus, path string, sizes []int) reading {
	res := reading{bus: bus, path: path, owner: bus}
	ctx, cancel := h.answer()
	defer cancel()
	if res.item, res.answered, res.err = readItem(ctx, h.conn, bus, path, sizes); res.err == nil && !strings.HasPrefix(bus, ":") {
		res.owner, res.err = dbusx.NameOwner(ctx, h.conn, bus)
	}
	if res.err != nil {
		res.err = fmt.Errorf("read item %s%s within %s: %w", bus, path, h.timeout, res.err)
	}
	return res
}

// finish stores what a read brought back and remembers the item's
// owner, unless the item was forgotten while it was read, or its bus
// name or its owner left the bus after it answered: that loss is
// handled, and nothing would forget the item again. A read that failed
// is reported and leaves the item as it was.
func (h *Host) finish(res reading) {
	key := res.bus + res.path
	if h.reads[key] != res.of {
		return
	}
	delete(h.reads, key)
	switch {
	case res.err != nil:
		h.failed(res.err)
	case !res.of.lostSince(res.answered, res.bus, res.owner):
		h.owners[key] = res.owner
		h.store.Put(res.item)
	}
	if res.of.again {
		h.track(key)
	}
}

// forget drops one item, and what a read of it under way brings.
func (h *Host) forget(bus, path string) {
	delete(h.owners, bus+path)
	delete(h.reads, bus+path)
	h.store.Remove(bus, path)
}

// forgetName drops every item a vanished connection owned (an external
// watcher normally unregisters them, but a crashed item may not be).
func (h *Host) forgetName(name string) {
	for _, it := range h.store.Items() {
		if h.owners[it.Key()] == name || it.Bus == name {
			h.forget(it.Bus, it.Path)
		}
	}
}

// WatchMenu returns a feed that ticks (debounced) whenever the item's
// menu layout or properties change, for as long as a menu is open;
// stop releases it and closes the feed, once however often it is
// called.
func (h *Host) WatchMenu(key string) (<-chan struct{}, func()) {
	raw := make(chan struct{}, 1)
	out := make(chan struct{}, 1)
	h.mu.Lock()
	h.menus[key] = append(h.menus[key], raw)
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(out)
		var timer <-chan time.Time
		for {
			select {
			case <-done:
				return
			case <-raw:
				timer = time.After(menuDebounce)
			case <-timer:
				timer = nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	stop := sync.OnceFunc(func() {
		h.mu.Lock()
		feeds := h.menus[key]
		for i, ch := range feeds {
			if ch == raw {
				h.menus[key] = append(feeds[:i], feeds[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		close(done)
	})
	return out, stop
}

// notifyMenu ticks an item's open menu feeds.
func (h *Host) notifyMenu(key string) {
	h.mu.Lock()
	feeds := append([]chan struct{}(nil), h.menus[key]...)
	h.mu.Unlock()
	for _, ch := range feeds {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// readItem fetches the SNI property set, keeping of each pixmap
// property the pixmaps picked for sizes; answered is the sequence of
// the item's answer on conn.
func readItem(ctx context.Context, conn *dbus.Conn, bus, path string, sizes []int) (it *Item, answered dbus.Sequence, err error) {
	call := conn.Object(bus, dbus.ObjectPath(path)).CallWithContext(ctx, PropsIface+".GetAll", 0, ItemIface)
	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, dbus.NoSequence, fmt.Errorf("%s.GetAll: %w", PropsIface, err)
	}
	it = &Item{Bus: bus, Path: path}
	str := func(name string) string {
		if v, ok := props[name]; ok {
			switch s := v.Value().(type) {
			case string:
				return s
			case dbus.ObjectPath:
				return string(s)
			}
		}
		return ""
	}
	it.ID = str("Id")
	it.Title = str("Title")
	it.Category = ParseCategory(str("Category"))
	it.Status = ParseStatus(str("Status"))
	if v, ok := props["ItemIsMenu"]; ok {
		it.ItemIsMenu, _ = v.Value().(bool)
	}
	it.IconName = str("IconName")
	it.OverlayIconName = str("OverlayIconName")
	it.AttentionName = str("AttentionIconName")
	it.IconThemePath = str("IconThemePath")
	it.MenuPath = str("Menu")
	pixmaps := func(name string) []Pixmap {
		v, ok := props[name]
		if !ok {
			return nil
		}
		return keepPixmaps(parsePixmaps(v.Value()), sizes)
	}
	it.IconPixmap = pixmaps("IconPixmap")
	it.AttentionIconPixmap = pixmaps("AttentionIconPixmap")
	it.OverlayIconPixmap = pixmaps("OverlayIconPixmap")
	if tooltip, ok := props["ToolTip"]; ok {
		it.Tooltip = parseTooltip(tooltip.Value())
	}
	return it, call.ResponseSequence, nil
}

// parsePixmaps converts the spec's a(iiay) list, dropping a pixmap
// whose data is shorter than its size.
func parsePixmaps(v any) []Pixmap {
	rows, ok := v.([][]any)
	if !ok {
		if anyRows, ok := v.([]any); ok {
			for _, r := range anyRows {
				if tuple, ok := r.([]any); ok {
					rows = append(rows, tuple)
				}
			}
		}
	}
	var out []Pixmap
	for _, tuple := range rows {
		if len(tuple) != 3 {
			continue
		}
		w, _ := tuple[0].(int32)
		hgt, _ := tuple[1].(int32)
		data, _ := tuple[2].([]byte)
		if w <= 0 || hgt <= 0 || int64(len(data)) < int64(w)*int64(hgt)*4 {
			continue
		}
		out = append(out, Pixmap{Width: w, Height: hgt, Data: data})
	}
	return out
}

// parseTooltip converts the spec's (sa(iiay)ss).
func parseTooltip(v any) Tooltip {
	tuple, ok := v.([]any)
	if len(tuple) != 4 || !ok {
		return Tooltip{}
	}
	tip := Tooltip{}
	tip.IconName, _ = tuple[0].(string)
	tip.Title, _ = tuple[2].(string)
	tip.Description, _ = tuple[3].(string)
	return tip
}

// Close ends the calls under way, waits for the loop and everything
// else the host started, and takes back the matches, the watch on bus
// names, the watcher object and its name; the connection stays open.
func (h *Host) Close() error {
	h.cancel()
	h.work.Wait()
	return h.release()
}

// watcher is the exported StatusNotifierWatcher (watcher/mod.rs).
// A registration asks the bus whether its name still has an owner
// before the item is recorded: checking counts the registrations of a
// name under way, and lost is when the bus announced, during one, that
// the name lost its owner.
type watcher struct {
	host    *Host
	ownerOf func(name string) (answered dbus.Sequence, err error)

	mu       sync.Mutex
	items    []string
	hosts    []string
	checking map[string]int
	lost     map[string]dbus.Sequence
}

func newWatcher(h *Host) *watcher {
	w := &watcher{host: h, hosts: []string{h.conn.Names()[0]}, checking: map[string]int{}, lost: map[string]dbus.Sequence{}}
	w.ownerOf = w.askOwner
	return w
}

func (w *watcher) askOwner(name string) (dbus.Sequence, error) {
	ctx, cancel := w.host.answer()
	defer cancel()
	var owner string
	call := w.host.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, name)
	return call.ResponseSequence, call.Store(&owner)
}

// RegisterStatusNotifierItem accepts an item. A bare object path is
// the sender's own (sender + path), as the spec and the Rust watcher
// allow; anything else is a bus name.
func (w *watcher) RegisterStatusNotifierItem(sender dbus.Sender, service string) *dbus.Error {
	if strings.HasPrefix(service, "/") {
		service = string(sender) + service
	}
	w.register(service)
	return nil
}

// register records an item, announces it, and tracks it, unless its
// bus name has no owner by the time the bus answers whether it has, or
// the bus announced its loss after that answer while the name was
// checked: that loss has been handled, and nothing would drop the item
// again. A bus name alone and that name at the default path are one
// item: the orphan scan registers the bare name while the item itself
// may register name/StatusNotifierItem, and either order must leave
// one entry.
func (w *watcher) register(service string) {
	bus, path := ParseAddress(service)
	w.mu.Lock()
	if w.knowsLocked(bus, path) {
		w.mu.Unlock()
		return
	}
	w.checking[bus]++
	w.mu.Unlock()
	answered, err := w.ownerOf(bus)
	w.mu.Lock()
	lost, wasLost := w.lost[bus]
	if w.checking[bus]--; w.checking[bus] == 0 {
		delete(w.checking, bus)
		delete(w.lost, bus)
	}
	if err != nil || wasLost && lost > answered || w.knowsLocked(bus, path) {
		w.mu.Unlock()
		if derr, ok := errors.AsType[dbus.Error](err); err != nil && (!ok || derr.Name != errNameHasNoOwner) {
			w.host.failed(fmt.Errorf("register %s: the owner of %s: %w", service, bus, err))
		}
		return
	}
	w.items = append(w.items, service)
	w.mu.Unlock()
	_ = w.host.conn.Emit(WatcherPath, WatcherIface+".StatusNotifierItemRegistered", service)
	w.host.requestTrack(service)
}

func (w *watcher) knowsLocked(bus, path string) bool {
	return slices.ContainsFunc(w.items, func(s string) bool {
		b, p := ParseAddress(s)
		return b == bus && p == path
	})
}

// registered reports whether a bus name has any item registered.
func (w *watcher) registered(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.items {
		if s == name || strings.HasPrefix(s, name+"/") {
			return true
		}
	}
	return false
}

// nameVanished is unregister_item + unregister_host for a connection
// that left the bus.
func (w *watcher) nameVanished(loss nameLoss) {
	name := loss.name
	w.mu.Lock()
	if w.checking[name] > 0 {
		w.lost[name] = max(w.lost[name], loss.announced)
	}
	var removed []string
	kept := w.items[:0]
	for _, s := range w.items {
		if s == name || strings.HasPrefix(s, name+"/") {
			removed = append(removed, s)
			continue
		}
		kept = append(kept, s)
	}
	w.items = kept
	hostGone, emptied := false, false
	for i, h := range w.hosts {
		if h == name {
			w.hosts = append(w.hosts[:i], w.hosts[i+1:]...)
			hostGone, emptied = true, len(w.hosts) == 0
			break
		}
	}
	w.mu.Unlock()
	for _, s := range removed {
		_ = w.host.conn.Emit(WatcherPath, WatcherIface+".StatusNotifierItemUnregistered", s)
		bus, path := ParseAddress(s)
		w.host.forget(bus, path)
	}
	if hostGone && emptied {
		_ = w.host.conn.Emit(WatcherPath, WatcherIface+".StatusNotifierHostUnregistered")
	}
}

// RegisterStatusNotifierHost accepts a host; the first one beyond our
// own announces itself.
func (w *watcher) RegisterStatusNotifierHost(service string) *dbus.Error {
	w.mu.Lock()
	if slices.Contains(w.hosts, service) {
		w.mu.Unlock()
		return nil
	}
	w.hosts = append(w.hosts, service)
	w.mu.Unlock()
	_ = w.host.conn.Emit(WatcherPath, WatcherIface+".StatusNotifierHostRegistered")
	return nil
}

// Get implements the Properties read of the watcher's three
// properties.
func (w *watcher) Get(iface, prop string) (dbus.Variant, *dbus.Error) {
	if iface != WatcherIface {
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unknown interface %s", iface))
	}
	all, _ := w.GetAll(iface)
	v, ok := all[prop]
	if !ok {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty", Body: []any{prop}}
	}
	return v, nil
}

// GetAll implements the Properties read of the watcher.
func (w *watcher) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != WatcherIface {
		return nil, dbus.MakeFailedError(fmt.Errorf("unknown interface %s", iface))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return map[string]dbus.Variant{
		"RegisteredStatusNotifierItems":  dbus.MakeVariant(append([]string{}, w.items...)),
		"IsStatusNotifierHostRegistered": dbus.MakeVariant(len(w.hosts) > 0),
		"ProtocolVersion":                dbus.MakeVariant(ProtocolVersion),
	}, nil
}

// Set rejects writes: the watcher's properties are read-only.
func (w *watcher) Set(string, string, dbus.Variant) *dbus.Error {
	return &dbus.Error{Name: "org.freedesktop.DBus.Error.PropertyReadOnly"}
}

// Actions drives one item (the bar's click handlers).
type Actions struct {
	conn *dbus.Conn
}

// Activate sends the primary activation at the given coordinates.
func (a *Actions) Activate(ctx context.Context, it Item, x, y int32) error {
	return a.conn.Object(it.Bus, dbus.ObjectPath(it.Path)).CallWithContext(ctx, ItemIface+".Activate", 0, x, y).Err
}

// SecondaryActivate sends the middle-click activation.
func (a *Actions) SecondaryActivate(ctx context.Context, it Item, x, y int32) error {
	return a.conn.Object(it.Bus, dbus.ObjectPath(it.Path)).CallWithContext(ctx, ItemIface+".SecondaryActivate", 0, x, y).Err
}

// ContextMenu asks the item to show its own menu.
func (a *Actions) ContextMenu(ctx context.Context, it Item, x, y int32) error {
	return a.conn.Object(it.Bus, dbus.ObjectPath(it.Path)).CallWithContext(ctx, ItemIface+".ContextMenu", 0, x, y).Err
}

// Scroll sends a scroll delta ("horizontal"/"vertical").
func (a *Actions) Scroll(ctx context.Context, it Item, delta int32, orientation string) error {
	return a.conn.Object(it.Bus, dbus.ObjectPath(it.Path)).CallWithContext(ctx, ItemIface+".Scroll", 0, delta, orientation).Err
}

// IsWatcher reports whether this process serves the watcher role (the
// Rust service in TrayMode::Auto having claimed it).
func (h *Host) IsWatcher() bool { return h.watcher != nil }
