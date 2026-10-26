package dbus

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// recorder logs the calls it serves; Slow takes its time first, or
// waits for Fast when release is set.
type recorder struct {
	mu      sync.Mutex
	got     []string
	release chan struct{}
}

func (r *recorder) log(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, name)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *recorder) Slow() *Error {
	if r.release != nil {
		<-r.release
	} else {
		time.Sleep(50 * time.Millisecond)
	}
	r.log("slow")
	return nil
}

func (r *recorder) Fast() *Error {
	r.log("fast")
	if r.release != nil {
		close(r.release)
	}
	return nil
}

// waitSeen waits until r served n calls.
func waitSeen(t *testing.T, r *recorder, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.seen(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("served %v, want %d calls", r.seen(), n)
	return nil
}

// TestOrderedCallsRunInArrivalOrder is the fourth lys-shell patch:
// calls to members DispatchInOrder names run one at a time in arrival
// order, even when the caller does not wait for replies; calls to
// other members still run concurrently, so one that blocks does not
// hold back the next.
func TestOrderedCallsRunInArrivalOrder(t *testing.T) {
	server, client, cleanup := pairedConns(t)
	defer cleanup()
	ordered := &recorder{}
	if err := server.Export(ordered, "/o", "a.ordered"); err != nil {
		t.Fatal(err)
	}
	server.DispatchInOrder("a.ordered", "Slow", "Fast")
	free := &recorder{release: make(chan struct{})}
	if err := server.Export(free, "/f", "a.free"); err != nil {
		t.Fatal(err)
	}
	o := client.Object(server.Names()[0], "/o")
	for range 3 {
		o.Go("a.ordered.Slow", FlagNoReplyExpected, nil)
		o.Go("a.ordered.Fast", FlagNoReplyExpected, nil)
	}
	if got := waitSeen(t, ordered, 6); !slices.Equal(got, []string{"slow", "fast", "slow", "fast", "slow", "fast"}) {
		t.Errorf("ordered calls ran %v, want them in arrival order", got)
	}

	f := client.Object(server.Names()[0], "/f")
	f.Go("a.free.Slow", FlagNoReplyExpected, nil)
	f.Go("a.free.Fast", FlagNoReplyExpected, nil)
	if got := waitSeen(t, free, 2); !slices.Equal(got, []string{"fast", "slow"}) {
		t.Errorf("unordered calls ran %v, want the blocked Slow overtaken", got)
	}
}
