package lyswmtest

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

type captureFake struct {
	keymap   string
	commands chan string
	events   chan string
	opened   chan struct{}
	closed   chan struct{}
}

// ServeCapture makes the fake a lys-wm that serves input captures: each
// opens with keymap, every command it reads is acknowledged and
// recorded for ExpectCaptureCommand, and SendCapture writes to the
// newest one.
func (w *WM) ServeCapture(keymap string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.capture = &captureFake{
		keymap: keymap, commands: make(chan string, 64), events: make(chan string, 64),
		opened: make(chan struct{}, 16), closed: make(chan struct{}, 16),
	}
	w.answers["capture"] = ack
}

func (w *WM) captureFake(t *testing.T) *captureFake {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capture == nil {
		t.Fatal("lyswmtest: the fake serves no capture; call ServeCapture")
	}
	return w.capture
}

func (w *WM) serveCapture(conn net.Conn, reader *bufio.Reader, done <-chan struct{}) {
	w.mu.Lock()
	c := w.capture
	w.mu.Unlock()
	keymap, err := json.Marshal(map[string]string{"event": "keymap", "keymap": c.keymap})
	if err != nil {
		return
	}
	writes := make(chan string, 64)
	writes <- string(keymap)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			c.commands <- strings.TrimSpace(line)
			writes <- `{"event":"applied"}`
		}
	}()
	c.opened <- struct{}{}
	defer func() { c.closed <- struct{}{} }()
	for {
		var line string
		select {
		case line = <-writes:
		case line = <-c.events:
		case <-ended:
			return
		case <-done:
			return
		}
		if _, err := conn.Write([]byte(line + "\n")); err != nil {
			return
		}
	}
}

// AwaitCapture waits for a capture to open.
func (w *WM) AwaitCapture(t *testing.T) {
	t.Helper()
	select {
	case <-w.captureFake(t).opened:
	case <-time.After(timeout):
		t.Fatal("lyswmtest: no capture opened")
	}
}

// AwaitCaptureClosed waits for a capture to close.
func (w *WM) AwaitCaptureClosed(t *testing.T) {
	t.Helper()
	select {
	case <-w.captureFake(t).closed:
	case <-time.After(timeout):
		t.Fatal("lyswmtest: no capture closed")
	}
}

// ExpectCaptureCommand checks the next command line a capture sent.
func (w *WM) ExpectCaptureCommand(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-w.captureFake(t).commands:
		if got != want {
			t.Errorf("capture command %s, want %s", got, want)
		}
	case <-time.After(timeout):
		t.Fatalf("no capture command, want %s", want)
	}
}

// SendCapture writes event lines to the open capture.
func (w *WM) SendCapture(t *testing.T, lines ...string) {
	t.Helper()
	c := w.captureFake(t)
	for _, line := range lines {
		select {
		case c.events <- line:
		case <-time.After(timeout):
			t.Fatal("lyswmtest: the capture takes no more events")
		}
	}
}
