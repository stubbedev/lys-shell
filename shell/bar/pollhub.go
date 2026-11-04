package bar

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// pollHub is one sampler per config key across the bars, paused while the
// session is idle or locked. A nil hub is a hub of its own for each join.
type pollHub struct {
	mu        sync.Mutex
	samplers  map[string]pollMember
	members   map[pollMember]struct{}
	analyzers map[string]*cavaAnalyzer
	idle      atomic.Bool
	locked    atomic.Bool
}

type pollMember interface{ refreshStale() }

// pollPause says which signals hold a sampler back.
type pollPause int

const (
	// pauseIdleOrLocked is for what nobody misses on an idle screen.
	pauseIdleOrLocked pollPause = iota
	// pauseLocked is for what an idle but visible screen must keep showing:
	// the time.
	pauseLocked
)

func newPollHub() *pollHub {
	return &pollHub{samplers: map[string]pollMember{}, members: map[pollMember]struct{}{}, analyzers: map[string]*cavaAnalyzer{}}
}

// cavaAnalyzer is key's running analyzer, built by the first bar asking and
// forgotten once its last bar left.
func (h *pollHub) cavaAnalyzer(key string, build func() (*cavaAnalyzer, error)) (*cavaAnalyzer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a, ok := h.analyzers[key]; ok {
		return a, nil
	}
	a, err := build()
	if err != nil {
		return nil, err
	}
	a.released = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.analyzers, key)
	}
	h.analyzers[key] = a
	return a, nil
}

func (h *pollHub) paused(p pollPause) bool {
	if h.locked.Load() {
		return true
	}
	return p == pauseIdleOrLocked && h.idle.Load()
}

func (h *pollHub) setIdle(on bool) {
	h.idle.Store(on)
	h.resumed()
}

func (h *pollHub) setLocked(on bool) {
	h.locked.Store(on)
	h.resumed()
}

func (h *pollHub) resumed() {
	h.mu.Lock()
	members := make([]pollMember, 0, len(h.members))
	for m := range h.members {
		members = append(members, m)
	}
	h.mu.Unlock()
	for _, m := range members {
		go m.refreshStale()
	}
}

// pollSource starts what fires a sampler until ctx ends.
type pollSource func(ctx context.Context, fire func()) error

// pollGrid fires at once and then on the shared grid of every.
func pollGrid(every time.Duration) pollSource {
	return func(ctx context.Context, fire func()) error {
		go func() {
			fire()
			timer := time.NewTimer(untilPollGrid(time.Now(), every))
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				fire()
				timer.Reset(untilPollGrid(time.Now(), every))
			}
		}()
		return nil
	}
}

// sampler is one source of readings: sample runs on the source's goroutine
// and each change reaches every bar that joined.
type sampler[T any] struct {
	hub    *pollHub
	key    string
	pause  pollPause
	sample func() (T, bool)
	same   func(a, b T) bool
	end    func()

	cancel context.CancelFunc

	run   sync.Mutex
	stale bool

	mu     sync.Mutex
	subs   map[*func(T)]struct{}
	last   T
	has    bool
	ending bool
}

// samplerSpec is what joining needs: one bar's copy of the sampler's work,
// the first joiner's being the one that runs.
type samplerSpec[T any] struct {
	key    string
	pause  pollPause
	source pollSource
	sample func() (T, bool)
	same   func(a, b T) bool
	// closeRead lets go of what sample holds, once the sampler ends or at once
	// for a bar joining one already running.
	closeRead func()
}

// joinSampler has deliver called off the loop with each changed reading until
// ctx ends, the last one at once when the sampler already has it. The last bar
// leaving ends the sampler.
func joinSampler[T any](ctx context.Context, h *pollHub, spec samplerSpec[T], deliver func(T)) error {
	if h == nil {
		h = newPollHub()
	}
	h.mu.Lock()
	s, running := h.samplers[spec.key].(*sampler[T])
	if !running {
		s = &sampler[T]{hub: h, key: spec.key, pause: spec.pause, sample: spec.sample, same: spec.same, end: spec.closeRead, subs: map[*func(T)]struct{}{}}
		h.samplers[spec.key] = s
		h.members[s] = struct{}{}
	}
	s.mu.Lock()
	s.subs[&deliver] = struct{}{}
	last, has := s.last, s.has
	s.mu.Unlock()
	h.mu.Unlock()
	context.AfterFunc(ctx, func() { s.leave(&deliver) })
	if running {
		if spec.closeRead != nil {
			spec.closeRead()
		}
		if has {
			deliver(last)
		}
		return nil
	}
	life, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	if err := spec.source(life, s.fire); err != nil {
		cancel()
		s.leave(&deliver)
		return err
	}
	return nil
}

func (s *sampler[T]) leave(deliver *func(T)) {
	h := s.hub
	h.mu.Lock()
	s.mu.Lock()
	delete(s.subs, deliver)
	done := len(s.subs) == 0
	s.mu.Unlock()
	if done {
		delete(h.samplers, s.key)
		delete(h.members, s)
	}
	h.mu.Unlock()
	if !done {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	go func() {
		s.run.Lock()
		defer s.run.Unlock()
		s.mu.Lock()
		s.ending = true
		s.mu.Unlock()
		if s.end != nil {
			s.end()
		}
	}()
}

func (s *sampler[T]) fire() {
	s.run.Lock()
	defer s.run.Unlock()
	if s.hub.paused(s.pause) {
		s.stale = true
		return
	}
	s.read()
}

func (s *sampler[T]) refreshStale() {
	s.run.Lock()
	defer s.run.Unlock()
	if !s.stale || s.hub.paused(s.pause) {
		return
	}
	s.stale = false
	s.read()
}

func (s *sampler[T]) read() {
	s.mu.Lock()
	ended := s.ending
	s.mu.Unlock()
	if ended {
		return
	}
	v, ok := s.sample()
	if !ok {
		return
	}
	s.mu.Lock()
	if s.has && s.same(s.last, v) {
		s.mu.Unlock()
		return
	}
	s.last, s.has = v, true
	subs := make([]*func(T), 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		(*sub)(v)
	}
}
