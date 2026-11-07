package idle

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/internal/feed"
)

// Watcher tells when the session has been idle for a while: idle runs once
// it has been idle for after, resume when the user is back. Both run on
// the watcher's own goroutine, and idle inhibitors hold the idle off.
type Watcher interface {
	Watch(after time.Duration, idle, resume func()) (stop func(), err error)
}

// Actions carry the stages out. Dim and ScreenOff return what undoes them
// at resume.
type Actions interface {
	Dim(DimPercent) (restore func() error, err error)
	Lock() error
	ScreenOff() (restore func() error, err error)
	Suspend() error
}

// Status is what the service has armed and which stages have run since the
// user was last active.
type Status struct {
	Enabled bool
	Source  PowerSource
	Arms    []Arm
	Reached []Stage
}

// Next is the first armed stage that has not run yet; false when every
// armed one has, or none is.
func (s Status) Next() (Arm, bool) {
	for _, a := range s.Arms {
		if !slices.Contains(s.Reached, a.Stage) {
			return a, true
		}
	}
	return Arm{}, false
}

// Service arms one idle watch per stage of the policy on the current power
// source, and re-arms them when either changes.
type Service struct {
	watcher Watcher
	actions Actions

	mu       sync.Mutex
	policy   Policy
	enabled  bool
	source   PowerSource
	stops    []func()
	restores [stageCount]func() error
	reached  []Stage
	gen      uint64
	changes  feed.Tick
}

// New is a service that has armed nothing until Configure.
func New(w Watcher, a Actions) *Service {
	return &Service{watcher: w, actions: a}
}

// Configure arms the policy's stages on source; disabled arms none. Every
// stage that has run is undone first, so a new policy starts from an
// active session.
func (s *Service) Configure(p Policy, enabled bool, source PowerSource) error {
	s.mu.Lock()
	s.policy, s.enabled, s.source = p, enabled, source
	err := s.rearmLocked()
	s.mu.Unlock()
	feed.Notify(&s.changes)
	return err
}

// SetPowerSource re-arms the stages with source's timeouts.
func (s *Service) SetPowerSource(source PowerSource) error {
	s.mu.Lock()
	if s.source == source {
		s.mu.Unlock()
		return nil
	}
	s.source = source
	err := s.rearmLocked()
	s.mu.Unlock()
	feed.Notify(&s.changes)
	return err
}

// Policy is the policy the service runs.
func (s *Service) Policy() Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy
}

// Status reads what is armed and reached.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Enabled: s.enabled, Source: s.source, Reached: slices.Clone(s.reached)}
	if s.enabled {
		st.Arms = s.policy.Arms(s.source)
	}
	return st
}

// Changes ticks after the service re-arms or a stage runs or is undone.
func (s *Service) Changes() (<-chan struct{}, func()) { return s.changes.Subscribe() }

// Close stops every watch and undoes what the stages did.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	return s.disarmLocked()
}

func (s *Service) disarmLocked() error {
	for _, stop := range s.stops {
		stop()
	}
	s.stops = s.stops[:0]
	var err error
	for _, stage := range Stages {
		err = errors.Join(err, s.restoreLocked(stage))
	}
	s.reached = s.reached[:0]
	return err
}

func (s *Service) rearmLocked() error {
	s.gen++
	err := s.disarmLocked()
	if !s.enabled {
		return err
	}
	gen := s.gen
	for _, arm := range s.policy.Arms(s.source) {
		stage := arm.Stage
		stop, werr := s.watcher.Watch(arm.After, func() { s.idled(gen, stage) }, func() { s.resumed(gen, stage) })
		if werr != nil {
			return errors.Join(err, fmt.Errorf("idle: watch %s after %s: %w", stage, arm.After, werr))
		}
		s.stops = append(s.stops, stop)
	}
	return err
}

func (s *Service) idled(gen uint64, stage Stage) {
	s.mu.Lock()
	if gen != s.gen || slices.Contains(s.reached, stage) {
		s.mu.Unlock()
		return
	}
	s.reached = append(s.reached, stage)
	err := s.runLocked(stage)
	s.mu.Unlock()
	if err != nil {
		log.Printf("idle: %s: %v", stage, err)
	}
	feed.Notify(&s.changes)
}

func (s *Service) runLocked(stage Stage) error {
	var (
		restore func() error
		err     error
	)
	switch stage {
	case Dim:
		restore, err = s.actions.Dim(s.policy.DimPercent)
	case Lock:
		err = s.actions.Lock()
	case ScreenOff:
		restore, err = s.actions.ScreenOff()
	case Suspend:
		err = s.actions.Suspend()
	}
	s.restores[stage] = restore
	return err
}

func (s *Service) resumed(gen uint64, stage Stage) {
	s.mu.Lock()
	if gen != s.gen {
		s.mu.Unlock()
		return
	}
	s.reached = slices.DeleteFunc(s.reached, func(r Stage) bool { return r == stage })
	err := s.restoreLocked(stage)
	s.mu.Unlock()
	if err != nil {
		log.Printf("idle: undo %s: %v", stage, err)
	}
	feed.Notify(&s.changes)
}

func (s *Service) restoreLocked(stage Stage) error {
	restore := s.restores[stage]
	s.restores[stage] = nil
	if restore == nil {
		return nil
	}
	if err := restore(); err != nil {
		return fmt.Errorf("idle: undo %s: %w", stage, err)
	}
	return nil
}
