// Package recorder is the screen recorder service: the lifecycle state
// machine (idle, starting, recording, stopping), the elapsed timer,
// options from the modules.recorder config, and the engine. The engine
// asks the share picker for an output, has the compositor copy its
// frames into a buffer on the GPU behind it, encodes them to H.264 on
// that GPU's video encoder with an optional webcam overlay, mixes the
// microphone and the desktop's audio into Opus, and muxes both into MP4
// or Matroska.
package recorder

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/feed"
	"github.com/stubbedev/lys-shell/internal/version"
)

// Status is the lifecycle gate (state.rs's Status), updated
// synchronously so rapid clicks never double-start.
type Status int

// Statuses.
const (
	StatusIdle Status = iota
	StatusStarting
	StatusRecording
	StatusStopping
)

// String names the status.
func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "starting"
	case StatusRecording:
		return "recording"
	case StatusStopping:
		return "stopping"
	}
	return "idle"
}

// Toast and notification constants (state.rs).
const (
	toastIcon      = "ld-circle-dot-symbolic"
	errorIcon      = "ld-alert-triangle-symbolic"
	savedIcon      = "ld-video-symbolic"
	startToastMS   = 1000
	stopToastMS    = 1500
	notifyAppName  = version.Name
	elapsedTick    = time.Second
	outputDirPerms = 0o755
)

// Session is a negotiated capture source, held across the start delay
// and closed when the recording ends.
type Session interface {
	// Start records the source with opts; term reports an unexpected
	// end (a source disconnect, a full disk), never a requested Stop.
	Start(opts Options, term func(reason string)) (Recording, error)
	Close()
}

// Recording is one running capture.
type Recording interface {
	// SetPaused pauses or resumes the capture: paused time is cut from
	// the file.
	SetPaused(paused bool)
	// Stop drains the encoders and finalizes the file, blocking until
	// it is written; the error says what of it could not be.
	Stop() error
}

// Engine opens capture sessions.
type Engine interface {
	// OpenSession negotiates the source, showing the picker when no
	// choice is remembered; ctx cancels it.
	OpenSession(ctx context.Context, showCursor bool) (Session, error)
}

// Hooks are the user-facing reports: an OSD toast and a desktop
// notification. Nil hooks are skipped.
type Hooks struct {
	Toast  func(label, icon string, durationMS uint32)
	Notify func(appName, summary, body, icon string)
}

// Change is the state the bar module, the dropdown, and the daemon
// read.
type Change struct {
	Status      Status
	Active      bool
	Paused      bool
	Preparing   bool
	ElapsedSecs uint32
	OutputPath  string
	// Error is why the last recording failed to start or ended on its
	// own, "" once a new one starts.
	Error string
}

// State is the recorder service. Safe for concurrent use.
type State struct {
	engine Engine
	config func() config.RecorderConfig
	hooks  Hooks

	mu          sync.Mutex
	status      Status
	active      bool
	paused      bool
	preparing   bool
	elapsed     uint32
	output      string
	lastErr     string
	recording   Recording
	session     Session
	cancelStart context.CancelFunc
	// gen numbers each start, so a late termination report from an
	// earlier recording is ignored.
	gen       uint64
	timerStop chan struct{}
	changes   *feed.Feed[Change]
}

// NewState builds the service over an engine, reading options from
// cfg at each start.
func NewState(engine Engine, cfg func() config.RecorderConfig, hooks Hooks) *State {
	return &State{engine: engine, config: cfg, hooks: hooks, changes: feed.New[Change](8)}
}

// Changes delivers a snapshot on every change and elapsed second to
// every subscriber; stop ends the feed.
func (s *State) Changes() (<-chan Change, func()) { return s.changes.Subscribe() }

// Snapshot reads the current state.
func (s *State) Snapshot() Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *State) snapshotLocked() Change {
	return Change{
		Status:      s.status,
		Active:      s.active,
		Paused:      s.paused,
		Preparing:   s.preparing,
		ElapsedSecs: s.elapsed,
		OutputPath:  s.output,
		Error:       s.lastErr,
	}
}

func (s *State) notify() { s.changes.Publish(s.Snapshot()) }

// Toggle keys off the lifecycle, not active: during the start delay a
// toggle cancels the pending start (state.rs's toggle).
func (s *State) Toggle() {
	s.mu.Lock()
	idle := s.status == StatusIdle
	s.mu.Unlock()
	if idle {
		s.Start()
	} else {
		s.Stop()
	}
}

// Start begins a recording with the current config. The Idle→Starting
// claim is synchronous, so a second call while one is in flight is a
// no-op; the rest (picker, delay, recording) runs off the caller.
func (s *State) Start() {
	s.mu.Lock()
	if s.status != StatusIdle {
		s.mu.Unlock()
		return
	}
	s.status = StatusStarting
	s.preparing = true
	s.lastErr = ""
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelStart = cancel
	s.gen++
	gen := s.gen
	s.mu.Unlock()
	s.notify()
	go s.runStart(ctx, gen)
}

// runStart is run_start: the output directory, the capture session
// (first, so the picker answers the click), the announced start delay,
// then the recording.
func (s *State) runStart(ctx context.Context, gen uint64) {
	cfg := s.config()
	opts := BuildOptions(cfg, time.Now())
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), outputDirPerms); err != nil {
		s.failStart(err)
		return
	}
	session, err := s.engine.OpenSession(ctx, opts.ShowCursor)
	if ctx.Err() != nil {
		if session != nil {
			session.Close()
		}
		s.resetIdle()
		return
	}
	if err != nil {
		s.failStart(err)
		return
	}
	s.toast(i18n.T("recorder-toast-starting"), toastIcon, startToastMS)
	select {
	case <-ctx.Done():
		session.Close()
		s.resetIdle()
		return
	case <-time.After(time.Duration(cfg.StartDelayMs) * time.Millisecond):
	}
	rec, err := session.Start(opts, func(reason string) { s.unexpectedStop(gen, reason) })
	if err != nil {
		session.Close()
		s.failStart(err)
		return
	}
	s.commit(ctx, rec, session, opts.OutputPath)
}

// commit is commit_recording: Recording, unless a stop cancelled the
// start meanwhile, which tears the fresh recording back down.
func (s *State) commit(ctx context.Context, rec Recording, session Session, path string) {
	s.mu.Lock()
	if s.status != StatusStarting || ctx.Err() != nil {
		s.mu.Unlock()
		if err := rec.Stop(); err != nil {
			log.Printf("recorder: finalize a recording cancelled while starting: %v", err)
		}
		session.Close()
		s.resetIdle()
		return
	}
	s.status = StatusRecording
	s.recording, s.session = rec, session
	s.output = path
	s.elapsed = 0
	s.paused = false
	s.active = true
	s.preparing = false
	s.startTimerLocked()
	s.mu.Unlock()
	s.notify()
}

// failStart is fail_start.
func (s *State) failStart(err error) {
	log.Printf("recorder: failed to start recording: %v", err)
	s.fail(err.Error())
}

// fail settles to Idle with reason as the error state, and reports it.
func (s *State) fail(reason string) {
	s.mu.Lock()
	s.lastErr = reason
	s.mu.Unlock()
	s.resetIdle()
	s.showError(fmt.Sprintf("%s: %s", i18n.T("recorder-toast-failed"), reason))
}

// unexpectedStop is handle_unexpected_stop: a live recording that died
// on its own is torn down and reported.
func (s *State) unexpectedStop(gen uint64, reason string) {
	s.mu.Lock()
	if s.status != StatusRecording || s.gen != gen {
		s.mu.Unlock()
		return
	}
	s.status = StatusStopping
	rec, session := s.recording, s.session
	s.mu.Unlock()
	log.Printf("recorder: recording terminated unexpectedly: %s", reason)
	if err := rec.Stop(); err != nil {
		log.Printf("recorder: finalize the terminated recording: %v", err)
	}
	session.Close()
	s.fail(reason)
}

// Stop stops the recording, or cancels one still starting. Only the
// first call from Starting or Recording does work. From Recording the
// UI flips to stopped at once, the blocking finalize runs off the
// caller, and the status settles to Idle after it.
func (s *State) Stop() {
	s.mu.Lock()
	prev := s.status
	if prev == StatusIdle || prev == StatusStopping {
		s.mu.Unlock()
		return
	}
	s.status = StatusStopping
	if s.cancelStart != nil {
		s.cancelStart()
	}
	if prev == StatusStarting {
		// The in-flight start sees the cancellation and resets.
		s.mu.Unlock()
		s.notify()
		return
	}
	s.stopTimerLocked()
	s.active, s.paused, s.elapsed = false, false, 0
	rec, session, path := s.recording, s.session, s.output
	s.recording, s.session = nil, nil
	s.mu.Unlock()
	s.notify()
	go func() {
		err := rec.Stop()
		session.Close()
		if err != nil {
			log.Printf("recorder: finalize %s: %v", path, err)
			s.fail(err.Error())
			return
		}
		s.mu.Lock()
		s.status = StatusIdle
		s.mu.Unlock()
		s.notify()
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			s.toast(i18n.T("recorder-toast-stopped"), toastIcon, stopToastMS)
			if s.hooks.Notify != nil {
				s.hooks.Notify(notifyAppName, i18n.T("recorder-notification-saved"), path, savedIcon)
			}
			return
		}
		log.Printf("recorder: recording produced no output file: %s", path)
		s.showError(i18n.T("recorder-toast-failed"))
	}()
}

// SetPaused pauses or resumes an active recording.
func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	rec := s.recording
	if !s.active || rec == nil || s.paused == paused {
		s.mu.Unlock()
		return
	}
	rec.SetPaused(paused)
	s.paused = paused
	s.mu.Unlock()
	s.notify()
}

// resetIdle is reset_idle.
func (s *State) resetIdle() {
	s.mu.Lock()
	s.stopTimerLocked()
	s.status = StatusIdle
	s.preparing, s.active, s.paused = false, false, false
	s.elapsed = 0
	s.recording, s.session = nil, nil
	s.mu.Unlock()
	s.notify()
}

// startTimerLocked counts recorded seconds, skipping paused ones
// (start_timer). The caller holds mu.
func (s *State) startTimerLocked() {
	stop := make(chan struct{})
	s.timerStop = stop
	go func() {
		ticker := time.NewTicker(elapsedTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.timerStop != stop || !s.active {
					s.mu.Unlock()
					return
				}
				if s.paused {
					s.mu.Unlock()
					continue
				}
				s.elapsed++
				s.mu.Unlock()
				s.notify()
			}
		}
	}()
}

func (s *State) stopTimerLocked() {
	if s.timerStop != nil {
		close(s.timerStop)
		s.timerStop = nil
	}
}

func (s *State) toast(label, icon string, ms uint32) {
	if s.hooks.Toast != nil {
		s.hooks.Toast(label, icon, ms)
	}
}

// showError is show_error: a toast and a desktop notification.
func (s *State) showError(message string) {
	s.toast(message, errorIcon, stopToastMS)
	if s.hooks.Notify != nil {
		s.hooks.Notify(notifyAppName, i18n.T("recorder-notification-failed"), message, errorIcon)
	}
}

// BuildOptions is build_options: the recording's options from the
// config, the output named for now.
func BuildOptions(cfg config.RecorderConfig, now time.Time) Options {
	opts := Options{
		OutputPath:       OutputPath(cfg.OutputDirectory, cfg.OutputFormat, now),
		Format:           cfg.OutputFormat,
		Framerate:        cfg.Framerate,
		ShowCursor:       cfg.ShowCursor,
		Microphone:       cfg.Microphone,
		MicrophoneDevice: cfg.MicrophoneDevice,
		SystemAudio:      cfg.SystemAudio,
	}
	if cfg.WebcamEnabled {
		opts.Webcam = &Webcam{
			Device: cfg.WebcamDevice,
			X:      uint32(cfg.WebcamX),
			Y:      uint32(cfg.WebcamY),
			Size:   uint32(cfg.WebcamSize),
		}
	}
	return opts
}

// OutputPath is output_path: lys-shell-<local timestamp>.<format> in
// the configured directory, else the videos directory; a format's name
// is its extension.
func OutputPath(configuredDir string, format config.RecorderFormat, now time.Time) string {
	dir := configuredDir
	if dir == "" {
		dir = VideosDir()
	}
	return filepath.Join(dir, version.Name+"-"+now.Format("20060102-150405")+"."+string(format))
}

// VideosDir is videos_dir: $XDG_VIDEOS_DIR, else $HOME/Videos.
func VideosDir() string {
	if dir := os.Getenv("XDG_VIDEOS_DIR"); dir != "" {
		return dir
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, "Videos")
	}
	return "."
}

// FormatElapsed is format_elapsed: H:MM:SS above the hour, M:SS within.
func FormatElapsed(totalSecs uint32) string {
	hours := totalSecs / 3600
	minutes := (totalSecs % 3600) / 60
	seconds := totalSecs % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
