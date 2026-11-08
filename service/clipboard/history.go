// Package clipboard is the session's clipboard history
// (crates/wayle-clipboard): what gets remembered, how it is labelled,
// what is refused, and the service that keeps it current from the
// Wayland selection over lys-ui's data-control device.
//
// An entry is bytes plus the mime type they arrived under, not a
// string. A file manager puts text/uri-list on the clipboard, an
// image editor image/png, a terminal text/plain;charset=utf-8 —
// remembering only the last of those would make the history useless
// for exactly the things that are most annoying to copy twice.
//
// The bytes live off the Go heap and are shared by reference count:
// the history holds each entry, and so does every Snapshot listing it
// and every paste serving it. An entry the history drops is freed when
// the last of those lets go, never while one can still read it.
package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/stubbedev/lys-ui/transfer"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/internal/fileuri"
	"github.com/stubbedev/lys-shell/offheap"
	"github.com/stubbedev/lys-shell/service/clipboard/historybudget"
)

// DefaultCapacity is how many entries are kept before the oldest is
// dropped (history.rs DEFAULT_CAPACITY).
const DefaultCapacity = 200

// TextMimes are the mime types a plain-text selection arrives under,
// best first.
var TextMimes = []string{
	"text/plain;charset=utf-8",
	"text/plain",
	"UTF8_STRING",
	"STRING",
	"TEXT",
}

// URIListMime is the mime type a file manager copies files under.
const URIListMime = "text/uri-list"

// SensitiveMimes are the mime types lys-shell will not remember, however
// they are offered. Password managers mark a selection with one of
// these to ask clipboard managers not to keep it, and honouring that
// is the difference between a convenience and a credential leak.
// x-kde-passwordManagerHint carries the value "secret"; the others
// are presence-only conventions.
var SensitiveMimes = []string{
	"x-kde-passwordManagerHint",
	"org.kde.passwordManagerHint",
	"password",
	"x-lys-shell-no-history",
}

// SecretContent is text offered as a password manager offers it: the
// text under every text mime, marked with x-kde-passwordManagerHint so
// a clipboard history (this one included) does not keep it.
func SecretContent(text string) transfer.Content {
	hint := SensitiveMimes[0]
	return transfer.Merge(transfer.Text(text), transfer.Payloads([]string{hint}, map[string][]byte{hint: []byte("secret")}))
}

// Kind is what a remembered selection is, for labelling and for
// choosing an icon.
type Kind uint8

const (
	// KindText is plain text.
	KindText Kind = iota
	// KindFiles is one or more file:// URIs — what copying in a file
	// manager puts down.
	KindFiles
	// KindImage is an image, in whatever encoding the mime names.
	KindImage
	// KindOther is anything else, kept verbatim and offered back
	// under its own mime.
	KindOther
)

// String names the kind.
func (k Kind) String() string {
	switch k {
	case KindText:
		return "text"
	case KindFiles:
		return "files"
	case KindImage:
		return "image"
	case KindOther:
		return "other"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// payload is one entry's bytes, off the Go heap and shared by
// reference count: freed, with its pages handed back, when the last
// holder releases it. Only a holder may hold it again.
type payload struct {
	mem  *memory.Shared
	data memory.Slice[byte]
	refs atomic.Int64
}

// newPayload takes buf, which mem holds, held once by the caller.
func newPayload(mem *memory.Shared, buf memory.Slice[byte]) *payload {
	p := &payload{mem: mem, data: buf}
	p.refs.Store(1)
	return p
}

// bytes are the payload, valid while the caller holds it.
func (p *payload) bytes() []byte { return p.data.Elems() }

// hold takes another reference; the caller already holds one.
func (p *payload) hold() {
	if p.refs.Add(1) <= 1 {
		panic("clipboard: an entry was held after it was freed")
	}
}

// release drops one reference, freeing the bytes with the last.
func (p *payload) release() {
	switch n := p.refs.Add(-1); {
	case n > 0:
		return
	case n < 0:
		panic("clipboard: an entry was released more often than it was held")
	}
	if err := offheap.Free(p.mem, p.data); err != nil {
		log.Printf("clipboard: freeing an entry: %v", err)
	}
}

// Snapshot is the history as it stood, each entry held until Release
// so the history may drop it meanwhile without the listing losing its
// bytes. Release it exactly once, as soon as it has been read: reading
// an entry afterwards panics.
type Snapshot struct {
	entries  []Entry
	released atomic.Bool
}

// Entries are the snapshot's entries, most recent first.
func (s *Snapshot) Entries() []Entry { return s.entries }

// Release lets go of every entry. A second Release panics.
func (s *Snapshot) Release() {
	if s.released.Swap(true) {
		panic("clipboard: a snapshot was released twice")
	}
	for _, e := range s.entries {
		e.p.release()
	}
}

// Entry is one remembered selection, as a Snapshot lists it.
type Entry struct {
	// ID is stable for the life of the entry, so a row the launcher
	// drew still names the same selection once something new arrives
	// at the front.
	ID uint64
	// Mime is the type the bytes are in, and the one they are offered
	// back under.
	Mime string
	// Pinned entries are listed first, survive restarts and are never
	// evicted.
	Pinned bool

	p    *payload
	snap *Snapshot
}

// content is the entry's bytes under its mime, read while its snapshot
// holds them.
func (e Entry) content() content {
	if e.snap.released.Load() {
		panic(fmt.Sprintf("clipboard: entry %d read after its snapshot was released", e.ID))
	}
	return content{mime: e.Mime, data: e.p.bytes()}
}

// Kind reports what the entry is, from its mime type.
func (e Entry) Kind() Kind { return kindOf(e.Mime) }

// Size is how many bytes the entry holds.
func (e Entry) Size() int { return len(e.content().data) }

// Paths returns the decoded paths in a text/uri-list entry.
func (e Entry) Paths() []string { return e.content().paths() }

// Preview is a single line for a list row, at most maxChars
// characters.
func (e Entry) Preview(maxChars int) string { return e.content().preview(maxChars) }

// content is a selection's bytes under its mime, which is all the
// labelling needs.
type content struct {
	mime string
	data []byte
}

// kindOf reports what a selection of this mime is.
func kindOf(mime string) Kind {
	switch {
	case mime == URIListMime:
		return KindFiles
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	case IsTextMime(mime):
		return KindText
	}
	return KindOther
}

// text returns the selection as text, when it is text (or a file
// list) and valid UTF-8.
func (c content) text() (string, bool) {
	if k := kindOf(c.mime); k != KindText && k != KindFiles {
		return "", false
	}
	if !utf8.Valid(c.data) {
		return "", false
	}
	return string(c.data), true
}

// paths returns the decoded paths in a text/uri-list selection.
// Comment lines are part of the format (RFC 2483) and are not paths,
// and a URI that is not file:// has no path to give.
func (c content) paths() []string {
	if kindOf(c.mime) != KindFiles || !utf8.Valid(c.data) {
		return nil
	}
	var paths []string
	for line := range strings.SplitSeq(string(c.data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "file://"); ok {
			paths = append(paths, fileuri.DecodeString(rest))
		}
	}
	return paths
}

// preview is a single line for a list row, at most maxChars
// characters.
func (c content) preview(maxChars int) string {
	var label string
	switch kindOf(c.mime) {
	case KindText:
		// Runs of whitespace — newlines included — collapse to one
		// space, so a copied paragraph is one readable row rather
		// than something that breaks the list's geometry.
		text, _ := c.text()
		label = strings.Join(strings.Fields(text), " ")
	case KindFiles:
		paths := c.paths()
		names := make([]string, len(paths))
		for i, p := range paths {
			names[i] = p[strings.LastIndexByte(p, '/')+1:]
		}
		switch len(names) {
		case 0:
			label = "No files"
		case 1:
			label = names[0]
		default:
			label = fmt.Sprintf("%s (%d files)", strings.Join(names, ", "), len(names))
		}
	case KindImage:
		label = "Image · " + c.mime + " · " + HumanSize(len(c.data))
	case KindOther:
		label = c.mime + " · " + HumanSize(len(c.data))
	}
	return truncateChars(label, maxChars)
}

// IsTextMime reports whether a mime type names plain text.
func IsTextMime(mime string) bool {
	return slices.Contains(TextMimes, mime) || strings.HasPrefix(mime, "text/")
}

// IsSensitive reports whether a selection offering these mimes must
// not be remembered.
func IsSensitive(mimes []string) bool {
	return slices.ContainsFunc(mimes, func(m string) bool { return slices.Contains(SensitiveMimes, m) })
}

// BestMime picks the mime worth remembering out of everything the
// owner offered. Files beat images beat text: a file manager also
// offers the file's name as text/plain, and remembering that instead
// would put a bare filename on the clipboard where a file was copied.
// Within text, the order of TextMimes wins so the entry is UTF-8
// wherever the owner can provide it. Anything else is still worth
// being able to paste back, byte for byte, so the first offered mime
// is the last resort; false only when nothing was offered.
func BestMime(mimes []string) (string, bool) {
	if slices.Contains(mimes, URIListMime) {
		return URIListMime, true
	}
	if i := slices.IndexFunc(mimes, func(m string) bool { return strings.HasPrefix(m, "image/") }); i >= 0 {
		return mimes[i], true
	}
	for _, text := range TextMimes {
		if slices.Contains(mimes, text) {
			return text, true
		}
	}
	if i := slices.IndexFunc(mimes, func(m string) bool { return strings.HasPrefix(m, "text/") }); i >= 0 {
		return mimes[i], true
	}
	if len(mimes) > 0 {
		return mimes[0], true
	}
	return "", false
}

// offerMimes are the mime types an entry of this mime is offered back
// under. Text goes out under every flavour of text a receiver might
// ask for, since they are all the same bytes and an application that
// only knows STRING would otherwise see an empty clipboard; the
// entry's own mime leads and is never offered twice. Everything else
// is offered under exactly what it came in as: a PNG is not also a
// text/plain.
func offerMimes(mime string) []string {
	if kindOf(mime) != KindText {
		return []string{mime}
	}
	mimes := []string{mime}
	for _, m := range TextMimes {
		if m != mime {
			mimes = append(mimes, m)
		}
	}
	return mimes
}

// History is the clipboard history: pinned entries first, then the
// rest, each most recent first. The unpinned entries are bounded by
// entry count and by total bytes, the oldest making way for a new one;
// pinned entries are never evicted, and their own byte budget refuses a
// pin rather than dropping one. An entry restored from a Store holds no
// bytes until it is filled. It is plain bookkeeping and not safe for
// concurrent use; the Clipboard service guards it.
type History struct {
	mem           memory.Shared
	entries       []stored
	capacity      int
	maxEntryBytes int
	budget        int
	pinnedBudget  int
	// size and count are the unpinned entries' bytes and number,
	// pinnedSize the pinned entries' bytes.
	size       int
	count      int
	pinnedSize int
	nextID     uint64
	closed     bool
}

// stored is one entry the history holds. p is nil while a restored
// entry's bytes are still only on disk; digest is set once summed.
type stored struct {
	id     uint64
	mime   string
	size   int
	p      *payload
	digest Digest
	summed bool
	pinned bool
}

// NewHistory builds an empty history keeping at most capacity unpinned
// entries of at most maxEntryBytes each, budget bytes in all, and
// pinned entries of pinnedBudget bytes in all. A zero capacity or entry
// size remembers nothing. A negative bound is an error, and so is a
// budget smaller than the largest entry: that entry would be accepted
// only to evict everything, itself included.
func NewHistory(capacity, maxEntryBytes, budget, pinnedBudget int) (*History, error) {
	if capacity < 0 || maxEntryBytes < 0 {
		return nil, fmt.Errorf("clipboard: history bounds must not be negative (capacity %d, max entry %d)", capacity, maxEntryBytes)
	}
	if err := checkBudget(maxEntryBytes, budget); err != nil {
		return nil, err
	}
	if err := checkBudget(maxEntryBytes, pinnedBudget); err != nil {
		return nil, err
	}
	return &History{capacity: capacity, maxEntryBytes: maxEntryBytes, budget: budget, pinnedBudget: pinnedBudget, nextID: 1}, nil
}

// DefaultHistory is the history the shell runs with: DefaultCapacity
// entries of up to MaxEntryBytes, budget in all, and pinned entries of
// pinned in all.
func DefaultHistory(budget, pinned historybudget.Budget) (*History, error) {
	return NewHistory(DefaultCapacity, historybudget.MaxEntryBytes, budget.Bytes(), pinned.Bytes())
}

// checkBudget refuses a budget that cannot hold the largest entry.
func checkBudget(maxEntryBytes, budget int) error {
	if budget < maxEntryBytes {
		return fmt.Errorf("clipboard: a history budget of %d bytes cannot hold an entry of %d", budget, maxEntryBytes)
	}
	return nil
}

// errClosed reports a push to a history that was closed.
var errClosed = errors.New("clipboard: the history is closed")

// Push records a selection and returns the id it is stored under.
// Re-copying something already remembered moves it to the front and
// keeps its id (and its pin) rather than adding a second copy: the
// clipboard is a stack of what you have, and the same bytes twice is
// one thing. The same bytes under another mime are another entry —
// pasting a PNG back as text/plain would hand the receiver bytes it
// cannot use. The oldest unpinned entries make way until the history is
// within its capacity and budget again.
//
// False when the selection is not worth remembering: empty,
// oversized, whitespace-only text, or a zero-capacity history. An
// error when the history is closed or the bytes cannot be held.
func (h *History) Push(mime string, data []byte) (uint64, bool, error) {
	v := h.judge(mime, data)
	if v.settled {
		return v.id, v.kept, v.err
	}
	buf, err := offheap.Clone(&h.mem, data)
	if err != nil {
		return 0, false, fmt.Errorf("clipboard: %w", err)
	}
	return h.place(mime, buf, v), true, nil
}

// pushOwned is Push of bytes already off the heap in the history's
// memory, taking buf: it becomes the entry, or is freed.
func (h *History) pushOwned(mime string, buf memory.Slice[byte]) (uint64, bool, error) {
	v := h.judge(mime, buf.Elems())
	if v.settled {
		if err := offheap.Free(&h.mem, buf); err != nil {
			v.err = errors.Join(v.err, fmt.Errorf("clipboard: freeing a selection not kept: %w", err))
		}
		return v.id, v.kept, v.err
	}
	return h.place(mime, buf, v), true, nil
}

// place stores buf: as the bytes of the restored entry judge matched and
// moved to the front, or as a new entry.
func (h *History) place(mime string, buf memory.Slice[byte], v verdict) uint64 {
	if v.fill {
		h.entries[0].p = newPayload(&h.mem, buf)
		return v.id
	}
	return h.insert(mime, buf)
}

// verdict is what Push makes of a selection before it needs its own
// bytes: settled when it is refused, already held, or cannot be
// recorded at all. fill is a restored entry, matched by digest and moved
// to the front, that takes the selection's bytes as its own.
type verdict struct {
	settled bool
	fill    bool
	id      uint64
	kept    bool
	err     error
}

// judge settles a selection that needs no new entry: refused, or
// already held under mime, which it moves to the front. An entry whose
// bytes are still on disk is matched by digest, summed only when one of
// that mime and size is waiting.
func (h *History) judge(mime string, data []byte) verdict {
	if h.closed {
		return verdict{settled: true, err: errClosed}
	}
	if len(data) == 0 || len(data) > h.maxEntryBytes || h.capacity == 0 {
		return verdict{settled: true}
	}
	if IsTextMime(mime) && utf8.Valid(data) && strings.TrimSpace(string(data)) == "" {
		return verdict{settled: true}
	}
	var sum Digest
	summed := false
	i := slices.IndexFunc(h.entries, func(e stored) bool {
		if e.mime != mime || e.size != len(data) {
			return false
		}
		if e.p != nil {
			return bytes.Equal(e.p.bytes(), data)
		}
		if !summed {
			sum, summed = DigestOf(data), true
		}
		return e.digest == sum
	})
	if i < 0 {
		return verdict{}
	}
	entry := h.entries[i]
	copy(h.entries[1:i+1], h.entries[:i])
	h.entries[0] = entry
	if entry.p == nil {
		return verdict{fill: true, id: entry.id}
	}
	return verdict{settled: true, id: entry.id, kept: true}
}

// insert adds buf as the newest entry and returns its id.
func (h *History) insert(mime string, buf memory.Slice[byte]) uint64 {
	id := h.nextID
	h.nextID++
	h.entries = slices.Insert(h.entries, 0, stored{id: id, mime: mime, size: buf.Len(), p: newPayload(&h.mem, buf)})
	h.account(h.entries[0], 1)
	h.evict()
	return id
}

// account adds (sign 1) or removes (sign -1) an entry's bytes from the
// total of its kind.
func (h *History) account(e stored, sign int) {
	if e.pinned {
		h.pinnedSize += sign * e.size
		return
	}
	h.size += sign * e.size
	h.count += sign
}

// evict drops the oldest unpinned entries until the history is within
// its capacity and budget. The newest always stays: it is no larger
// than maxEntryBytes, which the budget holds.
func (h *History) evict() {
	for h.count > h.capacity || h.size > h.budget {
		last := len(h.entries) - 1
		for h.entries[last].pinned {
			last--
		}
		h.removeAt(last)
	}
}

// removeAt lets go of the entry at i.
func (h *History) removeAt(i int) {
	e := h.entries[i]
	h.account(e, -1)
	if e.p != nil {
		e.p.release()
	}
	h.entries = slices.Delete(h.entries, i, i+1)
}

// SetBudget bounds the unpinned entries to budget bytes from now on,
// evicting the oldest past it.
func (h *History) SetBudget(budget int) error {
	if err := checkBudget(h.maxEntryBytes, budget); err != nil {
		return err
	}
	h.budget = budget
	h.evict()
	return nil
}

// SetPinnedBudget bounds the pinned entries to budget bytes for pins
// from now on. Entries already pinned stay: a pin is never undone
// behind the user's back.
func (h *History) SetPinnedBudget(budget int) error {
	if err := checkBudget(h.maxEntryBytes, budget); err != nil {
		return err
	}
	h.pinnedBudget = budget
	return nil
}

// Entries returns every entry whose bytes are in memory, pinned first,
// each most recent first, each held until the snapshot is released.
func (h *History) Entries() *Snapshot {
	s := &Snapshot{entries: make([]Entry, 0, len(h.entries))}
	for _, pinned := range [...]bool{true, false} {
		for _, e := range h.entries {
			if e.pinned != pinned || e.p == nil {
				continue
			}
			e.p.hold()
			s.entries = append(s.entries, Entry{ID: e.id, Mime: e.mime, Pinned: e.pinned, p: e.p, snap: s})
		}
	}
	return s
}

func (h *History) index(id uint64) int {
	return slices.IndexFunc(h.entries, func(e stored) bool { return e.id == id })
}

// hold returns the entry with this id, held for the caller to release;
// false once it aged out or was removed, or while its bytes are only
// on disk.
func (h *History) hold(id uint64) (string, *payload, bool) {
	i := h.index(id)
	if i < 0 || h.entries[i].p == nil {
		return "", nil, false
	}
	e := h.entries[i]
	e.p.hold()
	return e.mime, e.p, true
}

// Remove forgets one entry, pinned or not, reporting whether it was
// there.
func (h *History) Remove(id uint64) bool {
	i := h.index(id)
	if i < 0 {
		return false
	}
	h.removeAt(i)
	return true
}

// pinned reports whether the entry with this id is pinned.
func (h *History) pinned(id uint64) bool {
	i := h.index(id)
	return i >= 0 && h.entries[i].pinned
}

// ClearUnpinned forgets every entry that is not pinned.
func (h *History) ClearUnpinned() {
	h.entries = slices.DeleteFunc(h.entries, func(e stored) bool {
		if e.pinned {
			return false
		}
		h.account(e, -1)
		if e.p != nil {
			e.p.release()
		}
		return true
	})
}

// Clear forgets everything, pinned entries included.
func (h *History) Clear() {
	for _, e := range h.entries {
		if e.p != nil {
			e.p.release()
		}
	}
	h.entries = nil
	h.size, h.count, h.pinnedSize = 0, 0, 0
}

// Close forgets everything for good: a later Push is an error.
func (h *History) Close() {
	h.Clear()
	h.closed = true
}

// ErrPinnedFull is a pin past the pinned entries' byte budget.
var ErrPinnedFull = errors.New("clipboard: the pinned entries' budget is full")

// errNotInMemory is a pin of an entry whose bytes were not read yet.
var errNotInMemory = errors.New("clipboard: the entry's bytes are not in memory")

// Pin keeps the entry with this id for good: listed first, never
// evicted, and refused past the pinned budget.
func (h *History) Pin(id uint64) error {
	i := h.index(id)
	if i < 0 {
		return ErrEntryGone
	}
	e := &h.entries[i]
	switch {
	case e.pinned:
		return nil
	case e.p == nil:
		return errNotInMemory
	case h.pinnedSize+e.size > h.pinnedBudget:
		return fmt.Errorf("%w: %s of %s pinned, the entry is %s", ErrPinnedFull, HumanSize(h.pinnedSize), HumanSize(h.pinnedBudget), HumanSize(e.size))
	}
	h.account(*e, -1)
	e.pinned = true
	h.account(*e, 1)
	return nil
}

// Unpin returns the entry with this id to the rest of the history,
// where the budget may evict it; false when it was not pinned.
func (h *History) Unpin(id uint64) bool {
	i := h.index(id)
	if i < 0 || !h.entries[i].pinned {
		return false
	}
	e := &h.entries[i]
	h.account(*e, -1)
	e.pinned = false
	h.account(*e, 1)
	h.evict()
	return true
}

// restore adds entries a Store holds, oldest last, without their bytes.
func (h *History) restore(records []Record, pinned bool) {
	for _, r := range records {
		e := stored{id: h.nextID, mime: r.Mime, size: r.Size, digest: r.Digest, summed: true, pinned: pinned}
		h.nextID++
		h.entries = append(h.entries, e)
		h.account(e, 1)
	}
	h.evict()
}

// onDisk is an entry whose bytes are still only in a Store.
type onDisk struct {
	id     uint64
	pinned bool
	rec    Record
}

// unloaded lists the entries whose bytes are still only on disk; with
// a non-zero id, only that one.
func (h *History) unloaded(id uint64) []onDisk {
	var out []onDisk
	for _, e := range h.entries {
		if e.p == nil && (id == 0 || e.id == id) {
			out = append(out, onDisk{id: e.id, pinned: e.pinned, rec: Record{Digest: e.digest, Mime: e.mime, Size: e.size}})
		}
	}
	return out
}

// fill gives a restored entry its bytes, taking buf; false, buf not
// taken, when the entry left or was filled meanwhile.
func (h *History) fill(id uint64, buf memory.Slice[byte]) bool {
	i := h.index(id)
	if i < 0 || h.entries[i].p != nil {
		return false
	}
	h.entries[i].p = newPayload(&h.mem, buf)
	return true
}

// heldEntry is an entry handed out for storing, its bytes (when in
// memory) held until released.
type heldEntry struct {
	id     uint64
	mime   string
	size   int
	p      *payload
	digest Digest
	summed bool
}

// held lists the pinned or the unpinned entries in order, holding the
// bytes of those in memory.
func (h *History) held(pinned bool) []heldEntry {
	var out []heldEntry
	for _, e := range h.entries {
		if e.pinned != pinned {
			continue
		}
		if e.p != nil {
			e.p.hold()
		}
		out = append(out, heldEntry{id: e.id, mime: e.mime, size: e.size, p: e.p, digest: e.digest, summed: e.summed})
	}
	return out
}

// setDigest remembers an entry's digest once summed.
func (h *History) setDigest(id uint64, d Digest) {
	if i := h.index(id); i >= 0 {
		h.entries[i].digest, h.entries[i].summed = d, true
	}
}

// Len reports how many entries are held.
func (h *History) Len() int { return len(h.entries) }

// truncateChars cuts to maxChars characters, not bytes: a byte-wise
// cut would split a multi-byte character.
func truncateChars(text string, maxChars int) string {
	if utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	keep := max(maxChars-1, 0)
	var b strings.Builder
	for _, r := range text {
		if keep == 0 {
			break
		}
		b.WriteRune(r)
		keep--
	}
	b.WriteRune('…')
	return b.String()
}

// HumanSize is a byte count short enough for a list row.
func HumanSize(n int) string {
	units := [...]string{"B", "KB", "MB", "GB"}
	size := float64(n)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

// ErrEntryGone is an entry that left the history since a row was drawn
// for it.
var ErrEntryGone = errors.New("clipboard: the entry is no longer in the history")

// Bytes copies the entry's bytes out, for a reader that outlives its
// snapshot.
func (e Entry) Bytes() []byte { return slices.Clone(e.content().data) }

// dropUnloaded forgets the unpinned entries whose bytes are still only
// on disk.
func (h *History) dropUnloaded() {
	h.entries = slices.DeleteFunc(h.entries, func(e stored) bool {
		if e.pinned || e.p != nil {
			return false
		}
		h.account(e, -1)
		return true
	})
}
