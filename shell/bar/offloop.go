package bar

import (
	"context"
	"log"
	"sync"
	"time"
)

const offLoopTimeout = 5 * time.Second

type firstRead struct {
	chrome interface{ SetVisible(on bool) }
	read   bool
}

func (f *firstRead) setChrome(c moduleChrome) { f.hideUntilRead(c) }

func (f *firstRead) hideUntilRead(c interface{ SetVisible(on bool) }) {
	f.chrome = c
	c.SetVisible(f.read)
}

func (f *firstRead) reveal() {
	if f.read {
		return
	}
	f.read = true
	if f.chrome != nil {
		f.chrome.SetVisible(true)
	}
}

func mountOffLoop(ctx ModuleContext, start func()) {
	if ctx.App == nil {
		start()
		return
	}
	ctx.Go(start)
}

func returnToLoop(ctx ModuleContext, fn func()) {
	if ctx.App == nil {
		fn()
		return
	}
	ctx.Invoke(fn)
}

type offLoopRead[T any] struct {
	ctx    ModuleContext
	module string
	read   func(context.Context) (T, error)
	apply  func(T)

	mu    sync.Mutex
	busy  bool
	again bool
}

func newOffLoopRead[T any](ctx ModuleContext, module string, read func(context.Context) (T, error), apply func(T)) *offLoopRead[T] {
	return &offLoopRead[T]{ctx: ctx, module: module, read: read, apply: apply}
}

func (r *offLoopRead[T]) request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy {
		r.again = true
		return
	}
	r.busy = true
	r.ctx.Go(r.run)
}

func (r *offLoopRead[T]) first() {
	if r.ctx.App != nil {
		r.request()
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx.Life(), offLoopTimeout)
	defer cancel()
	v, err := r.read(ctx)
	if err != nil {
		log.Printf("%s: first read: %v", r.module, err)
		return
	}
	r.apply(v)
}

func (r *offLoopRead[T]) run() {
	life := r.ctx.Life()
	ctx, cancel := context.WithTimeout(life, offLoopTimeout)
	v, err := r.read(ctx)
	cancel()
	if life.Err() != nil {
		return
	}
	r.ctx.Invoke(func() {
		if err != nil {
			log.Printf("%s: refresh: %v", r.module, err)
		} else {
			r.apply(v)
		}
		r.settle()
	})
}

func (r *offLoopRead[T]) settle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.again {
		r.busy = false
		return
	}
	r.again = false
	r.ctx.Go(r.run)
}

// sourceSubscription follows a source until ctx ends or stop, which reports
// what ending it met.
type sourceSubscription func(ctx context.Context) (ticks <-chan struct{}, stop func() error, err error)

// infallibleStop is subscribe as a sourceSubscription, for a source whose
// stop cannot fail.
func infallibleStop(subscribe func(context.Context) (<-chan struct{}, func(), error)) sourceSubscription {
	return func(ctx context.Context) (<-chan struct{}, func() error, error) {
		ticks, stop, err := subscribe(ctx)
		if err != nil {
			return nil, nil, err
		}
		return ticks, func() error {
			stop()
			return nil
		}, nil
	}
}

// stopFollowing runs stop and logs what it met under what.
func stopFollowing(what string, stop func() error) {
	if err := stop(); err != nil {
		log.Printf("%s: stop following: %v", what, err)
	}
}

func followReads[T any](ctx ModuleContext, module string, gate *firstRead, subscribe sourceSubscription, read func(context.Context) (T, error), apply func(T)) *offLoopRead[T] {
	reads := newOffLoopRead(ctx, module, read, func(v T) {
		apply(v)
		gate.reveal()
	})
	mountOffLoop(ctx, func() {
		ticks, stop, err := subscribe(ctx.Life())
		reads.first()
		if err != nil {
			log.Printf("%s: subscribe: %v", module, err)
			return
		}
		reads.on(ticks, func() { stopFollowing(module, stop) })
	})
	return reads
}

func (r *offLoopRead[T]) on(ch <-chan struct{}, stop func()) {
	drain(r.ctx, ch, stop, func(struct{}) { r.request() })
}
