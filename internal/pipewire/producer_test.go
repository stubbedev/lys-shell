package pipewire

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/pipewire/pwtest"
)

// settlesTo waits until the process runs want goroutines, failing with
// the stacks of the ones left over.
func settlesTo(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > want {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("%d goroutines, %d before the stream:\n%s", runtime.NumGoroutine(), want, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// streaming starts a producer into a linked reference consumer and
// waits for its first frame. It returns the goroutine count from before
// the producer started.
func streaming(t *testing.T) (*Producer, int) {
	t.Helper()
	l, err := load()
	if err != nil {
		t.Fatal(err)
	}
	loop, err := startLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loop.stop)
	before := runtime.NumGoroutine()
	p, err := Start(Config{Width: 64, Height: 32, Stride: 256, Format: VideoBGRx, FPS: 30, Source: solid{0x7f}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	c := newConsumer(t, loop, 64, 32)
	t.Cleanup(func() {
		loop.do(func() {
			l.streamDisconnect(c.stream)
			l.streamDestroy(c.stream)
		})
	})
	link(t)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-c.frames:
			if f.size > 0 {
				return p, before
			}
		case <-deadline:
			t.Fatal("no frame arrived")
		}
	}
}

// PipeWire going away mid-stream ends the stream, saying so, and Close
// then leaves no goroutine behind.
func TestTheServerGoingAwayEndsTheStream(t *testing.T) {
	daemon := pwtest.Start(t)
	p, before := streaming(t)
	select {
	case <-p.Done():
		t.Fatalf("the stream ended while PipeWire ran: %v", p.Err())
	case <-time.After(100 * time.Millisecond):
	}
	daemon.Kill()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived PipeWire")
	}
	if !errors.Is(p.Err(), errServerClosed) {
		t.Errorf("Err = %v, want the server hanging up", p.Err())
	}
	p.Close()
	settlesTo(t, before)
}

// A stream after PipeWire restarted, in the same process, runs as the
// first did: nothing of the first daemon's connection outlives it.
func TestAStreamRunsAgainAfterPipeWireRestarts(t *testing.T) {
	first := pwtest.Start(t)
	p, _ := streaming(t)
	first.Kill()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived PipeWire")
	}
	p.Close()

	pwtest.Start(t)
	again, _ := streaming(t)
	if again.NodeID() == 0 {
		t.Error("the second stream has no node")
	}
}

// Closing a live stream ends it without an error and leaves no
// goroutine behind.
func TestClosingAStreamLeavesNothingRunning(t *testing.T) {
	pwtest.Start(t)
	p, before := streaming(t)
	p.Close()
	select {
	case <-p.Done():
	default:
		t.Fatal("a closed stream is not done")
	}
	if p.Err() != nil {
		t.Errorf("a closed stream reports %v", p.Err())
	}
	settlesTo(t, before)
	p.Close()
}
