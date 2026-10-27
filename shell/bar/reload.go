package bar

import (
	"sync"

	"github.com/stubbedev/lys-shell/config"
)

// reloadQueue hands config snapshots to the loop, coalescing the ones
// published before the loop got to them into one apply: from the
// snapshot the bars last saw to the latest.
type reloadQueue struct {
	invoke func(func())
	apply  func(old, next *config.Config)

	mu        sync.Mutex
	old, next *config.Config
}

func newReloadQueue(invoke func(func()), apply func(old, next *config.Config)) *reloadQueue {
	return &reloadQueue{invoke: invoke, apply: apply}
}

// push queues one published change; it is a config.Service subscriber
// and runs on any goroutine.
func (q *reloadQueue) push(old, next *config.Config) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.next != nil {
		q.next = next
		return
	}
	q.old, q.next = old, next
	q.invoke(q.flush)
}

// flush applies the queued change on the loop.
func (q *reloadQueue) flush() {
	q.mu.Lock()
	old, next := q.old, q.next
	q.old, q.next = nil, nil
	q.mu.Unlock()
	q.apply(old, next)
}
