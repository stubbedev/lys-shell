// Package recordertest is the scripted recorder.Engine every recorder
// test drives: sessions and recordings that record their calls, an
// optional gate that holds OpenSession (the output picker), and a hook
// that ends a recording as a failed capture would.
package recordertest

import (
	"context"
	"os"
	"sync"

	"github.com/stubbedev/lys-shell/service/recorder"
)

// Engine is a fake recorder.Engine.
type Engine struct {
	// OpenErr and StartErr fail the respective step; StopErr is what
	// every recording's Stop returns.
	OpenErr, StartErr, StopErr error
	// Gate, when set, holds OpenSession until it closes or ctx ends.
	Gate chan struct{}
	// WriteOutput makes a stopped recording leave a non-empty file at
	// its output path, as a finalized muxer does.
	WriteOutput bool

	mu         sync.Mutex
	sessions   []*Session
	recordings []*Recording
}

// OpenSession implements recorder.Engine.
func (e *Engine) OpenSession(ctx context.Context, _ bool) (recorder.Session, error) {
	if e.Gate != nil {
		select {
		case <-e.Gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e.OpenErr != nil {
		return nil, e.OpenErr
	}
	s := &Session{e: e}
	e.mu.Lock()
	e.sessions = append(e.sessions, s)
	e.mu.Unlock()
	return s, nil
}

// Sessions are the opened sessions.
func (e *Engine) Sessions() []*Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Session(nil), e.sessions...)
}

// Recordings are the started recordings.
func (e *Engine) Recordings() []*Recording {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Recording(nil), e.recordings...)
}

// Session is a fake recorder.Session.
type Session struct {
	e      *Engine
	mu     sync.Mutex
	closed int
}

// Start implements recorder.Session.
func (s *Session) Start(opts recorder.Options, term func(string)) (recorder.Recording, error) {
	if s.e.StartErr != nil {
		return nil, s.e.StartErr
	}
	r := &Recording{Opts: opts, term: term, write: s.e.WriteOutput, stopErr: s.e.StopErr}
	s.e.mu.Lock()
	s.e.recordings = append(s.e.recordings, r)
	s.e.mu.Unlock()
	return r, nil
}

// Close implements recorder.Session.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
}

// Closed counts Close calls.
func (s *Session) Closed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Recording is a fake recorder.Recording.
type Recording struct {
	// Opts are the options it started with.
	Opts recorder.Options

	mu      sync.Mutex
	paused  []bool
	stops   int
	hold    chan struct{}
	term    func(string)
	write   bool
	stopErr error
}

// SetPaused implements recorder.Recording.
func (r *Recording) SetPaused(paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = append(r.paused, paused)
}

// Stop implements recorder.Recording.
func (r *Recording) Stop() error {
	r.mu.Lock()
	r.stops++
	write := r.write && r.stops == 1
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if write {
		_ = os.WriteFile(r.Opts.OutputPath, []byte("video"), 0o600)
	}
	return r.stopErr
}

// HoldStop makes Stop block until release runs, so a test can see
// what the service shows while a recording finalizes.
func (r *Recording) HoldStop() (release func()) {
	gate := make(chan struct{})
	r.mu.Lock()
	r.hold = gate
	r.mu.Unlock()
	return sync.OnceFunc(func() { close(gate) })
}

// Paused lists the SetPaused arguments.
func (r *Recording) Paused() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.paused...)
}

// Stops counts Stop calls.
func (r *Recording) Stops() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

// Fail ends the recording on its own, as a dying capture reports.
func (r *Recording) Fail(reason string) { r.term(reason) }
