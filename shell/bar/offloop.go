package bar

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const offLoopTimeout = 5 * time.Second

func renderOnce[T any](read func(context.Context) (T, error), apply func(T)) error {
	ctx, cancel := context.WithTimeout(context.Background(), offLoopTimeout)
	defer cancel()
	v, err := read(ctx)
	if err != nil {
		return err
	}
	apply(v)
	return nil
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
	go r.run()
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
	go r.run()
}

func followReads[T any](ctx ModuleContext, module string, subscribe func(context.Context) (<-chan struct{}, func(), error), read func(context.Context) (T, error), apply func(T)) (*offLoopRead[T], error) {
	if err := renderOnce(read, apply); err != nil {
		return nil, fmt.Errorf("%s: %w", module, err)
	}
	ticks, stop, err := subscribe(ctx.Life())
	if err != nil {
		return nil, fmt.Errorf("%s: subscribe: %w", module, err)
	}
	reads := newOffLoopRead(ctx, module, read, apply)
	reads.on(ticks, stop)
	return reads, nil
}

func (r *offLoopRead[T]) on(ch <-chan struct{}, stop func()) {
	drain(r.ctx, ch, stop, func(struct{}) { r.request() })
}
