package dialogqueue

import (
	"slices"
	"testing"
)

// Requests show one at a time, in arrival order; a waiting request
// dismissed is answered cancelled and never shows.
func TestQueueOrdersAndDismisses(t *testing.T) {
	var q Queue
	var log []string
	add := func(h string) {
		q.Enqueue(h, func() { log = append(log, "show "+h) }, func() { log = append(log, "cancel "+h) })
	}
	add("a")
	add("b")
	add("c")
	if !q.Showing("a") || q.Showing("b") || q.Len() != 2 {
		t.Fatalf("showing a: %v, waiting %d", q.Showing("a"), q.Len())
	}
	if q.Dismiss("b") || q.Dismiss("") || q.Dismiss("zzz") {
		t.Error("a waiting, empty or unknown handle reported as showing")
	}
	if !q.Dismiss("a") {
		t.Error("the showing request was not reported as showing")
	}
	q.Done()
	q.Done()
	q.Done()
	if want := []string{"show a", "cancel b", "show c"}; !slices.Equal(log, want) {
		t.Errorf("log = %q, want %q", log, want)
	}
	if q.Showing("c") || q.Len() != 0 {
		t.Error("the queue did not drain")
	}
	add("d")
	if !q.Showing("d") {
		t.Error("a request into an idle queue did not show at once")
	}
}
