// Package dialogqueue orders the requests one shell dialog surface
// serves: one shows at a time, and the rest wait their turn in arrival
// order instead of cancelling the one showing.
package dialogqueue

import "slices"

// entry is one waiting request.
type entry struct {
	handle string
	start  func()
	cancel func()
}

// Queue is one surface's requests. Loop goroutine only.
type Queue struct {
	showing bool
	handle  string
	waiting []entry
}

// Enqueue shows the request for handle at once when nothing shows,
// else queues it: start shows it, cancel answers it cancelled without
// showing it (when it is dismissed while waiting).
func (q *Queue) Enqueue(handle string, start, cancel func()) {
	if q.showing {
		q.waiting = append(q.waiting, entry{handle, start, cancel})
		return
	}
	q.showing, q.handle = true, handle
	start()
}

// Done marks the showing request answered and shows the next one.
func (q *Queue) Done() {
	q.showing, q.handle = false, ""
	if len(q.waiting) == 0 {
		return
	}
	next := q.waiting[0]
	q.waiting = slices.Delete(q.waiting, 0, 1)
	q.showing, q.handle = true, next.handle
	next.start()
}

// Showing reports whether handle names the request showing.
func (q *Queue) Showing(handle string) bool {
	return q.showing && handle != "" && q.handle == handle
}

// Dismiss drops a waiting request for handle, answering it cancelled;
// it reports whether handle is instead the one showing, which the
// caller takes down (and then calls Done).
func (q *Queue) Dismiss(handle string) (showing bool) {
	if handle == "" {
		return false
	}
	if q.Showing(handle) {
		return true
	}
	for i, e := range q.waiting {
		if e.handle == handle {
			q.waiting = slices.Delete(q.waiting, i, i+1)
			e.cancel()
			return false
		}
	}
	return false
}

// Len is the number of requests waiting behind the one showing.
func (q *Queue) Len() int { return len(q.waiting) }
