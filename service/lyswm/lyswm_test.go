package lyswm_test

import (
	"errors"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

func awaitTick(t *testing.T, w *lyswm.Watcher) {
	t.Helper()
	select {
	case _, ok := <-w.Ticks():
		if !ok {
			t.Fatal("the stream ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick")
	}
}

func TestNotRunning(t *testing.T) {
	t.Setenv(lyswm.SocketEnv, "")
	if lyswm.IsRunning() {
		t.Fatal("an empty socket variable reads as running")
	}
	if _, err := lyswm.Workspaces(); !errors.Is(err, lyswm.ErrNotRunning) {
		t.Fatalf("Workspaces err = %v", err)
	}
	if err := lyswm.Dispatch("workspace 2"); !errors.Is(err, lyswm.ErrNotRunning) {
		t.Fatalf("Dispatch err = %v", err)
	}
	if _, err := lyswm.Watch(); !errors.Is(err, lyswm.ErrNotRunning) {
		t.Fatalf("Watch err = %v", err)
	}
}

func TestQueriesDecodeTheResults(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("workspaces", `[{"id":1,"output":"DP-1","windows":3,"visible":true,"focused":true},{"id":4,"output":"HDMI-A-1","windows":0,"visible":true,"focused":false}]`)
	f.Result("windows", `[{"id":7,"app_id":"foot","title":"~","pid":4242,"workspace":1,"output":"DP-1","rect":{"x":10,"y":20,"width":300,"height":400},"floating":true,"fullscreen":false,"minimized":true,"urgent":true,"unresponsive":false,"focused":true,"content_type":"none","icon":null}]`)
	f.Result("outputs", `[{"name":"DP-1","description":"LG","rect":{"x":0,"y":0,"width":2048,"height":1152},"scale":1.25,"mode":{"width":2560,"height":1440,"refresh_mhz":143912},"workspace":1,"focused":true,"powered":true}]`)
	workspaces, err := lyswm.Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	want := []lyswm.Workspace{{ID: 1, Output: "DP-1", Windows: 3, Visible: true, Focused: true}, {ID: 4, Output: "HDMI-A-1", Visible: true}}
	if !slices.Equal(workspaces, want) {
		t.Fatalf("workspaces = %+v", workspaces)
	}
	windows, err := lyswm.Windows()
	if err != nil {
		t.Fatal(err)
	}
	wantWin := lyswm.Window{ID: 7, AppID: "foot", Title: "~", Workspace: 1, Output: "DP-1", Rect: lyswm.Rect{X: 10, Y: 20, Width: 300, Height: 400}, Floating: true, Minimized: true, Urgent: true, Focused: true}
	if len(windows) != 1 || windows[0] != wantWin {
		t.Fatalf("windows = %+v", windows)
	}
	outputs, err := lyswm.Outputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Name != "DP-1" || !outputs[0].Focused || outputs[0].Scale != 1.25 || outputs[0].Rect.Width != 2048 {
		t.Fatalf("outputs = %+v", outputs)
	}
	wantSent := []string{`{"request":"workspaces"}`, `{"request":"windows"}`, `{"request":"outputs"}`}
	if got := f.Requests(); !slices.Equal(got, wantSent) {
		t.Fatalf("requests = %q", got)
	}
}

func TestActiveWindowNullIsNoWindow(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Answer("activewindow", `{"version":1}`)
	if w, ok, err := lyswm.ActiveWindow(); err != nil || ok {
		t.Fatalf("ActiveWindow = %+v, %v, %v; want no window", w, ok, err)
	}
}

func TestActiveWindow(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("activewindow", `{"id":3,"app_id":"firefox","title":"Docs","focused":true}`)
	w, ok, err := lyswm.ActiveWindow()
	if err != nil || !ok || w.ID != 3 || w.AppID != "firefox" || w.Title != "Docs" {
		t.Fatalf("ActiveWindow = %+v, %v, %v", w, ok, err)
	}
}

func TestDispatchSendsTheAction(t *testing.T) {
	f := lyswmtest.Start(t)
	if err := lyswm.Dispatch("workspace e+1"); err != nil {
		t.Fatal(err)
	}
	f.ExpectDispatch(t, "workspace e+1")
	if got := f.Requests(); len(got) != 1 || got[0] != `{"request":"dispatch","action":"workspace e+1"}` {
		t.Fatalf("requests = %q", got)
	}
}

func TestDispatchRejected(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Answer("dispatch", `{"version":1,"error":"bad action: unknown action \"teleport\""}`)
	err := lyswm.Dispatch("teleport")
	var rejected *lyswm.RejectedError
	if !errors.As(err, &rejected) || rejected.Request != "dispatch" || !strings.Contains(rejected.Message, "teleport") {
		t.Fatalf("err = %v, want the rejection", err)
	}
}

func TestUnknownVersionIsRefused(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Answer("workspaces", `{"version":2,"result":[]}`)
	f.Answer("watch", `{"version":2}`)
	if _, err := lyswm.Workspaces(); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Workspaces err = %v, want a version refusal", err)
	}
	if _, err := lyswm.Watch(); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Watch err = %v, want a version refusal", err)
	}
}

func TestWatchSendsOnlyTheWatchRequest(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if got, want := f.Requests(), []string{`{"request":"watch"}`}; !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func checkSeed[T comparable](t *testing.T, s lyswm.State[T], kind, seed string, want T) {
	t.Helper()
	f := lyswmtest.Start(t)
	f.Result(kind, seed)
	w, err := lyswm.WatchState(s)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if got := w.Value(); got != want {
		t.Errorf("seeded %s = %+v, want %+v", kind, got, want)
	}
	if got, want := f.Requests(), []string{`{"request":"watch"}`, `{"request":"` + kind + `"}`}; !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
	select {
	case <-w.Ticks():
		t.Error("seeding ticked")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchStateSeedsWithoutAnEvent(t *testing.T) {
	t.Run("submap", func(t *testing.T) { checkSeed(t, lyswm.SubmapState, "submap", `"resize"`, "resize") })
	t.Run("layout", func(t *testing.T) {
		checkSeed(t, lyswm.LayoutState, "layout", `{"index":1,"name":"German"}`, lyswm.Layout{Index: 1, Name: "German"})
	})
	t.Run("locks", func(t *testing.T) {
		checkSeed(t, lyswm.LocksState, "locks", `{"caps":false,"num":true,"scroll":false}`, lyswm.Locks{Num: true})
		checkSeed(t, lyswm.ReducedMotionState, "reducedmotion", `true`, true)
	})
}

func checkFollow[T comparable](t *testing.T, s lyswm.State[T], event string, want T) {
	t.Helper()
	f := lyswmtest.Start(t)
	w, err := lyswm.WatchState(s)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	seeded := w.Value()
	f.Events <- `{"event":"focus","window":null}`
	f.Events <- `{"event":"title","window":3,"title":"x"}`
	select {
	case <-w.Ticks():
		t.Fatal("ticked on an event of another kind")
	case <-time.After(200 * time.Millisecond):
	}
	if got := w.Value(); got != seeded {
		t.Fatalf("another kind's event changed the value to %+v", got)
	}
	f.Events <- event
	awaitTick(t, w.Watcher)
	if got := w.Value(); got != want {
		t.Fatalf("value = %+v, want %+v", got, want)
	}
}

func TestWatchStateFollowsItsEvent(t *testing.T) {
	t.Run("submap", func(t *testing.T) {
		checkFollow(t, lyswm.SubmapState, `{"event":"submap","submap":"resize"}`, "resize")
	})
	t.Run("layout", func(t *testing.T) {
		checkFollow(t, lyswm.LayoutState, `{"event":"layout","layout":{"index":1,"name":"German"}}`, lyswm.Layout{Index: 1, Name: "German"})
	})
	t.Run("locks", func(t *testing.T) {
		checkFollow(t, lyswm.LocksState, `{"event":"locks","locks":{"caps":true,"num":true,"scroll":false}}`, lyswm.Locks{Caps: true, Num: true})
		checkFollow(t, lyswm.ReducedMotionState, `{"event":"reducedmotion","reduced_motion":true}`, true)
	})
}

func TestWatchStateEventAfterTheStreamOpensWinsOverTheSeed(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Events <- `{"event":"locks","locks":{"caps":true,"num":false,"scroll":true}}`
	w, err := lyswm.WatchState(lyswm.LocksState)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	awaitTick(t, w.Watcher)
	if l := w.Value(); l != (lyswm.Locks{Caps: true, Scroll: true}) {
		t.Errorf("locks = %+v, want the event's Caps and Scroll Lock over the seeded all off", l)
	}
}

func checkSeedFails[T any](t *testing.T, s lyswm.State[T], request, answer, want string) {
	t.Helper()
	f := lyswmtest.Start(t)
	f.Answer(request, answer)
	w, err := lyswm.WatchState(s)
	if err == nil {
		w.Close()
		t.Fatal("WatchState succeeded without a seed")
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("WatchState err = %v, want %q", err, want)
	}
}

func TestWatchStateFailsWhenTheSeedFails(t *testing.T) {
	rejected := `{"version":1,"error":"unknown request"}`
	t.Run("submap rejected", func(t *testing.T) {
		checkSeedFails(t, lyswm.SubmapState, "submap", rejected, "seed the submap: lys-wm rejected submap: unknown request")
	})
	t.Run("submap null", func(t *testing.T) {
		checkSeedFails(t, lyswm.SubmapState, "submap", `{"version":1,"result":null}`, "seed the submap: lys-wm: submap answered no result")
	})
	t.Run("layout rejected", func(t *testing.T) {
		checkSeedFails(t, lyswm.LayoutState, "layout", rejected, "seed the layout: lys-wm rejected layout: unknown request")
	})
	t.Run("layout missing", func(t *testing.T) {
		checkSeedFails(t, lyswm.LayoutState, "layout", `{"version":1}`, "seed the layout: lys-wm: layout answered no result")
	})
	t.Run("locks rejected", func(t *testing.T) {
		checkSeedFails(t, lyswm.LocksState, "locks", rejected, "seed the locks: lys-wm rejected locks: unknown request")
	})
	t.Run("locks null", func(t *testing.T) {
		checkSeedFails(t, lyswm.LocksState, "locks", `{"version":1,"result":null}`, "seed the locks: lys-wm: locks answered no result")
	})
}

func TestWatchTicksOnlyOnItsKinds(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.Watch(lyswm.EventFocus)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.Events <- `{"event":"title","window":3,"title":"x"}`
	f.Events <- `{"event":"layout","layout":{"index":2,"name":"French"}}`
	select {
	case <-w.Ticks():
		t.Fatal("ticked on an event it does not follow")
	case <-time.After(200 * time.Millisecond):
	}
	f.Events <- `{"event":"focus","window":null}`
	awaitTick(t, w)
}

func TestWatchEndsOnAMalformedLine(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f.Events <- `not json`
	select {
	case _, ok := <-w.Ticks():
		if ok {
			t.Fatal("a malformed line ticked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end")
	}
	if !strings.Contains(logged.String(), "not json") {
		t.Errorf("the stream ended without saying why: %q", logged.String())
	}
}

// Closing a stream is not a failure to report.
func TestWatchClosedLogsNothing(t *testing.T) {
	lyswmtest.Start(t)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	w.Close()
	select {
	case <-w.Ticks():
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end")
	}
	if logged.String() != "" {
		t.Errorf("logged %q", logged.String())
	}
}

// syncBuffer is a log destination the watcher's goroutine and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestWatchEndsWhenClosed(t *testing.T) {
	lyswmtest.Start(t)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	select {
	case _, ok := <-w.Ticks():
		if ok {
			t.Fatal("ticked after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ticks stayed open after Close")
	}
}

func TestFollowReducedMotionSeedsFollowsAndSaysWhenItEnds(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("reducedmotion", `true`)
	seen := make(chan bool, 4)
	gone := make(chan struct{})
	stop, err := lyswm.FollowReducedMotion(func(on bool) { seen <- on }, func() { close(gone) })
	if err != nil {
		t.Fatal(err)
	}
	expect := func(want bool) {
		t.Helper()
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("set(%v), want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no call of set")
		}
	}
	expect(true)
	f.Events <- `{"event":"reducedmotion","reduced_motion":false}`
	expect(false)
	stop()
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("gone was not called when the stream ended")
	}
}

func TestWatchTitlesDropsHiddenWindows(t *testing.T) {
	f := lyswmtest.Start(t)
	announced := make(chan struct{}, 4)
	w, err := lyswm.WatchTitles(func(window uint64) bool { return window == 9 }, func() { announced <- struct{}{} }, lyswm.EventWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.AwaitWatch(t)
	expect := func(what string, ticks bool) {
		t.Helper()
		wait := 2 * time.Second
		if !ticks {
			wait = 200 * time.Millisecond
		}
		select {
		case <-w.Ticks():
			if !ticks {
				t.Errorf("%s ticked", what)
			}
			if len(announced) != 1 {
				t.Errorf("%s: %d announcements ahead of its tick", what, len(announced))
			}
			<-announced
		case <-time.After(wait):
			if ticks {
				t.Errorf("%s never ticked", what)
			}
		}
	}
	f.Events <- `{"event":"title","window":9,"title":"hidden"}`
	expect("a hidden window's title", false)
	f.Events <- `{"event":"title","window":4,"title":"shown"}`
	expect("a shown window's title", true)
	f.Events <- `{"event":"workspace","output":"DP-1","workspace":3}`
	expect("a workspace", true)
	f.Events <- `{"event":"submap","submap":"resize"}`
	expect("another kind", false)
}

func TestWatchSurvivesAnEventWithAWindowObject(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.WatchState(lyswm.LayoutState)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.AwaitWatch(t)
	f.Events <- `{"event":"open","window":{"id":7,"app_id":"foot"}}`
	f.Events <- `{"event":"focus","window":null}`
	f.Events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	select {
	case _, ok := <-w.Ticks():
		if !ok {
			t.Fatal("the stream ended on the window object")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick for the layout after the window object")
	}
	if got := w.Value(); got.Name != "German" {
		t.Errorf("the layout reads %q", got.Name)
	}
}

func TestWindowsReadTheirStackingOrder(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("windows", `[{"id":1,"output":"DP-1","stack":2},{"id":2,"output":"DP-1"},{"id":3,"output":"DP-2","stack":0},{"id":4,"output":"DP-1","stack":0},{"id":5,"output":"DP-1","stack":null}]`)
	windows, err := lyswm.Windows()
	if err != nil {
		t.Fatal(err)
	}
	if windows[0].Stack == nil || *windows[0].Stack != 2 {
		t.Errorf("window 1 stack = %v, want 2", windows[0].Stack)
	}
	if windows[1].Stack != nil || windows[4].Stack != nil {
		t.Error("a window no output shows read a stack index")
	}
	var ids []uint64
	for _, w := range lyswm.Stacked(windows) {
		ids = append(ids, w.ID)
	}
	if want := []uint64{3, 4, 1}; !slices.Equal(ids, want) {
		t.Errorf("stacked %v, want %v: the shown ones front to back, oldest first on a tie", ids, want)
	}
	if len(windows) != 5 {
		t.Errorf("Stacked changed the list it was given: %d windows", len(windows))
	}
}

func TestWatchTicksOnStackEvents(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.Watch(lyswm.EventStack)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.Events <- `{"event":"focus","window":3}`
	select {
	case <-w.Ticks():
		t.Fatal("ticked on an event it does not follow")
	case <-time.After(200 * time.Millisecond):
	}
	f.Events <- `{"event":"stack","window":3,"stack":null}`
	awaitTick(t, w)
}
