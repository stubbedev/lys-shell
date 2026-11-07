package recorder

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/stubbedev/lys-shell/i18n"
)

// ReplayStatus is the instant replay's lifecycle.
type ReplayStatus int

// Replay statuses.
const (
	ReplayOff ReplayStatus = iota
	ReplayStarting
	ReplayArmed
)

// String names the status.
func (s ReplayStatus) String() string {
	switch s {
	case ReplayStarting:
		return "starting"
	case ReplayArmed:
		return "armed"
	}
	return "off"
}

// ErrReplayOff is a save with no instant replay armed.
var ErrReplayOff = errors.New("recorder: the instant replay is not armed")

const replayIcon = "ld-history-symbolic"

// ConfigureReplay applies modules.recorder.replay.enable: switching it
// on arms the replay, off disarms it. An unchanged value leaves a replay
// started or stopped from the CLI as it is.
func (s *State) ConfigureReplay(enable bool) {
	s.mu.Lock()
	changed := s.replayWanted != enable
	s.replayWanted = enable
	s.mu.Unlock()
	switch {
	case !changed:
	case enable:
		s.StartReplay()
	default:
		s.StopReplay()
	}
}

// StartReplay arms the instant replay on the configured output. The
// Off→Starting claim is synchronous; opening the output and starting
// the capture run off the caller.
func (s *State) StartReplay() {
	s.mu.Lock()
	if s.replayStatus != ReplayOff {
		s.mu.Unlock()
		return
	}
	s.replayStatus = ReplayStarting
	s.replayGen++
	gen := s.replayGen
	s.mu.Unlock()
	s.notify()
	go s.runReplay(gen)
}

func (s *State) runReplay(gen uint64) {
	cfg := s.config()
	opts := BuildOptions(cfg, time.Now())
	session, err := s.engine.OpenOutput(cfg.Replay.Output, opts.ShowCursor)
	if err != nil {
		s.replayFailed(gen, err.Error())
		return
	}
	rep, err := session.StartReplay(opts, cfg.Replay.Seconds.Span(), func(reason string) { s.replayEnded(gen, reason) }, s.notify)
	if err != nil {
		session.Close()
		s.replayFailed(gen, err.Error())
		return
	}
	s.mu.Lock()
	if s.replayStatus != ReplayStarting || s.replayGen != gen {
		s.mu.Unlock()
		stopReplay(rep, session)
		return
	}
	s.replayStatus = ReplayArmed
	s.replay, s.replaySession = rep, session
	s.mu.Unlock()
	s.notify()
	s.toast(i18n.T("recorder-toast-replay-armed"), replayIcon, startToastMS)
}

// StopReplay disarms the instant replay, freeing its ring; one still
// starting is torn down as it comes up.
func (s *State) StopReplay() {
	s.mu.Lock()
	if s.replayStatus == ReplayOff {
		s.mu.Unlock()
		return
	}
	s.replayGen++
	rep, session := s.takeReplayLocked()
	s.mu.Unlock()
	if rep != nil {
		stopReplay(rep, session)
	}
	s.notify()
}

// takeReplayLocked turns the replay off and hands back what it ran on,
// nil while it was only starting. The caller holds mu.
func (s *State) takeReplayLocked() (Replay, Session) {
	rep, session := s.replay, s.replaySession
	s.replayStatus = ReplayOff
	s.replay, s.replaySession = nil, nil
	return rep, session
}

func stopReplay(rep Replay, session Session) {
	if err := rep.Stop(); err != nil {
		log.Printf("recorder: stop the instant replay: %v", err)
	}
	session.Close()
}

// SaveReplay writes the replay's last seconds to a new file in the
// output directory, without stopping it, and returns the file.
func (s *State) SaveReplay() (string, error) {
	s.mu.Lock()
	rep := s.replay
	s.mu.Unlock()
	if rep == nil {
		return "", ErrReplayOff
	}
	cfg := s.config()
	path := ReplayPath(cfg.OutputDirectory, cfg.OutputFormat, time.Now())
	if err := os.MkdirAll(filepath.Dir(path), outputDirPerms); err != nil {
		return "", fmt.Errorf("recorder: the output directory: %w", err)
	}
	if err := rep.Save(path, cfg.OutputFormat); err != nil {
		s.showError(fmt.Sprintf("%s: %v", i18n.T("recorder-toast-replay-failed"), err))
		return "", err
	}
	s.toast(i18n.T("recorder-toast-replay-saved"), replayIcon, stopToastMS)
	if s.hooks.Notify != nil {
		s.hooks.Notify(i18n.T("notification-source-recorder"), i18n.T("recorder-notification-replay-saved"), path, replayIcon)
	}
	return path, nil
}

// replayFailed settles a start that failed to Off, unless a stop or a
// newer start came first, and reports it.
func (s *State) replayFailed(gen uint64, reason string) {
	s.mu.Lock()
	if s.replayGen != gen || s.replayStatus != ReplayStarting {
		s.mu.Unlock()
		return
	}
	s.replayStatus = ReplayOff
	s.mu.Unlock()
	log.Printf("recorder: failed to arm the instant replay: %s", reason)
	s.notify()
	s.showError(fmt.Sprintf("%s: %s", i18n.T("recorder-toast-replay-failed"), reason))
}

// replayEnded tears down an armed replay whose capture died on its own.
func (s *State) replayEnded(gen uint64, reason string) {
	s.mu.Lock()
	if s.replayGen != gen || s.replayStatus != ReplayArmed {
		s.mu.Unlock()
		return
	}
	s.replayGen++
	rep, session := s.takeReplayLocked()
	s.mu.Unlock()
	log.Printf("recorder: the instant replay ended unexpectedly: %s", reason)
	stopReplay(rep, session)
	s.notify()
	s.showError(fmt.Sprintf("%s: %s", i18n.T("recorder-toast-replay-failed"), reason))
}
