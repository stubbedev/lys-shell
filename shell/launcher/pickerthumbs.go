package launcher

import (
	"image"
	"log"
	"slices"
	"sync"
)

// thumbJob is one cell's thumbnail asked for: the file and what shows it
// on the loop.
type thumbJob struct {
	path string
	done func(image.Image)
}

// thumbQueue makes thumbnails off the loop for the cells in view: newest
// first, and only the last limit asked for, as a cell asked for earlier
// has scrolled out by then. One worker runs while anything waits.
type thumbQueue struct {
	spawn  func(func())
	invoke func(func())
	load   func(path string) (image.Image, error)
	limit  int

	mu      sync.Mutex
	pending []thumbJob
	running bool
	closed  bool
}

func newThumbQueue(limit int, load func(string) (image.Image, error), spawn, invoke func(func())) *thumbQueue {
	return &thumbQueue{spawn: spawn, invoke: invoke, load: load, limit: max(1, limit)}
}

// request queues path's thumbnail; done runs on the loop with it.
func (q *thumbQueue) request(path string, done func(image.Image)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pending = append(q.pending, thumbJob{path: path, done: done})
	if over := len(q.pending) - q.limit; over > 0 {
		q.pending = slices.Delete(q.pending, 0, over)
	}
	if !q.running {
		q.running = true
		q.spawn(q.work)
	}
}

// close drops what waits; a thumbnail being made still lands, and is
// ignored by its cell's picker.
func (q *thumbQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.pending = nil
	q.mu.Unlock()
}

func (q *thumbQueue) work() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		job := q.pending[len(q.pending)-1]
		q.pending = q.pending[:len(q.pending)-1]
		q.mu.Unlock()
		img, err := q.load(job.path)
		if err != nil {
			log.Printf("launcher: %v", err)
		}
		if img != nil {
			q.invoke(func() { job.done(img) })
		}
	}
}

// thumbCache keeps the most recently shown thumbnails, so scrolling back
// shows them at once; older ones are read from the thumbnail cache again.
type thumbCache struct {
	size  int
	tick  uint64
	byKey map[string]*thumbEntry
}

type thumbEntry struct {
	img  image.Image
	used uint64
}

func newThumbCache(size int) *thumbCache {
	return &thumbCache{size: size, byKey: make(map[string]*thumbEntry, size+1)}
}

func (c *thumbCache) get(path string) (image.Image, bool) {
	e, ok := c.byKey[path]
	if !ok {
		return nil, false
	}
	c.tick++
	e.used = c.tick
	return e.img, true
}

func (c *thumbCache) put(path string, img image.Image) {
	c.tick++
	c.byKey[path] = &thumbEntry{img: img, used: c.tick}
	if len(c.byKey) <= c.size {
		return
	}
	oldest, at := "", c.tick
	for p, e := range c.byKey {
		if e.used < at {
			oldest, at = p, e.used
		}
	}
	delete(c.byKey, oldest)
}
