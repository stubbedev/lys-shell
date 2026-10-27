package lyswm_test

import (
	"errors"
	"slices"
	"strings"
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
	f.Result("windows", `[{"id":7,"app_id":"foot","title":"~","pid":4242,"workspace":1,"output":"DP-1","rect":{"x":10,"y":20,"width":300,"height":400},"floating":true,"fullscreen":false,"urgent":true,"unresponsive":false,"focused":true,"content_type":"none","icon":null}]`)
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
	wantWin := lyswm.Window{ID: 7, AppID: "foot", Title: "~", Workspace: 1, Output: "DP-1", Rect: lyswm.Rect{X: 10, Y: 20, Width: 300, Height: 400}, Floating: true, Urgent: true, Focused: true}
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
	f.Events <- `not json`
	select {
	case _, ok := <-w.Ticks():
		if ok {
			t.Fatal("a malformed line ticked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end")
	}
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
