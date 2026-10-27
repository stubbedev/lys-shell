// Package handoff carries what work off the loop makes onto the loop,
// and accounts for what the loop never takes: once the loop has ended
// an Invoke is dropped, so a result still in flight at shutdown would
// otherwise be lost with whatever it holds (an off-heap raster).
package handoff

import (
	"slices"
	"sync"
)

// Handoffs tracks the work started through it and the results it
// handed to the loop that the loop has not taken yet. Its zero value is
// ready for use. Go and Deliver are safe from any goroutine.
type Handoffs struct {
	mu      sync.Mutex
	running sync.WaitGroup
	closed  bool
	next    uint64
	// unclaimed are the results on their way to the loop, each with
	// what frees it when the loop never takes it.
	unclaimed map[uint64]func()
}

// Go runs work through spawn and reports true, or reports false and
// runs nothing once Close has begun.
func (h *Handoffs) Go(spawn func(func()), work func()) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	h.running.Add(1)
	h.mu.Unlock()
	spawn(func() {
		defer h.running.Done()
		work()
	})
	return true
}

// Closed reports whether Close has begun: work still to do is wasted,
// as nothing will take its result.
func (h *Handoffs) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// Deliver hands a result to the loop through invoke: land takes it
// there. When the loop never runs land, because Close began first or
// the loop ended with it queued, Close runs free instead. Exactly one
// of land and free runs.
func (h *Handoffs) Deliver(invoke func(func()), land, free func()) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		free()
		return
	}
	id := h.next
	h.next++
	if h.unclaimed == nil {
		h.unclaimed = map[uint64]func(){}
	}
	h.unclaimed[id] = free
	h.mu.Unlock()
	invoke(func() {
		h.mu.Lock()
		_, ok := h.unclaimed[id]
		delete(h.unclaimed, id)
		h.mu.Unlock()
		if ok {
			land()
		}
	})
}

// Close starts no more work, waits for the work running, and frees
// every result the loop has not taken, in the order they were
// delivered. It runs once the loop has ended (or on the loop), so no
// land runs after it. A second Close does nothing more.
func (h *Handoffs) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.running.Wait()
	h.mu.Lock()
	unclaimed := h.unclaimed
	h.unclaimed = nil
	h.mu.Unlock()
	ids := make([]uint64, 0, len(unclaimed))
	for id := range unclaimed {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		unclaimed[id]()
	}
}
