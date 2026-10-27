package notifications

import (
	"fmt"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/fakeclock"
)

// collect waits for n events.
func collect(t *testing.T, feed <-chan Event, n int) []Event {
	t.Helper()
	var out []Event
	deadline := time.After(time.Second)
	for len(out) < n {
		select {
		case ev := <-feed:
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("got %d events, want %d", len(out), n)
		}
	}
	return out
}

// newTestService isolates the DND state dir per test.
// newTestService is a service on a fake clock (advance drives it), its
// state in a temporary directory.
func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := NewService()
	s.UseClock(fakeclock.New())
	return s
}

// testClock is a test service's fake clock.
func testClock(s *Service) *fakeclock.Clock {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock.(*fakeclock.Clock)
}

// advance moves a test service's fake clock d forward, running the
// countdowns and expiries that fall due.
func advance(s *Service, d time.Duration) { testClock(s).Advance(d) }

func TestNotifyStoresAndEmits(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	id := s.Notify("mail", 0, "", "New mail", "hello", nil, 0)
	if id == 0 {
		t.Fatal("ids start at one")
	}
	ev := collect(t, feed, 1)[0]
	if ev.Kind != EventAdd || ev.Notif.ID != id || ev.Notif.Summary != "New mail" {
		t.Fatalf("event = %+v", ev)
	}
	if got := s.Count(); got != 1 {
		t.Fatalf("count = %d", got)
	}
	// IDs increment without reuse.
	if id2 := s.Notify("mail", 0, "", "Second", "", nil, 0); id2 != id+1 {
		t.Fatalf("second id = %d, want %d", id2, id+1)
	}
}

func TestReplacesID(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	id := s.Notify("app", 0, "", "First", "", nil, 0)
	collect(t, feed, 1)
	again := s.Notify("app", id, "", "Second", "", nil, 0)
	if again != id {
		t.Fatalf("replaces returned %d, want %d", again, id)
	}
	<-feed
	if got := s.Count(); got != 1 {
		t.Fatalf("count = %d, want the replacement only", got)
	}
}

func TestBlocklistConsumes(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	s.SetBlocklist([]string{"noisy*"})
	id := s.Notify("noisy-app", 0, "", "spam", "", nil, 0)
	if id == 0 {
		t.Fatal("blocked notifications still return an id")
	}
	select {
	case ev := <-feed:
		t.Fatalf("blocked notification leaked: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if got := s.Count(); got != 0 {
		t.Fatalf("count = %d, want 0", got)
	}
}

func TestDNDSuppressesPopups(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	s.Notify("app", 0, "", "ping", "", nil, 0)
	<-feed
	if len(s.Popups()) != 1 {
		t.Fatal("no dnd: the notification should be a popup")
	}
	s.SetDND(true)
	if !s.DND() {
		t.Fatal("dnd flag lost")
	}
	select {
	case ev := <-feed:
		if ev.Kind != EventDnd {
			t.Fatalf("event = %+v, want the dnd event", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no dnd event")
	}
	// A popup already up stays; DND keeps new ones off the list while
	// the history still takes them (handle_popup_added).
	s.Notify("app", 0, "", "quiet", "", nil, 0)
	<-feed
	if got := len(s.Popups()); got != 1 {
		t.Fatalf("popups under dnd = %d, want only the one shown before", got)
	}
	if got := s.Count(); got != 2 {
		t.Fatalf("history under dnd = %d, want both", got)
	}
}

func TestExpiryRemoves(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	s.Notify("app", 0, "", "flash", "", nil, 30)
	<-feed
	advance(s, 29*time.Millisecond)
	if got := s.Count(); got != 1 {
		t.Fatalf("expired early, count = %d", got)
	}
	advance(s, time.Millisecond)
	if got := s.Count(); got != 0 {
		t.Fatalf("expired notification stayed, count = %d", got)
	}
	sawExpired := false
	for len(feed) > 0 {
		if ev := <-feed; ev.Kind == EventRemove && ev.Reason == Expired {
			sawExpired = true
		}
	}
	if !sawExpired {
		t.Fatal("no expired close event")
	}
}

func TestZeroTimeoutNeverExpires(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	s.Notify("app", 0, "", "sticky", "", nil, 0)
	<-feed
	advance(s, 24*time.Hour)
	if got := s.Count(); got != 1 {
		t.Fatalf("expire_timeout 0 removed the notification")
	}
}

func TestDismissAll(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	s.Notify("app", 0, "", "one", "", nil, 0)
	s.Notify("app", 0, "", "two", "", nil, 0)
	collect(t, feed, 2)
	s.DismissAll()
	deadline := time.Now().Add(2 * time.Second)
	for s.Count() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.Count(); got != 0 {
		t.Fatalf("dismiss all left %d", got)
	}
}

// An action press's token goes out as ActivationToken right before
// ActionInvoked, as the spec orders them; without one only
// ActionInvoked does; an unknown id emits nothing.
func TestInvokeAction(t *testing.T) {
	s := newTestService(t)
	feed, _ := s.Subscribe()
	var signals []string
	s.SetEmitter(func(signal string, args ...any) { signals = append(signals, sprint(signal, args)) })
	id := s.Notify("app", 0, "", "hi", "", []string{"reply", "Reply"}, 0)
	<-feed
	s.InvokeAction(id, "reply", "tok-1")
	want := []string{
		sprint(Interface+".ActivationToken", []any{id, "tok-1"}),
		sprint(Interface+".ActionInvoked", []any{id, "reply"}),
	}
	if len(signals) < 2 || signals[0] != want[0] || signals[1] != want[1] {
		t.Fatalf("signals = %v, want %v first", signals, want)
	}
	signals = nil
	other := s.Notify("app", 0, "", "again", "", []string{"reply", "Reply"}, 0)
	<-feed
	s.InvokeAction(other, "reply", "")
	if len(signals) == 0 || signals[0] != sprint(Interface+".ActionInvoked", []any{other, "reply"}) {
		t.Fatalf("signals without a token = %v", signals)
	}
	signals = nil
	s.InvokeAction(9999, "nope", "tok-2")
	if len(signals) != 0 {
		t.Errorf("an unknown id emitted %v", signals)
	}
}

func sprint(signal string, args []any) string { return fmt.Sprint(signal, args) }

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"noisy*", "noisy-app", true},
		{"noisy*", "quiet-app", false},
		{"*slack*", "Slack Desktop", true},
		{"exact", "exact", true},
		{"exact", "exactly", false},
	} {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestDNDPersistence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := NewService()
	if s.DND() {
		t.Fatal("a fresh state dir starts with dnd off")
	}
	s.SetDND(true)
	restored := NewService()
	if !restored.DND() {
		t.Fatal("the dnd flag did not survive a restart")
	}
	s.SetDND(false)
	restored = NewService()
	if restored.DND() {
		t.Fatal("dnd off did not persist")
	}
}

// history is the first page of s's history, newest first.
func history(t *testing.T, s *Service) []*Notification {
	t.Helper()
	got, err := s.Page(nil, MaxPage)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
