// Package lyswmtest is a fake lys-wm on a private IPC socket, advertised
// through lyswm.SocketEnv for the test's duration.
package lyswmtest

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-shell/service/lyswm"
)

const (
	ack     = `{"version":1}`
	unknown = `{"version":1,"error":"unknown request"}`
	timeout = 2 * time.Second
)

// WM is the fake. It answers each request by its name, records every
// request line, reports dispatched actions, and streams Events to the
// watch connections.
type WM struct {
	mu       sync.Mutex
	answers  map[string]string
	requests []string

	dispatched chan string
	watching   chan struct{}
	Events     chan string
}

// Start serves the fake and advertises it. It answers a lys-wm with no
// windows, the default binds, the English (US) layout and every lock key
// off.
func Start(t *testing.T) *WM {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "wayland-1.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("lyswmtest: listen on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	w := &WM{
		answers:    map[string]string{},
		dispatched: make(chan string, 16),
		watching:   make(chan struct{}, 16),
		Events:     make(chan string, 16),
	}
	for request, result := range map[string]string{
		"workspaces":   `[]`,
		"windows":      `[]`,
		"outputs":      `[]`,
		"activewindow": `null`,
		"submap":       `""`,
		"layout":       `{"index":0,"name":"English (US)"}`,
		"locks":        `{"caps":false,"num":false,"scroll":false}`,
	} {
		w.Result(request, result)
	}
	w.Answer("dispatch", ack)
	w.Answer("watch", ack)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go w.serve(conn, done)
		}
	}()
	t.Setenv(lyswm.SocketEnv, path)
	return w
}

// Answer sets the whole response line to a request.
func (w *WM) Answer(request, response string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.answers[request] = response
}

// Result answers a request with the given JSON result.
func (w *WM) Result(request, result string) {
	w.Answer(request, `{"version":1,"result":`+result+`}`)
}

// Requests are the request lines received so far, in order.
func (w *WM) Requests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

func (w *WM) serve(conn net.Conn, done <-chan struct{}) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		Request string `json:"request"`
		Action  string `json:"action"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	w.mu.Lock()
	w.requests = append(w.requests, string(line[:len(line)-1]))
	answer, ok := w.answers[req.Request]
	w.mu.Unlock()
	if !ok {
		answer = unknown
	}
	if req.Request == "dispatch" {
		w.dispatched <- req.Action
	}
	if _, err := conn.Write([]byte(answer + "\n")); err != nil {
		return
	}
	if req.Request != "watch" {
		return
	}
	w.watching <- struct{}{}
	for {
		select {
		case e := <-w.Events:
			if _, err := conn.Write([]byte(e + "\n")); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// AwaitWatch waits for a watch connection.
func (w *WM) AwaitWatch(t *testing.T) {
	t.Helper()
	select {
	case <-w.watching:
	case <-time.After(timeout):
		t.Fatal("lyswmtest: nothing watched the event stream")
	}
}

// ExpectDispatch waits for the next dispatched action and checks it.
func (w *WM) ExpectDispatch(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-w.dispatched:
		if got != want {
			t.Errorf("dispatched %q, want %q", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("nothing dispatched, want %q", want)
	}
}

// ExpectNoDispatch checks that no action is dispatched for a while.
func (w *WM) ExpectNoDispatch(t *testing.T) {
	t.Helper()
	select {
	case got := <-w.dispatched:
		t.Errorf("dispatched %q, want nothing", got)
	case <-time.After(200 * time.Millisecond):
	}
}
