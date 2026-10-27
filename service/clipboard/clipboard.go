package clipboard

import (
	"errors"
	"io"
	"log"
	"sync"

	"github.com/stubbedev/lys-ui/app"
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

// pendingRead is one selection read awaiting its bytes.
type pendingRead struct {
	mime string
	data []byte
	done bool
}

// serving is a restored entry on the clipboard: the claim holds the
// entry until the claim ends, so pasting it still works after the
// history dropped it.
type serving struct {
	claim selectionClaim
	p     *payload
	once  sync.Once
}

// end lets go of the entry; once, however the claim ended.
func (s *serving) end() { s.once.Do(s.p.release) }

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
	Read(mime string, limit int64, done func([]byte, error)) error
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
	cl, err := c.dev.claim(app.SelectionSource{
		Mimes: offerMimes(mime),
		// The receiver may ask under any mime offered; every alias is
		// an alias for the entry's own type, so the bytes are the same.
		Send: func(_ string, w io.WriteCloser) {
			p.hold()
			go func() {
				_, err := w.Write(p.bytes())
				p.release()
				if err != nil {
					log.Printf("clipboard: cannot paste the %s entry: %v", mime, err)
				}
				if err := w.Close(); err != nil {
					log.Printf("clipboard: cannot finish pasting the %s entry: %v", mime, err)
				}
			}()
		},
	})
	if err != nil {
		p.release()
		log.Printf("clipboard: cannot take the selection: %v", err)
		return
	}
	s := &serving{claim: cl, p: p}
	c.serving = s
	cl.OnCancelled(func() {
		s.end()
		if c.serving == s {
			c.serving = nil
		}
	})
}

// Close gives up a restored selection and frees the history; the
// clipboard records nothing afterwards. Pastes still being written
// free their entry when they finish. Loop goroutine, or once the loop
// ended.
func (c *Clipboard) Close() {
	if s := c.serving; s != nil {
		c.serving = nil
		s.claim.Release()
		s.end()
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
	c.mu.Lock()
	limit := int64(c.history.maxEntryBytes)
	closed := c.closed
	c.mu.Unlock()
	if limit == 0 || closed {
		return
	}

	read := &pendingRead{mime: mime}
	c.reads = append(c.reads, read)
	err := offer.Read(mime, limit, func(data []byte, err error) {
		// An oversized selection is refused whole, the same verdict the
		// history would give it; anything else is worth a line.
		if err != nil && !errors.Is(err, app.ErrTransferTooLarge) {
			log.Printf("clipboard: cannot read the selection (%s): %v", mime, err)
		}
		read.data, read.done = data, true
		c.flushReads()
	})
	if err != nil {
		log.Printf("clipboard: cannot read the selection (%s): %v", mime, err)
		read.done = true
		c.flushReads()
	}
}

// flushReads records every finished read at the head of the queue,
// stopping at the first one still in flight. Reads finishing after
// Close are dropped with the queue.
func (c *Clipboard) flushReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	for len(c.reads) > 0 && c.reads[0].done {
		if r := c.reads[0]; r.data != nil {
			if _, _, err := c.history.Push(r.mime, r.data); err != nil {
				log.Printf("clipboard: cannot remember the selection (%s, %d bytes): %v", r.mime, len(r.data), err)
			}
		}
		c.reads[0] = nil
		c.reads = c.reads[1:]
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
