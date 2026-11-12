// Package recordertest is the scripted recorder.Engine every recorder
// test drives: sessions and recordings that record their calls, an
// optional gate that holds OpenSession (the output picker), and a hook
// that ends a recording as a failed capture would.
package recordertest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/config"

	"github.com/stubbedev/lys-shell/service/recorder"
)

// Engine is a fake recorder.Engine.
type Engine struct {
	// OpenErr and StartErr fail the respective step; StopErr is what
	// every recording's Stop returns, CloseErr what every session's
	// Close returns.
	OpenErr, StartErr, StopErr, CloseErr error
	// Gate, when set, holds OpenSession until it closes or ctx ends.
	Gate chan struct{}
	// WriteOutput makes a stopped recording leave a non-empty file at
	// its output path, as a finalized muxer does.
	WriteOutput bool

	mu         sync.Mutex
	sessions   []*Session
	recordings []*Recording
	replays    []*Replay
	outputs    []string
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

// OpenOutput implements recorder.Engine.
func (e *Engine) OpenOutput(output string, _ bool) (recorder.Session, error) {
	e.mu.Lock()
	e.outputs = append(e.outputs, output)
	e.mu.Unlock()
	if e.OpenErr != nil {
		return nil, e.OpenErr
	}
	s := &Session{e: e}
	e.mu.Lock()
	e.sessions = append(e.sessions, s)
	e.mu.Unlock()
	return s, nil
}

// Outputs are the outputs OpenOutput was asked for.
func (e *Engine) Outputs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.outputs...)
}

// Replays are the started replays.
func (e *Engine) Replays() []*Replay {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Replay(nil), e.replays...)
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

// StartReplay implements recorder.Session.
func (s *Session) StartReplay(opts recorder.Options, span time.Duration, term func(string), onKey func()) (recorder.Replay, error) {
	if s.e.StartErr != nil {
		return nil, s.e.StartErr
	}
	r := &Replay{Opts: opts, Span: span, term: term, onKey: onKey}
	s.e.mu.Lock()
	s.e.replays = append(s.e.replays, r)
	s.e.mu.Unlock()
	return r, nil
}

// Close implements recorder.Session.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	return s.e.CloseErr
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
		if err := os.WriteFile(r.Opts.OutputPath, []byte("video"), 0o600); err != nil {
			return fmt.Errorf("recordertest: write the output %s: %w", r.Opts.OutputPath, err)
		}
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

// Replay is a fake recorder.Replay: Save writes the path and format it
// was asked for into the file, as a muxed ring would write footage.
type Replay struct {
	// Opts and Span are what it started with.
	Opts recorder.Options
	Span time.Duration

	mu      sync.Mutex
	term    func(string)
	onKey   func()
	bytes   uint64
	saves   []string
	stops   int
	saveErr error
}

// Save implements recorder.Replay.
func (r *Replay) Save(path string, format config.RecorderFormat) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saves = append(r.saves, path)
	return os.WriteFile(path, []byte("replay "+string(format)), 0o600)
}

// Bytes implements recorder.Replay.
func (r *Replay) Bytes() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bytes
}

// Stop implements recorder.Replay.
func (r *Replay) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
	return nil
}

// Keyframe makes the ring hold n bytes and reports a keyframe entering
// it.
func (r *Replay) Keyframe(n uint64) {
	r.mu.Lock()
	r.bytes = n
	r.mu.Unlock()
	r.onKey()
}

// FailSaves makes every following Save fail with err.
func (r *Replay) FailSaves(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveErr = err
}

// Saves are the paths saved to.
func (r *Replay) Saves() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.saves...)
}

// Stops counts Stop calls.
func (r *Replay) Stops() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

// Fail ends the replay on its own, as a dying capture reports.
func (r *Replay) Fail(reason string) { r.term(reason) }
