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

func watchRequests() []string {
	return []string{`{"request":"watch"}`, `{"request":"submap"}`, `{"request":"layout"}`}
}

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

func TestWatchSeedsSubmapAndLayoutWithoutAnEvent(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("submap", `"resize"`)
	f.Result("layout", `{"index":1,"name":"German"}`)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Submap() != "resize" {
		t.Errorf("seeded submap = %q, want resize", w.Submap())
	}
	if l := w.Layout(); l != (lyswm.Layout{Index: 1, Name: "German"}) {
		t.Errorf("seeded layout = %+v, want German", l)
	}
	if got := f.Requests(); !slices.Equal(got, watchRequests()) {
		t.Errorf("requests = %q, want %q", got, watchRequests())
	}
	select {
	case <-w.Ticks():
		t.Error("seeding ticked")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchEventAfterTheStreamOpensWinsOverTheSeed(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	f.Events <- `{"event":"submap","submap":"resize"}`
	w, err := lyswm.Watch(lyswm.EventSubmap)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	awaitTick(t, w)
	if l := w.Layout(); l != (lyswm.Layout{Index: 1, Name: "German"}) {
		t.Errorf("layout = %+v, want the event's German over the seeded English (US)", l)
	}
	if w.Submap() != "resize" {
		t.Errorf("submap = %q, want the event's resize over the seeded default", w.Submap())
	}
}

func TestWatchFailsWhenTheSeedFails(t *testing.T) {
	for _, c := range []struct {
		name, request, answer, want string
	}{
		{"submap rejected", "submap", `{"version":1,"error":"unknown request"}`, "seed the submap: lys-wm rejected submap: unknown request"},
		{"submap null", "submap", `{"version":1,"result":null}`, "seed the submap: lys-wm: submap answered no result"},
		{"layout rejected", "layout", `{"version":1,"error":"unknown request"}`, "seed the layout: lys-wm rejected layout: unknown request"},
		{"layout missing", "layout", `{"version":1}`, "seed the layout: lys-wm: layout answered no result"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := lyswmtest.Start(t)
			f.Answer(c.request, c.answer)
			w, err := lyswm.Watch()
			if err == nil {
				w.Close()
				t.Fatal("Watch succeeded without a seed")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Watch err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestWatchKeepsSubmapAndLayout(t *testing.T) {
	f := lyswmtest.Start(t)
	w, err := lyswm.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.Events <- `{"event":"submap","submap":"resize"}`
	awaitTick(t, w)
	if w.Submap() != "resize" {
		t.Fatalf("submap = %q", w.Submap())
	}
	f.Events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	awaitTick(t, w)
	if l := w.Layout(); l != (lyswm.Layout{Index: 1, Name: "German"}) {
		t.Fatalf("layout = %+v", l)
	}
	f.Events <- `{"event":"submap","submap":""}`
	awaitTick(t, w)
	if w.Submap() != "" {
		t.Fatalf("submap after reset = %q", w.Submap())
	}
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
	if l := w.Layout(); l.Name != "French" {
		t.Fatalf("an unfollowed layout event was not kept: %+v", l)
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
