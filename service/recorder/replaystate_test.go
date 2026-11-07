package recorder_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/recorder/recordertest"
)

func armedReplay(t *testing.T, engine *recordertest.Engine, s *recorder.State) *recordertest.Replay {
	t.Helper()
	waitFor(t, "the replay armed", func() bool { return s.Snapshot().Replay == recorder.ReplayArmed })
	replays := engine.Replays()
	if len(replays) != 1 {
		t.Fatalf("%d replays started, want 1", len(replays))
	}
	return replays[0]
}

// Switching replay.enable on arms the replay on the configured output
// with the configured span; a keyframe updates the memory the snapshot
// reports; a save writes a new file in the output directory without
// stopping it, and says where; switching enable off stops the capture
// and closes the session.
func TestReplayArmsSavesAndDisarmsWithTheConfig(t *testing.T) {
	engine := &recordertest.Engine{}
	s, log := newState(t, engine, func(c *config.RecorderConfig) {
		c.OutputFormat = config.RecorderFormatMp4
		c.Replay.Seconds = 45
	})
	s.ConfigureReplay(false)
	if snap := s.Snapshot(); snap.Replay != recorder.ReplayOff || len(engine.Outputs()) != 0 {
		t.Fatalf("enable left off armed %+v (outputs %v)", snap, engine.Outputs())
	}
	s.ConfigureReplay(true)
	rp := armedReplay(t, engine, s)
	if got := engine.Outputs(); !slices.Equal(got, []string{config.ReplayOutputFocused}) || rp.Span != 45*time.Second {
		t.Errorf("opened %v with span %v, want the focused output and 45s", got, rp.Span)
	}
	rp.Keyframe(5 << 20)
	if got := s.Snapshot().ReplayBytes; got != 5<<20 {
		t.Errorf("snapshot reports %d bytes, want the ring's %d", got, 5<<20)
	}
	path, err := s.SaveReplay()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".mp4" || !strings.HasPrefix(filepath.Base(path), "lys-shell-replay-") {
		t.Errorf("saved to %s", path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replay mp4" {
		t.Errorf("the saved file holds %q (%v)", data, err)
	}
	if rp.Stops() != 0 || s.Snapshot().Replay != recorder.ReplayArmed {
		t.Errorf("a save stopped the replay: %d stops, %v", rp.Stops(), s.Snapshot().Replay)
	}
	toasts, notices := log.snapshot()
	if !slices.Contains(toasts, i18n.T("recorder-toast-replay-saved")) || !slices.Contains(notices, i18n.T("recorder-notification-replay-saved")+": "+path) {
		t.Errorf("toasts %q, notices %q", toasts, notices)
	}
	s.ConfigureReplay(true)
	if len(engine.Replays()) != 1 {
		t.Error("an unchanged enable armed a second replay")
	}
	s.ConfigureReplay(false)
	if snap := s.Snapshot(); snap.Replay != recorder.ReplayOff || snap.ReplayBytes != 0 {
		t.Errorf("after disabling = %+v", snap)
	}
	if rp.Stops() != 1 || engine.Sessions()[0].Closed() != 1 {
		t.Errorf("stops %d, session closes %d: the capture was left open", rp.Stops(), engine.Sessions()[0].Closed())
	}
}

// With no replay armed a save is the defined error and writes nothing.
func TestReplaySaveWhileOffFails(t *testing.T) {
	engine := &recordertest.Engine{}
	var dir string
	s, _ := newState(t, engine, func(c *config.RecorderConfig) { dir = c.OutputDirectory })
	if _, err := s.SaveReplay(); !errors.Is(err, recorder.ErrReplayOff) {
		t.Fatalf("save = %v, want %v", err, recorder.ErrReplayOff)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the output directory was touched: %v", err)
	}
}

// The CLI's start and stop work whatever the config says, and a
// recording runs beside an armed replay without touching it.
func TestReplayFromTheCLIBesideARecording(t *testing.T) {
	engine := &recordertest.Engine{WriteOutput: true}
	s, _ := newState(t, engine, func(c *config.RecorderConfig) { c.Replay.Output = "DP-2" })
	s.StartReplay()
	s.StartReplay()
	rp := armedReplay(t, engine, s)
	if got := engine.Outputs(); !slices.Equal(got, []string{"DP-2"}) {
		t.Errorf("opened %v, want DP-2 once", got)
	}
	s.Start()
	waitFor(t, "recording", func() bool { return s.Snapshot().Active })
	s.Stop()
	waitFor(t, "idle", func() bool { return s.Snapshot().Status == recorder.StatusIdle })
	if rp.Stops() != 0 || s.Snapshot().Replay != recorder.ReplayArmed {
		t.Errorf("the recording stopped the replay: %d stops", rp.Stops())
	}
	s.StopReplay()
	s.StopReplay()
	if rp.Stops() != 1 {
		t.Errorf("%d stops, want 1", rp.Stops())
	}
}

// A replay that cannot open its output, or whose capture dies, settles
// off and says why; a failed save leaves it armed and reports it.
func TestReplayFailuresAreReported(t *testing.T) {
	engine := &recordertest.Engine{OpenErr: errors.New("recorder: the output is gone: DP-9")}
	s, log := newState(t, engine, nil)
	s.StartReplay()
	waitFor(t, "the failure report", func() bool { toasts, _ := log.snapshot(); return len(toasts) > 0 })
	if snap := s.Snapshot(); snap.Replay != recorder.ReplayOff {
		t.Errorf("after a failed open = %+v", snap)
	}
	if toasts, _ := log.snapshot(); !strings.Contains(toasts[0], "DP-9") {
		t.Errorf("toasts %q", toasts)
	}

	engine = &recordertest.Engine{}
	s, log = newState(t, engine, nil)
	s.StartReplay()
	rp := armedReplay(t, engine, s)
	rp.FailSaves(errors.New("disk full"))
	if _, err := s.SaveReplay(); err == nil {
		t.Error("a failing save succeeded")
	}
	if s.Snapshot().Replay != recorder.ReplayArmed {
		t.Error("a failed save disarmed the replay")
	}
	rp.Fail("recorder: the output is gone: DP-1")
	waitFor(t, "off", func() bool { return s.Snapshot().Replay == recorder.ReplayOff })
	if rp.Stops() != 1 || engine.Sessions()[0].Closed() != 1 {
		t.Errorf("stops %d, closes %d after the capture died", rp.Stops(), engine.Sessions()[0].Closed())
	}
	toasts, _ := log.snapshot()
	if !slices.ContainsFunc(toasts, func(s string) bool { return strings.Contains(s, "disk full") }) ||
		!slices.ContainsFunc(toasts, func(s string) bool { return strings.Contains(s, "DP-1") }) {
		t.Errorf("toasts %q", toasts)
	}
}

func TestReplayPath(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 5, 3, 120_000_000, time.Local)
	got := recorder.ReplayPath("/v", config.RecorderFormatMkv, now)
	if got != "/v/lys-shell-replay-20261107-090503.120.mkv" {
		t.Errorf("ReplayPath = %s", got)
	}
	if other := recorder.ReplayPath("/v", config.RecorderFormatMkv, now.Add(time.Millisecond)); other == got {
		t.Error("two saves a millisecond apart share a name")
	}
}
