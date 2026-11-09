package notifications

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/fakeclock"
	"github.com/stubbedev/lys-desktop/service/notifications/notifypolicy"
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
	s := newService(t)
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
	settle(s)
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
	settle(s)
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
	s := newService(t)
	if s.DND() {
		t.Fatal("a fresh state dir starts with dnd off")
	}
	s.SetDND(true)
	settle(s)
	restored := newService(t)
	if !restored.DND() {
		t.Fatal("the dnd flag did not survive a restart")
	}
	s.SetDND(false)
	settle(s)
	restored = newService(t)
	if restored.DND() {
		t.Fatal("dnd off did not persist")
	}
}

func TestDNDIsSavedWhereTheServiceReadIt(t *testing.T) {
	read := t.TempDir()
	t.Setenv("XDG_STATE_HOME", read)
	s := newService(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s.SetDND(true)
	settle(s)
	t.Setenv("XDG_STATE_HOME", read)
	if !newService(t).DND() {
		t.Fatal("dnd turned on after the state dir moved did not reach the dir the service read")
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

// A dismissal is one change to the shell however much it took: one
// event naming the ids, past the feed's buffer, and one closed signal
// for each app. The rest of the window keeps its order, and what the
// service does not hold announces nothing.
func TestDismissIsOneRemoval(t *testing.T) {
	s := newTestService(t)
	var ids, kept []uint32
	for i := range 80 {
		id := s.Notify("app", 0, "", "n", "", nil, 0)
		if i%2 == 0 {
			ids = append(ids, id)
		} else {
			kept = append(kept, id)
		}
	}
	feed, _ := s.Subscribe()
	closed := closedSignals(s)

	s.Dismiss(append(slices.Clone(ids), ids[0], 9999))
	ev := collect(t, feed, 1)[0]
	if ev.Kind != EventRemove || ev.Reason != Dismissed || !slices.Equal(ev.IDs, ids) {
		t.Errorf("event = %+v, want one removal of %v", ev, ids)
	}
	if len(feed) != 0 {
		t.Errorf("%d events past the one removal", len(feed))
	}
	if !slices.Equal(*closed, ids) {
		t.Errorf("closed signals = %v, want one each for %v", *closed, ids)
	}
	var left []uint32
	for _, n := range slices.Backward(history(t, s)) {
		left = append(left, n.ID)
	}
	if !slices.Equal(left, kept) || s.Count() != len(kept) || len(s.Popups()) != len(kept) {
		t.Errorf("left %v (count %d, popups %d), want %v", left, s.Count(), len(s.Popups()), kept)
	}

	s.Dismiss(ids)
	s.Dismiss([]uint32{9999})
	s.Dismiss(nil)
	if len(feed) != 0 || len(*closed) != len(ids) {
		t.Errorf("dismissing what is gone: %d events, closed %v", len(feed), *closed)
	}
}

// Dismissing popups stops their countdowns and only theirs.
func TestDismissStopsOnlyItsPopupTimers(t *testing.T) {
	s := newTestService(t)
	gone := s.Notify("app", 0, "", "gone", "", nil, -1)
	stays := s.Notify("app", 0, "", "stays", "", nil, -1)
	s.Dismiss([]uint32{gone})
	if _, armed := s.timers[gone]; armed {
		t.Error("a dismissed popup kept its countdown")
	}
	if got := s.Popups(); len(got) != 1 || got[0].ID != stays {
		t.Fatalf("popups = %v, want the other one", got)
	}
	advance(s, DefaultPopupDuration)
	if got := len(s.Popups()); got != 0 {
		t.Errorf("the other popup's countdown no longer runs: %d popups", got)
	}
}

// Clear all is one removal too, naming the in-memory window; with only
// disk left it still tells the shell, and an empty history tells nobody.
func TestDismissAllIsOneRemoval(t *testing.T) {
	st, _ := openTestStore(t)
	s := newStoredService(t, st)
	memory, err := notifypolicy.NewMemoryLimit(notifypolicy.MinHistoryMemory)
	if err != nil {
		t.Fatal(err)
	}
	s.SetHistoryLimits(memory, notifypolicy.DefaultStoredLimit())
	var ids []uint32
	for range 2 * notifypolicy.MinHistoryMemory {
		advance(s, time.Millisecond)
		ids = append(ids, s.Notify("app", 0, "", "n", "", nil, 0))
	}
	window := ids[notifypolicy.MinHistoryMemory:]
	feed, _ := s.Subscribe()
	closed := closedSignals(s)

	s.DismissAll()
	settle(s)
	ev := collect(t, feed, 1)[0]
	if ev.Kind != EventRemove || !slices.Equal(ev.IDs, window) || len(feed) != 0 {
		t.Errorf("event = %+v with %d more, want one removal of %v", ev, len(feed), window)
	}
	if !slices.Equal(*closed, window) || s.Count() != 0 || len(stored(t, st)) != 0 {
		t.Errorf("closed %v, count %d, stored %d", *closed, s.Count(), len(stored(t, st)))
	}

	s.DismissAll()
	settle(s)
	if len(feed) != 0 || len(*closed) != len(window) {
		t.Errorf("clearing an empty history: %d events, closed %v", len(feed), *closed)
	}

	for range 2 * notifypolicy.MinHistoryMemory {
		advance(s, time.Millisecond)
		s.Notify("app", 0, "", "n", "", nil, 0)
	}
	collect(t, feed, 2*notifypolicy.MinHistoryMemory)
	inWindow := history(t, s)[:notifypolicy.MinHistoryMemory]
	shown := make([]uint32, 0, len(inWindow))
	for _, n := range inWindow {
		shown = append(shown, n.ID)
	}
	s.Dismiss(shown)
	settle(s)
	collect(t, feed, 1)
	*closed = nil

	s.DismissAll()
	settle(s)
	ev = collect(t, feed, 1)[0]
	if ev.Kind != EventRemove || len(ev.IDs) != 0 || len(*closed) != 0 || s.Count() != 0 {
		t.Errorf("clearing only disk: event %+v, closed %v, count %d", ev, *closed, s.Count())
	}
}

// The shell learns of a dismissal while the bus is still taking the
// signals, and the service stays usable meanwhile.
func TestClosedSignalsDoNotHoldTheService(t *testing.T) {
	s := newTestService(t)
	ids := []uint32{s.Notify("app", 0, "", "a", "", nil, 0), s.Notify("app", 0, "", "b", "", nil, 0)}
	feed, _ := s.Subscribe()
	release := make(chan struct{})
	var sent []uint32
	s.SetEmitter(func(_ string, args ...any) {
		<-release
		sent = append(sent, args[0].(uint32))
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Dismiss(ids)
	}()

	ev := collect(t, feed, 1)[0]
	if ev.Kind != EventRemove || s.Count() != 0 || len(s.Popups()) != 0 {
		t.Errorf("while the bus blocks: event %+v, count %d, popups %d", ev, s.Count(), len(s.Popups()))
	}
	close(release)
	<-done
	if !slices.Equal(sent, ids) {
		t.Errorf("signals = %v, want %v", sent, ids)
	}
}

// A subscriber that rereads the history on HistoryChanges sees the last
// change even when the event feed is full and drops it; popup and DND
// changes leave the history alone and do not tick.
func TestHistoryChangesSurviveAFullFeed(t *testing.T) {
	s := newTestService(t)
	const popups = 40
	var ids []uint32
	for range popups {
		ids = append(ids, s.Notify("app", 0, "", "n", "", nil, 0))
	}
	feed, _ := s.Subscribe()
	history, _ := s.HistoryChanges()
	for _, id := range ids {
		s.DismissPopup(id)
	}
	s.SetDND(false)
	settle(s)
	if len(history) != 0 {
		t.Fatal("a popup or DND change ticked the history")
	}

	s.Close(ids[0], Dismissed)
	s.Close(ids[1], Dismissed)
	if len(history) != 1 {
		t.Errorf("history ticks pending = %d, want the two closes as one", len(history))
	}
	for len(feed) > 0 {
		if ev := <-feed; ev.Kind == EventRemove {
			t.Fatal("the feed kept the removal, so this does not test a drop")
		}
	}
}

func BenchmarkDismiss(b *testing.B) {
	b.Setenv("XDG_STATE_HOME", b.TempDir())
	for _, n := range []int{1000, 5000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			memory, err := notifypolicy.NewMemoryLimit(notifypolicy.MaxHistoryMemory)
			if err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				b.StopTimer()
				st, err := OpenStore(filepath.Join(b.TempDir(), "notifications.db"))
				if err != nil {
					b.Fatal(err)
				}
				s := newService(b)
				s.SetHistoryLimits(memory, notifypolicy.DefaultStoredLimit())
				if err := s.AttachStore(st); err != nil {
					b.Fatal(err)
				}
				ids := make([]uint32, 0, n)
				for range n {
					ids = append(ids, s.Notify("app", 0, "", "n", "", nil, 0))
				}
				b.StartTimer()
				s.Dismiss(ids)
				b.StopTimer()
				if err := st.Close(); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}
