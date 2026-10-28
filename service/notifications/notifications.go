// Package notifications is the desktop notification service: the
// org.freedesktop.Notifications server, the stored history, the popup
// list with its pausable countdowns, and the sticky do-not-disturb
// state.
package notifications

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/feed"
	"github.com/stubbedev/lys-shell/internal/glob"
	"github.com/stubbedev/lys-shell/internal/version"
)

// D-Bus identity and server information.
const (
	Interface     = "org.freedesktop.Notifications"
	ObjPath       = "/org/freedesktop/Notifications"
	ServerName    = version.Name
	ServerVendor  = version.Name
	ServerVersion = version.Version
	SpecVersion   = "1.2"
)

// DefaultPopupDuration is the popup display time until the shell sets
// its configured one (builder.rs's popup_duration default).
const DefaultPopupDuration = 5000 * time.Millisecond

// Capabilities the server advertises (daemon.rs).
var Capabilities = []string{
	"body",
	"body-markup",
	"actions",
	"icon-static",
	"persistence",
}

// ClosedReason is the spec's closed reason codes.
type ClosedReason uint32

// Closed reasons.
const (
	Expired    ClosedReason = 1
	Dismissed  ClosedReason = 2
	ClosedCall ClosedReason = 3
	Unknown    ClosedReason = 4
)

// Notification is one received notification.
type Notification struct {
	ID      uint32
	AppName string
	AppIcon string
	Summary string
	Body    string
	// Actions are the parsed action pairs, the default one included.
	Actions []Action
	Expires time.Time // zero: never
	// ExpireMS is the sender's expire_timeout: negative is the server
	// default, zero never expires, positive is milliseconds.
	ExpireMS int32
	Added    time.Time
	Urgency  Urgency
	// ImagePath is the image-path hint, or the cached PNG of inline
	// image data.
	ImagePath    string
	DesktopEntry string
	// Transient notifications skip the history; resident ones stay
	// after an action is invoked.
	Transient bool
	Resident  bool
	// ReplacesID is the replaces_id the sender passed.
	ReplacesID uint32
}

// DefaultAction is the "default" action, when the sender offered one.
func (n *Notification) DefaultAction() (Action, bool) {
	for _, a := range n.Actions {
		if a.ID == DefaultActionID {
			return a, true
		}
	}
	return Action{}, false
}

// Expired reports whether the expiry elapsed at now.
func (n *Notification) Expired(now time.Time) bool {
	return !n.Expires.IsZero() && now.After(n.Expires)
}

// Event is the service's change feed.
type Event struct {
	Kind   EventKind
	Notif  *Notification
	ID     uint32
	Reason ClosedReason
}

// Event kinds.
const (
	EventAdd    EventKind = "add"
	EventRemove EventKind = "remove"
	EventDnd    EventKind = "dnd"
	EventAction EventKind = "action"
	// EventPopups is a popup-list change that leaves the history alone:
	// a countdown ran out or a popup was dismissed.
	EventPopups EventKind = "popups"
)

// EventKind names a change.
type EventKind string

// popupTimer is one popup's pausable countdown (popup_timer.rs's
// PopupTimer): running while stop is set, paused with the remaining
// time held in duration.
type popupTimer struct {
	started  time.Time
	duration time.Duration
	// stop disarms the running countdown; nil while paused.
	stop func() bool
}

// remaining is the time left on a running countdown.
func (t *popupTimer) remaining(now time.Time) time.Duration {
	return max(t.duration-now.Sub(t.started), 0)
}

// Service holds the state and serves the D-Bus interface.
type Service struct {
	mu   sync.Mutex
	next uint32
	// recent is the newest part of the history, oldest first by Key:
	// at most memory notifications, the rest only in the store.
	recent []*Notification
	// total counts the whole history, stored ones included.
	total  int
	memory MemoryLimit
	stored StoredLimit
	// expiry holds the history-expiry timers (remove-expired), stored
	// notifications outside recent included.
	expiry map[uint32]*expiryTimer
	// popups is the visible popup list, newest first; timers holds
	// each one's countdown.
	popups        []*Notification
	timers        map[uint32]*popupTimer
	popupDuration time.Duration
	dnd           bool
	removeExpired bool
	block         []string
	events        *feed.Feed[Event]
	owners        map[uint32]string
	emit          func(signal string, args ...any)
	// cacheImage turns inline image data into a file path.
	cacheImage func(imageData) (string, bool)
	// store persists the history; nil keeps it in memory only.
	store *Store
	// storeTail closes once the last queued store write is done; each
	// write waits for the one before it (persistUnlock).
	storeTail chan struct{}
	// clock stamps arrivals and arms the countdowns and expiries.
	clock Clock
}

// Clock is the service's time source: when a notification arrived,
// and the popup countdowns and history expiries it arms.
type Clock interface {
	Now() time.Time
	// AfterFunc runs fn once d has passed; stop disarms it, reporting
	// whether it was still armed.
	AfterFunc(d time.Duration, fn func()) (stop func() bool)
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) AfterFunc(d time.Duration, fn func()) func() bool {
	return time.AfterFunc(d, fn).Stop
}

// UseClock makes the service keep time by c instead of the wall clock,
// for a host that drives time itself; call it before the first
// notification.
func (s *Service) UseClock(c Clock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = c
}

// expiryTimer is one history entry's armed expiry.
type expiryTimer struct{ stop func() bool }

// NewService restores the DND flag from the state dir.
func NewService() *Service {
	idle := make(chan struct{})
	close(idle)
	return &Service{
		storeTail:     idle,
		next:          0,
		memory:        DefaultMemoryLimit(),
		stored:        DefaultStoredLimit(),
		expiry:        make(map[uint32]*expiryTimer),
		timers:        make(map[uint32]*popupTimer),
		popupDuration: DefaultPopupDuration,
		dnd:           loadDND(),
		removeExpired: true,
		owners:        make(map[uint32]string),
		cacheImage:    cacheImage,
		events:        feed.New[Event](32),
		clock:         wallClock{},
	}
}

// SetEmitter wires the D-Bus signal callback (server-side only).
func (s *Service) SetEmitter(fn func(signal string, args ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit = fn
}

// Subscribe returns a new change feed. Every subscriber sees every
// event (the bar modules on each output and the popup host all follow
// the one service); a subscriber that falls behind drops events rather
// than stalling the service, and re-reads the snapshot on the next.
// stop ends the feed.
func (s *Service) Subscribe() (<-chan Event, func()) { return s.events.Subscribe() }

// notify fans an event out to every subscriber, dropping it for the
// ones whose buffer is full.
func (s *Service) notify(ev Event) { s.events.Publish(ev) }

// Page reads up to limit history entries strictly older than before
// (nil: from the newest), newest first: from memory, then, past the
// in-memory window, from the store by its (timestamp, id) index. A
// short page is the end of the history.
func (s *Service) Page(before *Key, limit int) ([]*Notification, error) {
	if err := checkPageLimit(limit); err != nil {
		return nil, err
	}
	s.mu.Lock()
	end := len(s.recent)
	if before != nil {
		end, _ = slices.BinarySearchFunc(s.recent, *before, func(n *Notification, k Key) int { return n.Key().Compare(k) })
	}
	out := make([]*Notification, 0, limit)
	for i := end - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.recent[i])
	}
	st, onDisk := s.store, s.total > len(s.recent)
	cursor := before
	if end > 0 {
		k := s.recent[0].Key()
		cursor = &k
	}
	s.mu.Unlock()
	if len(out) == limit || st == nil || !onDisk {
		return out, nil
	}
	older, err := st.Page(cursor, limit-len(out))
	if err != nil {
		return nil, err
	}
	return append(out, older...), nil
}

// Count returns the history's size, stored entries included.
func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// SetHistoryLimits bounds the history: memory newest entries stay in
// memory, stored on disk. Lowering either applies at once.
func (s *Service) SetHistoryLimits(memory MemoryLimit, stored StoredLimit) {
	s.mu.Lock()
	s.memory, s.stored = memory, stored
	s.evictLocked()
	prune := s.pruneLocked()
	s.persistUnlock(func(st *Store) {
		_, err := st.Prune(prune)
		storeErr("prune", err)
	})
}

// pruneLocked is how many of the oldest stored entries the stored
// limit drops, taken off the total. The caller holds mu.
func (s *Service) pruneLocked() int {
	if s.store == nil {
		return 0
	}
	prune := max(0, s.total-s.stored.n)
	s.total -= prune
	return prune
}

// evictLocked drops the oldest in-memory entries past the memory limit.
// Without a store they leave the history; with one they stay on disk,
// their expiries armed. The caller holds mu.
func (s *Service) evictLocked() {
	for len(s.recent) > s.memory.n {
		n := s.recent[0]
		s.recent[0] = nil
		s.recent = s.recent[1:]
		delete(s.owners, n.ID)
		if s.store == nil {
			s.total--
			s.stopExpiryLocked(n.ID)
		}
	}
}

// insertLocked adds n to the in-memory window at its Key's place,
// normally the end. An entry older than the window while older ones
// live only on disk stays only on disk, so the window remains the
// newest part of the history. The caller holds mu.
func (s *Service) insertLocked(n *Notification) {
	i, _ := slices.BinarySearchFunc(s.recent, n.Key(), func(m *Notification, k Key) int { return m.Key().Compare(k) })
	if i == 0 && len(s.recent) > 0 && s.total > len(s.recent)+1 {
		return
	}
	s.owners[n.ID] = n.AppName
	s.recent = slices.Insert(s.recent, i, n)
	s.evictLocked()
}

// Popups snapshots the visible popup list, newest first. DND keeps
// new notifications off it (handle_popup_added); popups already shown
// when DND turns on stay until they time out or are dismissed.
func (s *Service) Popups() []*Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Notification, len(s.popups))
	copy(out, s.popups)
	return out
}

// SetPopupDuration sets how long a popup shows (set_popup_duration);
// it applies to popups that arrive afterwards.
func (s *Service) SetPopupDuration(d time.Duration) {
	s.mu.Lock()
	s.popupDuration = d
	s.mu.Unlock()
}

// DND reports do-not-disturb.
func (s *Service) DND() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dnd
}

// SetDND flips do-not-disturb and persists it.
func (s *Service) SetDND(on bool) {
	s.mu.Lock()
	s.dnd = on
	s.mu.Unlock()
	saveDND(on)
	s.notify(Event{Kind: EventDnd})
}

// ToggleDND flips do-not-disturb, returning the new state.
func (s *Service) ToggleDND() bool {
	s.mu.Lock()
	s.dnd = !s.dnd
	on := s.dnd
	s.mu.Unlock()
	saveDND(on)
	s.notify(Event{Kind: EventDnd})
	return on
}

// SetRemoveExpired toggles auto-removal of expired history entries.
func (s *Service) SetRemoveExpired(on bool) {
	s.mu.Lock()
	s.removeExpired = on
	s.mu.Unlock()
}

// SetBlocklist replaces the app-name glob blocklist.
func (s *Service) SetBlocklist(patterns []string) {
	s.mu.Lock()
	s.block = patterns
	s.mu.Unlock()
}

// Notify receives a notification without hints; see NotifyHints.
func (s *Service) Notify(appName string, replacesID uint32, appIcon, summary, body string, actions []string, expireTimeout int32) uint32 {
	return s.NotifyHints(appName, replacesID, appIcon, summary, body, actions, nil, expireTimeout)
}

// NotifyHints receives a notification (the D-Bus Notify call). An app
// may only replace an id it owns, in memory or in the store; any other
// replaces_id gets a new id (resolve_id). A blocked app name consumes
// the notification and still returns an id. A transient notification
// pops up but skips the history (handle_notification_added). The hints
// are parsed into the typed fields and not kept.
func (s *Service) NotifyHints(appName string, replacesID uint32, appIcon, summary, body string, actions []string, rawHints map[string]dbus.Variant, expireTimeout int32) uint32 {
	h := decodeHints(rawHints, s.cacheImage)
	stored, onDisk := s.storedOwner(replacesID)
	s.mu.Lock()
	id := replacesID
	owner, inMemory := s.owners[id]
	if !inMemory && onDisk {
		owner = stored
	}
	if id == 0 || owner != appName {
		s.next++
		id = s.next
		onDisk = false
	}
	for _, pattern := range s.block {
		if globMatch(pattern, appName) {
			s.mu.Unlock()
			return id
		}
	}
	replaced := s.removeHistoryLocked(id) || onDisk
	n := &Notification{
		ID:           id,
		AppName:      appName,
		AppIcon:      appIcon,
		Summary:      summary,
		Body:         body,
		Actions:      ParseActions(actions),
		ExpireMS:     expireTimeout,
		Added:        s.clock.Now(),
		Urgency:      h.urgency,
		ImagePath:    h.imagePath,
		DesktopEntry: h.desktopEntry,
		Transient:    h.transient,
		Resident:     h.resident,
		ReplacesID:   replacesID,
	}
	if expireTimeout > 0 {
		n.Expires = n.Added.Add(time.Duration(expireTimeout) * time.Millisecond)
	}
	prune := 0
	switch {
	case !n.Transient:
		if !replaced {
			s.total++
		}
		s.insertLocked(n)
		prune = s.pruneLocked()
		if !n.Expires.IsZero() && s.removeExpired {
			s.startExpiryLocked(n.ID, n.Expires)
		}
	case replaced:
		s.total--
		s.owners[n.ID] = appName
	default:
		s.owners[n.ID] = appName
	}
	s.addPopupLocked(n)
	s.persistUnlock(func(st *Store) {
		switch {
		case !n.Transient:
			storeErr("add", st.Add(n, prune))
		case replaced:
			// A transient replacement takes the stored one's place
			// in the history, so it leaves the store too.
			_, err := st.Remove([]uint32{id})
			storeErr("remove", err)
		}
	})
	s.notify(Event{Kind: EventAdd, Notif: n})
	return n.ID
}

// storedOwner is the app owning id in the store, for a replaces_id past
// the in-memory window.
func (s *Service) storedOwner(id uint32) (string, bool) {
	n, ok := s.storedOnly(id)
	if !ok {
		return "", false
	}
	return n.AppName, true
}

// storedOnly reads a history entry the in-memory window and the popups
// do not hold from the store.
func (s *Service) storedOnly(id uint32) (*Notification, bool) {
	if id == 0 {
		return nil, false
	}
	s.mu.Lock()
	_, known := s.owners[id]
	st := s.store
	s.mu.Unlock()
	if known || st == nil {
		return nil, false
	}
	n, ok, err := st.Find(id)
	if err != nil {
		log.Printf("notifications: %v", err)
		return nil, false
	}
	return n, ok
}

// addPopupLocked is handle_popup_added: outside DND the notification
// goes to the front of the popup list (a replacement moves up) and
// gets its countdown — the popup duration, capped by the sender's
// positive expire_timeout; a zero timeout sticks until dismissed. The
// caller holds mu.
func (s *Service) addPopupLocked(n *Notification) {
	if s.dnd {
		return
	}
	s.dropPopupLocked(n.ID)
	s.popups = append([]*Notification{n}, s.popups...)
	switch {
	case n.ExpireMS == 0:
	case n.ExpireMS > 0:
		s.startPopupTimerLocked(n.ID, min(s.popupDuration, time.Duration(n.ExpireMS)*time.Millisecond))
	default:
		s.startPopupTimerLocked(n.ID, s.popupDuration)
	}
}

// Close removes a notification, in memory or only in the store,
// emitting the closed signal. Every reason but Expired also takes it
// off the popup list; an expired history entry leaves its popup to its
// own countdown (handle_notification_removed).
func (s *Service) Close(id uint32, reason ClosedReason) {
	s.close([]uint32{id}, reason)
}

// Dismiss closes every notification ids names with the dismissed
// reason, in memory, on the popup list and in the store in one write:
// a popup stack's clear all and a history group's clear.
func (s *Service) Dismiss(ids []uint32) {
	s.close(ids, Dismissed)
}

func (s *Service) close(ids []uint32, reason ClosedReason) {
	s.mu.Lock()
	c := s.forgetLocked(ids, reason)
	s.persistUnlock(func(st *Store) {
		onDisk, err := st.Remove(ids)
		storeErr("remove", err)
		s.mu.Lock()
		for _, id := range onDisk {
			if !c.closed[id] {
				c.closed[id] = true
				s.total--
			}
		}
		s.mu.Unlock()
	})
	s.announce(c)
}

// DismissAll empties the history, the store included, and the popup
// list with the dismissed reason. The closed signal goes out for the
// in-memory window: the entries only on disk are past any app's
// interest, and a signal each would flood the bus.
func (s *Service) DismissAll() {
	s.mu.Lock()
	for id := range s.expiry {
		s.stopExpiryLocked(id)
	}
	c := s.forgetLocked(s.shownLocked(), Dismissed)
	s.total = 0
	s.persistUnlock(func(st *Store) { storeErr("remove all", st.RemoveAll()) })
	s.announce(c)
}

type closing struct {
	ids    []uint32
	reason ClosedReason
	closed map[uint32]bool
	popped bool
	emit   func(signal string, args ...any)
}

func (s *Service) forgetLocked(ids []uint32, reason ClosedReason) *closing {
	c := &closing{ids: ids, reason: reason, closed: make(map[uint32]bool, len(ids)), emit: s.emit}
	for _, id := range ids {
		if s.removeHistoryLocked(id) {
			c.closed[id] = true
			s.total--
		}
		if reason != Expired && s.dropPopupLocked(id) {
			c.popped = true
		}
	}
	return c
}

func (s *Service) shownLocked() []uint32 {
	inWindow := make(map[uint32]bool, len(s.recent))
	ids := make([]uint32, 0, len(s.recent)+len(s.popups))
	for _, n := range s.recent {
		inWindow[n.ID] = true
		ids = append(ids, n.ID)
	}
	for _, n := range s.popups {
		if !inWindow[n.ID] {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

func (s *Service) announce(c *closing) {
	announced := false
	for _, id := range c.ids {
		if !c.closed[id] {
			continue
		}
		delete(c.closed, id)
		announced = true
		if c.emit != nil {
			c.emit(Interface+".NotificationClosed", id, uint32(c.reason))
		}
		s.notify(Event{Kind: EventRemove, ID: id, Reason: c.reason})
	}
	if !announced && c.popped {
		s.notify(Event{Kind: EventPopups})
	}
}

// InvokeAction emits ActionInvoked and, unless the notification is
// resident, closes it with the closed reason (Notification::invoke).
// A token minted from the press goes first as ActivationToken, as the
// spec orders them, so the app can raise its window; "" sends none.
// An entry only in the store is read from it.
func (s *Service) InvokeAction(id uint32, key, token string) {
	s.mu.Lock()
	_, ok := s.owners[id]
	n := s.findLocked(id)
	emit := s.emit
	s.mu.Unlock()
	if !ok {
		n, ok = s.storedOnly(id)
	}
	if !ok {
		return
	}
	resident := n != nil && n.Resident
	if emit != nil {
		if token != "" {
			emit(Interface+".ActivationToken", id, token)
		}
		emit(Interface+".ActionInvoked", id, key)
	}
	if !resident {
		s.Close(id, ClosedCall)
	}
	s.notify(Event{Kind: EventAction, ID: id})
}

// DismissPopup hides a popup, keeping the notification in the history
// (dismiss_popup).
func (s *Service) DismissPopup(id uint32) {
	s.mu.Lock()
	dropped := s.dropPopupLocked(id)
	s.mu.Unlock()
	if dropped {
		s.notify(Event{Kind: EventPopups, ID: id})
	}
}

// InhibitPopup pauses a popup's countdown, keeping the time left
// (inhibit_popup: the pointer is over the card).
func (s *Service) InhibitPopup(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.timers[id]
	if !ok || t.stop == nil {
		return
	}
	t.stop()
	t.stop = nil
	t.duration = t.remaining(s.clock.Now())
}

// ReleasePopup resumes a paused countdown with the time it had left,
// dropping the popup at once when none remained (release_popup). A
// running or unknown countdown is left alone.
func (s *Service) ReleasePopup(id uint32) {
	s.mu.Lock()
	t, ok := s.timers[id]
	if !ok || t.stop != nil {
		s.mu.Unlock()
		return
	}
	if t.duration <= 0 {
		dropped := s.dropPopupLocked(id)
		s.mu.Unlock()
		if dropped {
			s.notify(Event{Kind: EventPopups, ID: id})
		}
		return
	}
	s.startPopupTimerLocked(id, t.duration)
	s.mu.Unlock()
}

// startPopupTimerLocked (re)starts one popup's countdown; when it runs
// out the popup leaves the list, the history keeps the notification.
// The caller holds mu.
func (s *Service) startPopupTimerLocked(id uint32, d time.Duration) {
	if old, ok := s.timers[id]; ok && old.stop != nil {
		old.stop()
	}
	t := &popupTimer{started: s.clock.Now(), duration: d}
	t.stop = s.clock.AfterFunc(d, func() {
		s.mu.Lock()
		// A pause or restart since replaced this countdown.
		if s.timers[id] != t || t.stop == nil {
			s.mu.Unlock()
			return
		}
		dropped := s.dropPopupLocked(id)
		s.mu.Unlock()
		if dropped {
			s.notify(Event{Kind: EventPopups, ID: id})
		}
	})
	s.timers[id] = t
}

// dropPopupLocked removes a popup and its countdown, reporting whether
// it was on the list. The caller holds mu.
func (s *Service) dropPopupLocked(id uint32) bool {
	if t, ok := s.timers[id]; ok {
		if t.stop != nil {
			t.stop()
		}
		delete(s.timers, id)
	}
	for i, n := range s.popups {
		if n.ID == id {
			s.popups = append(s.popups[:i], s.popups[i+1:]...)
			return true
		}
	}
	return false
}

// removeHistoryLocked drops a notification from the in-memory window,
// its owner and its expiry timer, reporting whether the window held it.
// The total is the caller's to adjust. The caller holds mu.
func (s *Service) removeHistoryLocked(id uint32) bool {
	s.stopExpiryLocked(id)
	delete(s.owners, id)
	i := slices.IndexFunc(s.recent, func(n *Notification) bool { return n.ID == id })
	if i < 0 {
		return false
	}
	s.recent = slices.Delete(s.recent, i, i+1)
	return true
}

// startExpiryLocked schedules the remove-expired cleanup: the history
// entry closes with the Expired reason at at, whether it is still in
// memory or only in the store by then. The caller holds mu.
func (s *Service) startExpiryLocked(id uint32, at time.Time) {
	timer := &expiryTimer{}
	timer.stop = s.clock.AfterFunc(at.Sub(s.clock.Now()), func() {
		// A replacement under the same id stopped this timer; a fire
		// racing that Stop must not close the newcomer.
		s.mu.Lock()
		current := s.expiry[id] == timer
		s.mu.Unlock()
		if current {
			s.Close(id, Expired)
		}
	})
	s.expiry[id] = timer
}

// stopExpiryLocked disarms id's expiry. The caller holds mu.
func (s *Service) stopExpiryLocked(id uint32) {
	if timer, ok := s.expiry[id]; ok {
		timer.stop()
		delete(s.expiry, id)
	}
}

// globMatch delegates to the shared blocklist matcher.
func globMatch(pattern, name string) bool {
	return glob.Match(pattern, name)
}

// loadDND restores the sticky flag; anything but "on" reads off.
func loadDND() bool {
	dir, ok := xdg.StateDir(version.Name)
	if !ok {
		return false
	}
	body, err := os.ReadFile(filepath.Join(dir, "dnd")) //nolint:gosec // the path is lys-shell's own state dir
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == "on"
}

// saveDND mirrors the flag into the state dir; failures are quiet.
func saveDND(on bool) {
	dir, ok := xdg.StateDir(version.Name)
	if !ok {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	body := "off\n"
	if on {
		body = "on\n"
	}
	_ = os.WriteFile(filepath.Join(dir, "dnd"), []byte(body), 0o600)
}

// String renders the summary for logs and tests.
func (n *Notification) String() string {
	return fmt.Sprintf("%d %s: %s", n.ID, n.AppName, n.Summary)
}

// findLocked is the in-memory or popped-up notification with id, or
// nil. The caller holds mu.
func (s *Service) findLocked(id uint32) *Notification {
	for _, list := range [][]*Notification{s.recent, s.popups} {
		for _, n := range list {
			if n.ID == id {
				return n
			}
		}
	}
	return nil
}

// AttachStore persists the history in st and restores what it holds
// (builder.rs's load_stored_notifications), before the first
// notification arrives: with remove-expired, what expired while
// lys-shell was down leaves the store, and the rest arms its expiry;
// the newest entries up to the memory limit fill the in-memory window,
// the rest stay on disk; a lowered stored limit prunes the oldest; new
// ids continue past the highest stored one. Restored notifications do
// not pop up again.
func (s *Service) AttachStore(st *Store) error {
	s.mu.Lock()
	removeExpired, now, memory, stored := s.removeExpired, s.clock.Now(), s.memory, s.stored
	arrived := len(s.recent) > 0 || s.store != nil
	s.mu.Unlock()
	if arrived {
		return errors.New("notifications: the store attaches once, before the first notification")
	}
	if removeExpired {
		if _, err := st.SweepExpired(now); err != nil {
			return fmt.Errorf("notifications: restoring the history: %w", err)
		}
	}
	total, err := st.Count()
	if err != nil {
		return fmt.Errorf("notifications: restoring the history: %w", err)
	}
	if excess := total - stored.n; excess > 0 {
		pruned, err := st.Prune(excess)
		if err != nil {
			return fmt.Errorf("notifications: restoring the history: %w", err)
		}
		total -= pruned
	}
	newest, err := st.Page(nil, memory.n)
	if err != nil {
		return fmt.Errorf("notifications: restoring the history: %w", err)
	}
	var pending []expiry
	if removeExpired {
		if pending, err = st.pendingExpiries(now); err != nil {
			return fmt.Errorf("notifications: restoring the history: %w", err)
		}
	}
	maxID, err := st.MaxID()
	if err != nil {
		return fmt.Errorf("notifications: restoring the history: %w", err)
	}
	s.mu.Lock()
	s.store = st
	s.total = total
	s.next = max(s.next, maxID)
	s.recent = make([]*Notification, 0, len(newest))
	for _, n := range slices.Backward(newest) {
		s.recent = append(s.recent, n)
		s.owners[n.ID] = n.AppName
	}
	for _, e := range pending {
		s.startExpiryLocked(e.id, e.at)
	}
	s.mu.Unlock()
	if total > 0 {
		s.notify(Event{Kind: EventAdd})
	}
	return nil
}

// persistUnlock releases mu and writes the change it guarded to the
// attached store. The write takes its place in line while mu is still
// held and waits for the one before it, so the writes reach the disk
// in the order the history changed: an expiry or close racing an
// arrival cannot remove the row before it is added and leave it on
// disk. The caller holds mu.
func (s *Service) persistUnlock(write func(*Store)) {
	st := s.store
	if st == nil {
		s.mu.Unlock()
		return
	}
	prev, done := s.storeTail, make(chan struct{})
	s.storeTail = done
	s.mu.Unlock()
	defer close(done)
	<-prev
	write(st)
}

// storeErr logs a failed write: the in-memory history stays right, the
// next restart misses the change (the Rust store ignores these too).
func storeErr(op string, err error) {
	if err != nil {
		log.Printf("notifications: store %s: %v", op, err)
	}
}
