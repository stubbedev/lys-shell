package lyswm

import (
	"bufio"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"
)

type fakeWM struct {
	mu       sync.Mutex
	requests []string
	answers  map[string]string
	events   chan string
	done     chan struct{}
}

func newFakeWM(t *testing.T, answers map[string]string) *fakeWM {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "wayland-1.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	t.Setenv(SocketEnv, path)
	f := &fakeWM{answers: answers, events: make(chan string, 16), done: make(chan struct{})}
	t.Cleanup(func() { close(f.done) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeWM) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSuffix(line, "\n")
	f.mu.Lock()
	f.requests = append(f.requests, line)
	f.mu.Unlock()
	answer, ok := f.answers[line]
	if !ok {
		answer = `{"version":1,"error":"unknown request"}`
	}
	if _, err := conn.Write([]byte(answer + "\n")); err != nil {
		return
	}
	if line != `{"request":"watch"}` {
		return
	}
	for {
		select {
		case e := <-f.events:
			if _, err := conn.Write([]byte(e + "\n")); err != nil {
				return
			}
		case <-f.done:
			return
		}
	}
}

func (f *fakeWM) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

const watchAck = `{"version":1}`

func awaitTick(t *testing.T, w *Watcher) {
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
	t.Setenv(SocketEnv, "")
	if IsRunning() {
		t.Fatal("an empty socket variable reads as running")
	}
	if _, err := Workspaces(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Workspaces err = %v", err)
	}
	if err := Dispatch("workspace 2"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Dispatch err = %v", err)
	}
	if _, err := Watch(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Watch err = %v", err)
	}
}

func TestQueriesDecodeTheResults(t *testing.T) {
	f := newFakeWM(t, map[string]string{
		`{"request":"workspaces"}`: `{"version":1,"result":[{"id":1,"output":"DP-1","windows":3,"visible":true,"focused":true},{"id":4,"output":"HDMI-A-1","windows":0,"visible":true,"focused":false}]}`,
		`{"request":"windows"}`:    `{"version":1,"result":[{"id":7,"app_id":"foot","title":"~","pid":4242,"workspace":1,"output":"DP-1","rect":{"x":10,"y":20,"width":300,"height":400},"floating":true,"fullscreen":false,"urgent":true,"unresponsive":false,"focused":true,"content_type":"none","icon":null}]}`,
		`{"request":"outputs"}`:    `{"version":1,"result":[{"name":"DP-1","description":"LG","rect":{"x":0,"y":0,"width":2048,"height":1152},"scale":1.25,"mode":{"width":2560,"height":1440,"refresh_mhz":143912},"workspace":1,"focused":true,"powered":true}]}`,
	})
	workspaces, err := Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	want := []Workspace{{ID: 1, Output: "DP-1", Windows: 3, Visible: true, Focused: true}, {ID: 4, Output: "HDMI-A-1", Visible: true}}
	if len(workspaces) != 2 || workspaces[0] != want[0] || workspaces[1] != want[1] {
		t.Fatalf("workspaces = %+v", workspaces)
	}
	windows, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	wantWin := Window{ID: 7, AppID: "foot", Title: "~", Workspace: 1, Output: "DP-1", Rect: Rect{10, 20, 300, 400}, Floating: true, Urgent: true, Focused: true}
	if len(windows) != 1 || windows[0] != wantWin {
		t.Fatalf("windows = %+v", windows)
	}
	outputs, err := Outputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Name != "DP-1" || !outputs[0].Focused || outputs[0].Scale != 1.25 || outputs[0].Rect.Width != 2048 {
		t.Fatalf("outputs = %+v", outputs)
	}
	if got := f.sent(); len(got) != 3 {
		t.Fatalf("requests = %q", got)
	}
}

func TestActiveWindowNullIsNoWindow(t *testing.T) {
	newFakeWM(t, map[string]string{`{"request":"activewindow"}`: `{"version":1}`})
	if w, ok, err := ActiveWindow(); err != nil || ok {
		t.Fatalf("ActiveWindow = %+v, %v, %v; want no window", w, ok, err)
	}
}

func TestActiveWindow(t *testing.T) {
	newFakeWM(t, map[string]string{`{"request":"activewindow"}`: `{"version":1,"result":{"id":3,"app_id":"firefox","title":"Docs","focused":true}}`})
	w, ok, err := ActiveWindow()
	if err != nil || !ok || w.ID != 3 || w.AppID != "firefox" || w.Title != "Docs" {
		t.Fatalf("ActiveWindow = %+v, %v, %v", w, ok, err)
	}
}

func TestDispatchSendsTheAction(t *testing.T) {
	f := newFakeWM(t, map[string]string{`{"request":"dispatch","action":"workspace e+1"}`: `{"version":1}`})
	if err := Dispatch("workspace e+1"); err != nil {
		t.Fatal(err)
	}
	if got := f.sent(); len(got) != 1 || got[0] != `{"request":"dispatch","action":"workspace e+1"}` {
		t.Fatalf("requests = %q", got)
	}
}

func TestDispatchRejected(t *testing.T) {
	newFakeWM(t, map[string]string{`{"request":"dispatch","action":"teleport"}`: `{"version":1,"error":"bad action: unknown action \"teleport\""}`})
	err := Dispatch("teleport")
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Request != "dispatch" || !strings.Contains(rejected.Message, "teleport") {
		t.Fatalf("err = %v, want the rejection", err)
	}
}

func TestUnknownVersionIsRefused(t *testing.T) {
	newFakeWM(t, map[string]string{
		`{"request":"workspaces"}`: `{"version":2,"result":[]}`,
		`{"request":"watch"}`:      `{"version":2}`,
	})
	if _, err := Workspaces(); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Workspaces err = %v, want a version refusal", err)
	}
	if _, err := Watch(); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Watch err = %v, want a version refusal", err)
	}
}

func TestWatchKeepsSubmapAndLayout(t *testing.T) {
	f := newFakeWM(t, map[string]string{`{"request":"watch"}`: watchAck})
	w, err := Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Submap() != "" {
		t.Fatalf("submap before any event = %q", w.Submap())
	}
	if _, ok := w.Layout(); ok {
		t.Fatal("a layout before the stream reported one")
	}
	f.events <- `{"event":"submap","submap":"resize"}`
	awaitTick(t, w)
	if w.Submap() != "resize" {
		t.Fatalf("submap = %q", w.Submap())
	}
	f.events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	awaitTick(t, w)
	if l, ok := w.Layout(); !ok || l != (Layout{Index: 1, Name: "German"}) {
		t.Fatalf("layout = %+v, %v", l, ok)
	}
	f.events <- `{"event":"submap","submap":""}`
	awaitTick(t, w)
	if w.Submap() != "" {
		t.Fatalf("submap after reset = %q", w.Submap())
	}
}

func TestWatchTicksOnlyOnItsKinds(t *testing.T) {
	f := newFakeWM(t, map[string]string{`{"request":"watch"}`: watchAck})
	w, err := Watch(EventFocus)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	f.events <- `{"event":"title","window":3,"title":"x"}`
	f.events <- `{"event":"layout","layout":{"index":0,"name":"English (US)"}}`
	select {
	case <-w.Ticks():
		t.Fatal("ticked on an event it does not follow")
	case <-time.After(200 * time.Millisecond):
	}
	if l, ok := w.Layout(); !ok || l.Name != "English (US)" {
		t.Fatalf("an unfollowed layout event was not kept: %+v, %v", l, ok)
	}
	f.events <- `{"event":"focus","window":null}`
	awaitTick(t, w)
}

func TestWatchEndsOnAMalformedLine(t *testing.T) {
	f := newFakeWM(t, map[string]string{`{"request":"watch"}`: watchAck})
	w, err := Watch()
	if err != nil {
		t.Fatal(err)
	}
	f.events <- `not json`
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
	newFakeWM(t, map[string]string{`{"request":"watch"}`: watchAck})
	w, err := Watch()
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
