package clipboard

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/transfer"

	"github.com/stubbedev/lys-shell/internal/offheap"
)

// Clipboard is the session's clipboard history, kept current: it
// watches the regular selection, remembers what lands on it, and puts
// an entry back when asked (service.rs). The primary selection is not
// remembered, as in the Rust service.
//
// Entries, Forget, Clear and SetBudget are safe from any goroutine.
// Copy hops onto the loop goroutine itself, since claiming the
// selection is Wayland work.
type Clipboard struct {
	mu      sync.Mutex
	history *History
	closed  bool

	dev    selectionDevice
	invoke func(func())

	// reads are the selection reads in flight, in the order their
	// selections arrived. Results are recorded strictly in that order,
	// so a slow owner's payload finishing after a quick one's cannot
	// jump ahead of it in the history. Loop goroutine only.
	reads []*pendingRead
	// serving is the restored entry we hold the selection with, nil
	// while we hold none. Loop goroutine only.
	serving *serving
}

// pendingRead is one selection read, streaming off the heap into buf.
// Once done, buf holds the whole selection when ok, and nothing
// otherwise.
type pendingRead struct {
	mime string
	buf  *offheap.Buffer
	done bool
	ok   bool
}

// discard frees whatever the read holds, a transfer still writing into
// it included.
func (r *pendingRead) discard() {
	if err := r.buf.Free(); err != nil {
		log.Printf("clipboard: freeing the %s selection read: %v", r.mime, err)
	}
}

// serving is a restored entry on the clipboard: the claim holds the
// entry until the claim ends, so pasting it still works after the
// history dropped it.
type serving struct {
	claim   selectionClaim
	release func()
	once    sync.Once
}

// end lets go of the entry; once, however the claim ended.
func (s *serving) end() { s.once.Do(s.release) }

// selectionDevice is what the service needs from lys-ui's data-control
// device, narrowed so tests drive it with fakes.
type selectionDevice interface {
	// watch calls fn on every regular-selection change, nil when the
	// selection was cleared.
	watch(fn func(selectionOffer))
	// claim takes the regular selection with src.
	claim(src app.SelectionSource) (selectionClaim, error)
}

// selectionClaim is our hold on the regular selection.
type selectionClaim interface {
	// OnCancelled calls fn once the claim ends for any reason but
	// Release, at once when it already ended.
	OnCancelled(fn func())
	// Release gives the selection up without calling OnCancelled.
	Release()
}

// selectionOffer is one offered selection, as the service reads it.
type selectionOffer interface {
	Mimes() []string
	Own() bool
	ReadTo(mime string, limit int64, w io.Writer, done func(int64, error)) error
}

// ErrNoDataControl is the Start failure on a compositor without
// either data-control protocol: the session simply has no clipboard
// history, and nothing else depends on it.
var ErrNoDataControl = errors.New("clipboard: compositor lacks ext-data-control-v1 and zwlr_data_control_manager_v1")

// Start begins watching the session's selection into history. Call it
// once at shell startup rather than when the launcher first opens, so
// the history covers the session instead of starting empty at the
// moment somebody goes looking for it. Fails with ErrNoDataControl
// when the compositor offers no data-control protocol.
func Start(a *app.Application, history *History) (*Clipboard, error) {
	dc, err := a.DataControl()
	if errors.Is(err, app.ErrDataControlUnavailable) {
		return nil, ErrNoDataControl
	}
	if err != nil {
		return nil, err
	}
	return start(dataControl{dc}, a.Invoke, history), nil
}

// start wires the service onto a device; invoke runs a function on
// the loop goroutine.
func start(dev selectionDevice, invoke func(func()), history *History) *Clipboard {
	c := &Clipboard{history: history, dev: dev, invoke: invoke}
	dev.watch(c.selectionChanged)
	return c
}

// Entries returns a snapshot of the history, most recent first, for
// the caller to release once it has read it.
func (c *Clipboard) Entries() *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.history.Entries()
}

// Forget drops one entry, reporting whether it was there.
func (c *Clipboard) Forget(id uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.history.Remove(id)
}

// Clear forgets everything.
func (c *Clipboard) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history.Clear()
}

// SetBudget bounds the history to budget from now on, dropping the
// oldest entries past it.
func (c *Clipboard) SetBudget(budget Budget) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.history.SetBudget(budget.Bytes())
}

// Copy puts a remembered entry back on the clipboard, under the mime
// it arrived with (and every text alias, for text), and reports
// whether the entry was still in the history. The entry keeps its
// place: a restore is not a new copy.
func (c *Clipboard) Copy(id uint64) bool {
	c.mu.Lock()
	mime, p, ok := c.history.hold(id)
	c.mu.Unlock()
	if !ok {
		return false
	}
	c.invoke(func() { c.claim(mime, p) })
	return true
}

// claim takes the selection with p under mime's offer set, handing the
// caller's hold on p to the claim. Each request holds p again while
// its bytes are written off the loop, so neither the history dropping
// the entry nor the claim ending can free them mid-paste. Loop
// goroutine only.
func (c *Clipboard) claim(mime string, p *payload) {
	err := c.serve(app.SelectionSource{
		Mimes: offerMimes(mime),
		// The receiver may ask under any mime offered; every alias is
		// an alias for the entry's own type, so the bytes are the same.
		Send: func(_ string, w io.WriteCloser) {
			p.hold()
			go func() {
				_, err := w.Write(p.bytes())
				p.release()
				finishPaste(mime, w, err)
			}()
		},
	}, p.release)
	if err != nil {
		log.Printf("clipboard: cannot take the selection: %v", err)
	}
}

// Put claims the regular selection with content through data-control,
// which needs neither keyboard focus nor an input event, serves it to
// every receiver that asks until another client takes the selection,
// and remembers it like any other copy. Loop goroutine only.
func (c *Clipboard) Put(content transfer.Content) error {
	if content.Empty() {
		return errors.New("clipboard: put: the content offers no mime type")
	}
	err := c.serve(app.SelectionSource{
		Mimes: content.Mimes,
		Send: func(mime string, w io.WriteCloser) {
			go func() { finishPaste(mime, w, content.Write(mime, w)) }()
		},
	}, func() {})
	if err != nil {
		return fmt.Errorf("clipboard: put %v: %w", content.Mimes, err)
	}
	c.record(content)
	return nil
}

func (c *Clipboard) record(content transfer.Content) {
	if IsSensitive(content.Mimes) {
		return
	}
	mime, ok := BestMime(content.Mimes)
	if !ok {
		return
	}
	read, _ := c.startRead(mime)
	if read == nil {
		return
	}
	err := content.Write(mime, read.buf)
	if err != nil && !errors.Is(err, offheap.ErrBufferFull) {
		log.Printf("clipboard: cannot remember the %s copy: %v", mime, err)
	}
	c.finishRead(read, err == nil)
}

func finishPaste(mime string, w io.WriteCloser, err error) {
	if err != nil {
		log.Printf("clipboard: cannot paste the %s entry: %v", mime, err)
	}
	if err := w.Close(); err != nil {
		log.Printf("clipboard: cannot finish pasting the %s entry: %v", mime, err)
	}
}

func (c *Clipboard) serve(src app.SelectionSource, release func()) error {
	cl, err := c.dev.claim(src)
	if err != nil {
		release()
		return err
	}
	s := &serving{claim: cl, release: release}
	c.serving = s
	cl.OnCancelled(func() {
		s.end()
		if c.serving == s {
			c.serving = nil
		}
	})
	return nil
}

// Close gives up a restored selection and frees the history; the
// clipboard records nothing afterwards. Pastes still being written
// free their entry when they finish; reads still in flight are freed
// now, and their transfers end on the next write. Loop goroutine, or
// once the loop ended.
func (c *Clipboard) Close() {
	if s := c.serving; s != nil {
		c.serving = nil
		s.claim.Release()
		s.end()
	}
	for _, r := range c.reads {
		r.discard()
	}
	c.reads = nil
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.history.Close()
}

// selectionChanged reads whatever just landed on the clipboard into
// the history. Loop goroutine.
func (c *Clipboard) selectionChanged(offer selectionOffer) {
	// Our own restore announced back: reading it would put this
	// process on both ends of the pipe, and it is already in the
	// history, which is how it got here.
	if offer == nil || offer.Own() {
		return
	}
	mimes := offer.Mimes()
	if len(mimes) == 0 || IsSensitive(mimes) {
		return
	}
	mime, ok := BestMime(mimes)
	if !ok {
		return
	}
	read, limit := c.startRead(mime)
	if read == nil {
		return
	}
	err := offer.ReadTo(mime, int64(limit), read.buf, func(_ int64, err error) {
		// An oversized selection is refused whole, the same verdict the
		// history would give it; anything else is worth a line.
		if err != nil && !errors.Is(err, app.ErrTransferTooLarge) {
			log.Printf("clipboard: cannot read the selection (%s): %v", mime, err)
		}
		c.finishRead(read, err == nil)
	})
	if err != nil {
		log.Printf("clipboard: cannot read the selection (%s): %v", mime, err)
		c.finishRead(read, false)
	}
}

func (c *Clipboard) startRead(mime string) (*pendingRead, int) {
	c.mu.Lock()
	limit := c.history.maxEntryBytes
	mem := &c.history.mem
	closed := c.closed
	c.mu.Unlock()
	if limit == 0 || closed {
		return nil, 0
	}
	buf, err := offheap.NewBuffer(mem, limit)
	if err != nil {
		log.Printf("clipboard: cannot read the selection (%s): %v", mime, err)
		return nil, 0
	}
	read := &pendingRead{mime: mime, buf: buf}
	c.reads = append(c.reads, read)
	return read, limit
}

// finishRead marks a read done, freeing a failed one's partial bytes
// at once, and records what the queue can.
func (c *Clipboard) finishRead(r *pendingRead, ok bool) {
	r.done, r.ok = true, ok
	if !ok {
		r.discard()
	}
	c.flushReads()
}

// flushReads records every finished read at the head of the queue,
// stopping at the first one still in flight. Reads finishing after
// Close were freed with the queue.
func (c *Clipboard) flushReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	for len(c.reads) > 0 && c.reads[0].done {
		if r := c.reads[0]; r.ok {
			c.remember(r)
		}
		c.reads[0] = nil
		c.reads = c.reads[1:]
	}
}

// remember hands a finished read's bytes to the history. c.mu held.
func (c *Clipboard) remember(r *pendingRead) {
	buf, err := r.buf.Take()
	if err != nil {
		log.Printf("clipboard: cannot take the %s selection read: %v", r.mime, err)
		return
	}
	if _, _, err := c.history.pushOwned(r.mime, buf); err != nil {
		log.Printf("clipboard: cannot remember the selection (%s, %d bytes): %v", r.mime, buf.Len(), err)
	}
}

// dataControl adapts lys-ui's device to selectionDevice.
type dataControl struct{ dc *app.DataControl }

func (d dataControl) watch(fn func(selectionOffer)) {
	d.dc.OnSelection(func(sel app.Selection, offer *app.SelectionOffer) {
		if sel != app.SelectionClipboard {
			return
		}
		if offer == nil {
			fn(nil)
			return
		}
		fn(offer)
	})
}

func (d dataControl) claim(src app.SelectionSource) (selectionClaim, error) {
	cl, err := d.dc.SetSelection(app.SelectionClipboard, src)
	if err != nil {
		return nil, err
	}
	return cl, nil
}
