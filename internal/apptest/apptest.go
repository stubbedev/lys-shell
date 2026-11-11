// Package apptest runs a lys-ui application on a private headless sway
// for a test, and waits on what it does.
package apptest

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// settle is how long what must not happen gets to happen anyway before
// a test concludes it does not.
const settle = 300 * time.Millisecond

// Run starts a private headless sway, env overriding its environment
// (swaytest.Start), and runs an application on it for the rest of the
// test. Cleanups the test registers afterwards run first, while the
// loop still runs.
func Run(t *testing.T, env ...string) *app.Application {
	t.Helper()
	swaytest.Start(t, env...)
	return Loop(t)
}

// Loop runs an application on the compositor WAYLAND_DISPLAY names for
// the rest of the test, as Run does on its sway.
func Loop(t *testing.T) *app.Application {
	t.Helper()
	sess, err := app.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	a := app.NewApplication(sess)
	a.Hold()
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() {
		a.Invoke(a.Quit)
		if err := <-done; err != nil && !errors.Is(err, app.ErrClosed) {
			t.Errorf("the loop ended with %v", err)
		}
		widget.WaitAssetLoads()
	})
	return a
}

// Until polls cond on a's loop until it holds, failing the test with
// what after ten seconds.
func Until(t *testing.T, a *app.Application, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if On(a, cond) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
	}
}

// On runs fn on a's loop and returns its result.
func On[T any](a *app.Application, fn func() T) T {
	got := make(chan T, 1)
	a.Invoke(func() { got <- fn() })
	return <-got
}

// Dispatched waits until a's loop has handled every event the
// compositor sent it before the call: the compositor answers a
// wl_display.sync after all it queued for the client ahead of it, and
// the loop dispatches in order. Input another client of the compositor
// injected and round-tripped has reached a's handlers when it returns.
// It fails the test after ten seconds.
func Dispatched(t *testing.T, a *app.Application) {
	t.Helper()
	awaitCallback(t, a, "a display sync", func() (*wl.Callback, error) { return a.Session().Display.Sync() })
}

// Drawn waits until the compositor has taken a frame of surface drawn
// after the call: invalidate owes the surface a draw, and that draw
// drains every invalidation made before it, so no frame is owed for
// them when Drawn returns. It fails the test after ten seconds.
func Drawn(t *testing.T, a *app.Application, surface *wl.Surface, invalidate func()) {
	t.Helper()
	awaitCallback(t, a, "a frame callback", func() (*wl.Callback, error) {
		cb, err := surface.Frame()
		if err == nil {
			invalidate()
		}
		return cb, err
	})
}

// awaitCallback makes request on a's loop and waits for its callback,
// what naming it in the failure after ten seconds.
func awaitCallback(t *testing.T, a *app.Application, what string, request func() (*wl.Callback, error)) {
	t.Helper()
	done := make(chan struct{})
	if err := On(a, func() error {
		cb, err := request()
		if err != nil {
			return err
		}
		cb.AddDoneHandler(callbackDone{cb: cb, done: done})
		return nil
	}); err != nil {
		t.Fatalf("request %s: %v", what, err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the loop dispatched no answer to %s within 10s: it was stuck or the compositor never answered", what)
	}
}

// callbackDone closes done when the callback cb fires.
type callbackDone struct {
	cb   *wl.Callback
	done chan struct{}
}

func (c callbackDone) HandleCallbackDone(wl.CallbackDoneEvent) {
	c.cb.Unregister()
	close(c.done)
}

// Never waits out settle, failing the test with what if answered has
// an answer by then.
func Never[T any](t *testing.T, what string, answered <-chan T) {
	t.Helper()
	select {
	case v := <-answered:
		t.Fatalf("%s: answered %+v", what, v)
	case <-time.After(settle):
	}
}

// Answer waits for answered's answer, failing the test with what after
// ten seconds.
func Answer[T any](t *testing.T, what string, answered <-chan T) T {
	t.Helper()
	select {
	case v := <-answered:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("never answered: %s", what)
		var none T
		return none
	}
}

// ShmResident is how many bytes of lys-ui's shared-memory buffer pool
// are resident in this process: the pixels of every surface drawn on a
// compositor without the GPU path, and of none it has let go.
func ShmResident(t *testing.T) int {
	t.Helper()
	f, err := os.Open("/proc/self/smaps")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	resident, inPool := 0, false
	for sc := bufio.NewScanner(f); sc.Scan(); {
		fields := strings.Fields(sc.Text())
		switch {
		case len(fields) >= 5 && strings.Contains(fields[0], "-"):
			inPool = len(fields) >= 6 && strings.HasPrefix(fields[5], "/memfd:lys-ui-shm")
		case inPool && len(fields) == 3 && fields[0] == "Rss:":
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatal(err)
			}
			resident += kb << 10
		}
	}
	return resident
}
