package clipboard

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/stubbedev/lys-shell/offheap"
)

// ErrNoPinStore is a pin with nowhere to keep it: the session has no
// data directory.
var ErrNoPinStore = errors.New("clipboard: pinned entries have no store")

// CanPin reports whether entries can be pinned at all.
func (c *Clipboard) CanPin() bool { return c.pins != nil }

// Pin keeps an entry for good: listed first, never evicted, and written
// to the pinned store before Pin returns. Refused past the pinned
// budget, with ErrPinnedFull.
func (c *Clipboard) Pin(id uint64) error {
	if c.pins == nil {
		return ErrNoPinStore
	}
	c.load(id)
	c.mu.Lock()
	err := c.history.Pin(id)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("clipboard: pin entry %d: %w", id, err)
	}
	if err := c.store(c.pins, true); err != nil {
		c.mu.Lock()
		c.history.Unpin(id)
		c.mu.Unlock()
		return fmt.Errorf("clipboard: pin entry %d: %w", id, err)
	}
	c.changed()
	return nil
}

// Unpin returns an entry to the rest of the history and removes it
// from the pinned store; the budget may then evict it like any other.
func (c *Clipboard) Unpin(id uint64) error {
	if c.pins == nil {
		return ErrNoPinStore
	}
	c.mu.Lock()
	ok := c.history.Unpin(id)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	c.changed()
	if err := c.store(c.pins, true); err != nil {
		return fmt.Errorf("clipboard: unpin entry %d: %w", id, err)
	}
	return nil
}

// load reads the bytes of entries restored from disk, all of them for
// id 0. An entry whose stored bytes are gone or damaged is forgotten,
// said in the log; any other read failure leaves it for the next try.
func (c *Clipboard) load(id uint64) {
	c.mu.Lock()
	todo := c.history.unloaded(id)
	pins := c.pins
	var saved *Store
	if c.saver != nil {
		saved = c.saver.store
	}
	c.mu.Unlock()
	mem := &c.history.mem
	for _, d := range todo {
		store := saved
		if d.pinned {
			store = pins
		}
		if store == nil {
			continue
		}
		buf, err := offheap.NewBuffer(mem, d.rec.Size)
		if err == nil {
			err = store.Read(d.rec, buf)
		}
		if err != nil {
			if buf != nil {
				err = errors.Join(err, buf.Free())
			}
			log.Printf("clipboard: restoring entry %d: %v", d.id, err)
			if errors.Is(err, ErrCorrupt) || errors.Is(err, os.ErrNotExist) {
				c.Forget(d.id)
			}
			continue
		}
		data, err := buf.Take()
		if err != nil {
			log.Printf("clipboard: restoring entry %d: %v", d.id, err)
			continue
		}
		c.mu.Lock()
		filled := c.history.fill(d.id, data)
		c.mu.Unlock()
		if !filled {
			if err := offheap.Free(mem, data); err != nil {
				log.Printf("clipboard: freeing restored entry %d: %v", d.id, err)
			}
		}
	}
}

// store writes the pinned or the unpinned entries to s as they stand.
// One store runs at a time, so a later state never lands under an
// earlier one.
func (c *Clipboard) store(s *Store, pinned bool) error {
	c.storing.Lock()
	defer c.storing.Unlock()
	c.mu.Lock()
	held := c.history.held(pinned)
	c.mu.Unlock()
	defer func() {
		for _, e := range held {
			if e.p != nil {
				e.p.release()
			}
		}
	}()
	items := make([]storeItem, len(held))
	for i := range held {
		e := &held[i]
		if !e.summed {
			e.digest = DigestOf(e.p.bytes())
		}
		items[i].rec = Record{Digest: e.digest, Mime: e.mime, Size: e.size}
		if e.p != nil {
			items[i].data = e.p.bytes()
		}
	}
	err := s.Sync(items)
	c.mu.Lock()
	for _, e := range held {
		if !e.summed {
			c.history.setDigest(e.id, e.digest)
		}
	}
	c.mu.Unlock()
	return err
}

// saver keeps the unpinned entries on disk, writing the history as it
// stands whenever it changed: changes while a write runs fold into the
// next, so nothing queues up behind a slow disk.
type saver struct {
	store *Store
	dirty chan struct{}
	done  chan struct{}
}

// SetPersist starts keeping the unpinned entries in store, or, with
// nil, stops and removes what was kept: the entries restored from it
// and not read since go with it, the rest stay in memory.
func (c *Clipboard) SetPersist(store *Store) error {
	c.mu.Lock()
	running := c.saver != nil
	c.mu.Unlock()
	switch {
	case store != nil && !running:
		c.startSaver(store)
		c.changed()
		return nil
	case store == nil && running:
		c.mu.Lock()
		c.history.dropUnloaded()
		c.mu.Unlock()
		return c.stopSaver(false)
	}
	return nil
}

func (c *Clipboard) startSaver(store *Store) {
	sv := &saver{store: store, dirty: make(chan struct{}, 1), done: make(chan struct{})}
	c.mu.Lock()
	c.saver = sv
	c.mu.Unlock()
	go func() {
		defer close(sv.done)
		for range sv.dirty {
			if err := c.store(sv.store, false); err != nil {
				log.Printf("clipboard: saving the history: %v", err)
			}
		}
	}()
}

// stopSaver ends the saver, then either saves the history one last
// time (keep) or empties its store.
func (c *Clipboard) stopSaver(keep bool) error {
	c.mu.Lock()
	sv := c.saver
	c.saver = nil
	if sv != nil {
		close(sv.dirty)
	}
	c.mu.Unlock()
	if sv == nil {
		return nil
	}
	<-sv.done
	if keep {
		return c.store(sv.store, false)
	}
	return sv.store.Sync(nil)
}

func (c *Clipboard) changed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.changedLocked()
}

// changedLocked wakes the saver, if any; a wake already pending covers
// this change too. c.mu held.
func (c *Clipboard) changedLocked() {
	if c.saver == nil {
		return
	}
	select {
	case c.saver.dirty <- struct{}{}:
	default:
	}
}

// The stores' directories under the shell's data directory.
const (
	pinnedDir  = "clipboard-pinned"
	historyDir = "clipboard-history"
)

// OpenPinnedStore opens the pinned entries' store under dataDir, the
// shell's data directory.
func OpenPinnedStore(dataDir string) (*Store, error) {
	return OpenStore(filepath.Join(dataDir, pinnedDir))
}

// OpenHistoryStore opens the persisted history's store under dataDir.
func OpenHistoryStore(dataDir string) (*Store, error) {
	return OpenStore(filepath.Join(dataDir, historyDir))
}

// Persisting reports whether the unpinned entries are kept on disk.
func (c *Clipboard) Persisting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saver != nil
}
