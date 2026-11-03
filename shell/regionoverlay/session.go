package regionoverlay

import (
	"context"
	"errors"
	"sync"

	"github.com/stubbedev/lys-lib/render"
)

// ErrBusy answers a request made while a capture overlay session is open,
// or an overlay request made during an instant capture. The request opened
// nothing and captured nothing, and what was running goes on untouched.
var ErrBusy = errors.New("another capture is in progress")

// Gate admits one capture overlay session at a time across every overlay
// sharing it. An instant capture is admitted only while no session is open,
// so an overlay never lands in a shot.
type Gate struct{ mu sync.RWMutex }

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

// OpenSession is the one way into a capture overlay: it holds g for the whole
// session, refusing with ErrBusy before anything else happens. Admitted, it
// takes the frames (nil Frames for none), runs show on the loop through
// invoke, and blocks for the answer show's overlay sends on the channel it
// was given. ctx ending takes down this session alone, through cancel on the
// loop: the overlay answers as the user's cancel does, and cancel ignores a
// channel that is no longer the open one. A ctx already ended once the frames
// are in answers the zero T without showing anything.
func OpenSession[T any](ctx context.Context, g *Gate, invoke func(func()), frames Frames, show func(map[string]render.StillImage, chan T), cancel func(chan T)) (T, error) {
	var none T
	if !g.mu.TryLock() {
		return none, ErrBusy
	}
	defer g.mu.Unlock()
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
	invoke(func() { show(captured, reply) })
	stop := context.AfterFunc(ctx, func() { invoke(func() { cancel(reply) }) })
	defer stop()
	return <-reply, nil
}
