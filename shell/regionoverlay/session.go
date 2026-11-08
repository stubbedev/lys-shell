package regionoverlay

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
)

// ErrBusy answers a request made while a capture overlay session is open,
// or an overlay request made during an instant capture. The request opened
// nothing and captured nothing, and what was running goes on untouched.
var ErrBusy = errors.New("another capture is in progress")

// Gate admits one capture overlay session at a time across every overlay
// sharing it. An instant capture is admitted only while no session is open,
// so an overlay never lands in a shot.
type Gate struct {
	mu     sync.RWMutex
	state  sync.Mutex
	opened uint64
	ended  chan struct{}
}

func (g *Gate) begin() {
	g.state.Lock()
	defer g.state.Unlock()
	g.opened++
	g.ended = make(chan struct{})
}

func (g *Gate) end() {
	g.state.Lock()
	defer g.state.Unlock()
	close(g.ended)
	g.ended = nil
}

// Open reports whether a session is open. It takes no part in admission, so
// asking never refuses a request, as Instant can.
func (g *Gate) Open() bool {
	g.state.Lock()
	defer g.state.Unlock()
	return g.ended != nil
}

func (g *Gate) awaitNoSession(ctx context.Context) (uint64, error) {
	for {
		g.state.Lock()
		opened, ended := g.opened, g.ended
		g.state.Unlock()
		if ended == nil {
			return opened, nil
		}
		select {
		case <-ended:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (g *Gate) admittedSince(opened uint64) bool {
	g.state.Lock()
	defer g.state.Unlock()
	return g.opened != opened
}

// BackgroundCapture runs capture so that what it returns shows no overlay, for
// a capture nobody asked for by hand. It neither refuses nor is refused: it
// waits out an open session, and a session admitted while it runs takes
// precedence, discarding its result to capture again once that session ended.
// So a capture runs again only for a session someone opened. It ends with
// ctx's error while waiting.
func BackgroundCapture[T any](ctx context.Context, g *Gate, capture func() (T, error)) (T, error) {
	for {
		opened, err := g.awaitNoSession(ctx)
		if err != nil {
			var none T
			return none, err
		}
		v, err := capture()
		if !g.admittedSince(opened) {
			return v, err
		}
	}
}

// Instant admits a capture that opens no overlay, ErrBusy while a session
// is open; the returned func ends it. Instant captures do not exclude each
// other.
func (g *Gate) Instant() (func(), error) {
	if !g.mu.TryRLock() {
		return nil, ErrBusy
	}
	return g.mu.RUnlock, nil
}

// Frames captures the frozen frames of one overlay session, keyed by
// connector. A session runs it once it holds the gate, so it takes no
// capture for a refused request.
type Frames func() (map[string]render.StillImage, error)

// Loop is the application loop an overlay session runs on: Invoke runs fn
// on it, and Settle, called on it, calls done on it once the compositor has
// handled every request the loop made before, so surfaces closed by then
// are off the screen.
type Loop interface {
	Invoke(fn func())
	Settle(done func())
}

// AppLoop is a's loop as a session's Loop.
func AppLoop(a *app.Application) Loop { return appLoop{a: a} }

type appLoop struct{ a *app.Application }

func (l appLoop) Invoke(fn func()) { l.a.Invoke(fn) }

func (l appLoop) Settle(done func()) {
	cb, err := l.a.Session().Display.Sync()
	if err != nil {
		log.Printf("region overlay: confirm the overlay left the screen: %v: the connection and its surfaces are gone", err)
		done()
		return
	}
	cb.AddDoneHandler(settled{cb: cb, done: done})
}

type settled struct {
	cb   *wl.Callback
	done func()
}

func (s settled) HandleCallbackDone(wl.CallbackDoneEvent) {
	s.cb.Unregister()
	s.done()
}

// settleTimeout bounds how long an ended session waits for the compositor
// to confirm its surfaces are gone before it frees the gate anyway.
const settleTimeout = 5 * time.Second

// OpenSession is the one way into a capture overlay: it holds g for the whole
// session, refusing with ErrBusy before anything else happens. Admitted, it
// takes the frames (nil Frames for none), runs show on loop, and blocks for
// the answer show's overlay sends on the channel it was given. It frees g
// only once loop settled after that answer, so a capture admitted next never
// shows the overlay's surfaces still on the screen. ctx ending takes down this session alone, through cancel on the
// loop: the overlay answers as the user's cancel does, and cancel ignores a
// channel that is no longer the open one. A ctx already ended once the frames
// are in answers the zero T without showing anything.
func OpenSession[T any](ctx context.Context, g *Gate, loop Loop, frames Frames, show func(map[string]render.StillImage, chan T), cancel func(chan T)) (T, error) {
	var none T
	if !g.mu.TryLock() {
		return none, ErrBusy
	}
	defer g.mu.Unlock()
	g.begin()
	defer g.end()
	var captured map[string]render.StillImage
	if frames != nil {
		var err error
		if captured, err = frames(); err != nil {
			return none, err
		}
	}
	if ctx.Err() != nil {
		return none, nil
	}
	reply := make(chan T, 1)
	loop.Invoke(func() { show(captured, reply) })
	stop := context.AfterFunc(ctx, func() { loop.Invoke(func() { cancel(reply) }) })
	answer := <-reply
	stop()
	gone := make(chan struct{})
	loop.Invoke(func() { loop.Settle(func() { close(gone) }) })
	select {
	case <-gone:
	case <-time.After(settleTimeout):
		log.Printf("region overlay: the compositor did not confirm within %v that the overlay left the screen", settleTimeout)
	}
	return answer, nil
}

// OpenSurface maps one capture overlay's surface over out: above everything,
// covering the output and holding the keyboard. It plays no exit motion
// whatever the application's surface motion, so Close ends its painting: the
// requester frees the frozen frames the surface paints once it has its answer.
func OpenSurface(a *app.Application, out *app.Output, namespace string, root widget.Widget, onKey func(*widget.Router, uint32, app.Mods)) (*app.LayerWindow, error) {
	return a.NewLayer(app.LayerConfig{
		Output:        out,
		Layer:         app.LayerOverlay,
		Anchor:        app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone: -1,
		Keyboard:      app.KeyboardExclusive,
		Namespace:     namespace,
		Root:          root,
		OwnMotion:     true,
		OnKey:         onKey,
	})
}
