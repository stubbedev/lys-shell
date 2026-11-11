package launcher

import (
	"context"
	"slices"
	"sync"

	"github.com/stubbedev/lys-ui/widget"
)

// iconRequest is one icon to decode.
type iconRequest struct {
	name       string
	size       int
	num, denom int
}

// iconLoader decodes a panel's icons on one goroutine, off the UI loop:
// newest first, since typing makes the older ones moot, and only while
// the panel is open. The goroutine, which spawn starts, exists only
// while there is work.
type iconLoader struct {
	ctx    context.Context
	spawn  func(func())
	invoke func(func())
	ready  func(iconRequest)
	decode func(iconRequest)

	mu      sync.Mutex
	queue   []iconRequest
	running bool
}

func newIconLoader(ctx context.Context, spawn, invoke func(func()), ready func(iconRequest)) *iconLoader {
	return &iconLoader{ctx: ctx, spawn: spawn, invoke: invoke, ready: ready, decode: func(r iconRequest) {
		_ = widget.PreloadThemeIcon(r.name, r.size, r.num, r.denom)
	}}
}

func (l *iconLoader) request(r iconRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if slices.Contains(l.queue, r) {
		return
	}
	l.queue = append(l.queue, r)
	if !l.running {
		l.running = true
		l.spawn(l.run)
	}
}

func (l *iconLoader) run() {
	for {
		l.mu.Lock()
		n := len(l.queue)
		if n == 0 || l.ctx.Err() != nil {
			l.queue = l.queue[:0]
			l.running = false
			l.mu.Unlock()
			return
		}
		r := l.queue[n-1]
		l.queue = l.queue[:n-1]
		decode := l.decode
		l.mu.Unlock()
		decode(r)
		l.invoke(func() {
			if l.ctx.Err() == nil {
				l.ready(r)
			}
		})
	}
}
