package lyswm_test

import (
	"testing"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

func awaitWindows(t *testing.T, w *lyswm.WindowsWatcher, want func([]lyswm.Window) bool) []lyswm.Window {
	t.Helper()
	for {
		if got := w.Windows(); want(got) {
			return got
		}
		awaitTick(t, w.Watcher)
	}
}

// The watcher starts from the windows request and keeps every window
// the events report: one opening, its title, app id, rect and output,
// minimize, stack and focus, and its close. Events of other kinds,
// an output object among them, leave the windows and the stream alone.
func TestWatchWindowsFollowsEveryWindowEvent(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("windows", `[{"id":1,"app_id":"foot","title":"~","workspace":1,"output":"DP-1","rect":{"x":0,"y":0,"width":100,"height":100},"stack":0}]`)
	w, err := lyswm.WatchWindows()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	f.AwaitWatch(t)
	if got := w.Windows(); len(got) != 1 || got[0].AppID != "foot" || got[0].Stack == nil || *got[0].Stack != 0 {
		t.Fatalf("seed = %+v", got)
	}
	for _, e := range []string{
		`{"event":"output","change":"changed","output":{"name":"DP-1","workspace":1}}`,
		`{"event":"open","window":{"id":2,"app_id":"mpv","title":"","workspace":1,"output":"DP-1","rect":{"x":0,"y":0,"width":10,"height":10}}}`,
		`{"event":"title","window":2,"title":"film"}`,
		`{"event":"appid","window":2,"app_id":"io.mpv.Mpv"}`,
		`{"event":"rect","window":2,"output":"HDMI-A-1","rect":{"x":5,"y":6,"width":7,"height":8}}`,
		`{"event":"minimized","window":1,"minimized":true}`,
		`{"event":"stack","window":1,"stack":null}`,
		`{"event":"focus","window":2}`,
		`{"event":"title","window":9,"title":"no such window"}`,
	} {
		f.Events <- e
	}
	got := awaitWindows(t, w, func(ws []lyswm.Window) bool { return len(ws) == 2 && ws[1].Focused })
	one, two := got[0], got[1]
	if !one.Minimized || one.Stack != nil || one.Focused {
		t.Errorf("window 1 = %+v, want minimized, unstacked and unfocused", one)
	}
	if two.Title != "film" || two.AppID != "io.mpv.Mpv" || two.Output != "HDMI-A-1" || two.Rect != (lyswm.Rect{X: 5, Y: 6, Width: 7, Height: 8}) {
		t.Errorf("window 2 = %+v", two)
	}
	f.Events <- `{"event":"close","window":1}`
	got = awaitWindows(t, w, func(ws []lyswm.Window) bool { return len(ws) == 1 })
	if got[0].ID != 2 {
		t.Errorf("after closing window 1 the windows are %+v", got)
	}
	f.Events <- `{"event":"focus","window":null}`
	awaitWindows(t, w, func(ws []lyswm.Window) bool { return !ws[0].Focused })
}

// An open for a window the seed already holds replaces it rather than
// listing it twice.
func TestWatchWindowsOpenOfASeededWindowReplacesIt(t *testing.T) {
	f := lyswmtest.Start(t)
	f.Result("windows", `[{"id":3,"app_id":"foot","title":"old"}]`)
	w, err := lyswm.WatchWindows()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	f.AwaitWatch(t)
	f.Events <- `{"event":"open","window":{"id":3,"app_id":"foot","title":"new"}}`
	got := awaitWindows(t, w, func(ws []lyswm.Window) bool { return len(ws) > 0 && ws[0].Title == "new" })
	if len(got) != 1 {
		t.Fatalf("windows = %+v, want the one window", got)
	}
}
