package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/pulse/native"
	"github.com/stubbedev/lys-lib/pulse/pulsetest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// audioFixture serves dev.stubbe.lys.Audio1 on a private session bus over a
// fake PulseAudio server: HDMI and the default speakers (50%), and a
// muted microphone at 75%.
func audioFixture(t *testing.T) *pulsetest.Server {
	t.Helper()
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "hdmi", "HDMI Audio", 2))
	srv.PutSink(pulsetest.Sink(3, "speakers", "Built-in Speakers", 4))
	mic := pulsetest.Source(10, "mic", "Microphone", native.InvalidIndex, "")
	mic.Volume = native.CVolume{0xC000, 0xC000}
	mic.Mute = true
	srv.PutSource(mic)
	srv.SetDefaults("speakers", "mic")
	svc, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	bus := dbustest.Start(t)
	bus.UseAsSessionBus(t)
	release, err := pulse.NewDaemon(svc).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return srv
}

// runUntil reruns a read-only command until it prints want: property
// reads see a change once the server's subscription event lands.
func runUntil(t *testing.T, want string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stdout, stderr, code = runCaptured(t, false, args...)
		if (code == 0 && stdout == want) || time.Now().After(deadline) {
			return stdout, stderr, code
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAudioCommands(t *testing.T) {
	audioFixture(t)
	steps := []struct {
		args []string
		want string
		read bool
	}{
		{[]string{"audio", "output-volume"}, "Volume: 50%\n", true},
		{[]string{"audio", "output-volume", "+5"}, "Volume: 55%\n", false},
		{[]string{"audio", "output-volume", "-100"}, "Volume: 0%\n", false},
		{[]string{"audio", "output-volume", "150"}, "Volume: 100%\n", false},
		{[]string{"audio", "output-mute"}, "Muted\n", false},
		{[]string{"audio", "output-volume"}, "Volume: 100% (muted)\n", true},
		{[]string{"audio", "output-mute"}, "Unmuted\n", false},
		{[]string{"audio", "input-volume"}, "Volume: 75% (muted)\n", true},
		{[]string{"audio", "input-volume", "-30"}, "Volume: 45% (muted)\n", false},
		{[]string{"audio", "input-mute"}, "Unmuted\n", false},
		{[]string{"audio", "sinks"}, "Audio outputs:\n  HDMI Audio\n  Built-in Speakers *\n", true},
		{[]string{"audio", "sources"}, "Audio inputs:\n  Microphone *\n", true},
		{[]string{"audio", "status"}, "Volume: 100%\nDefault output: speakers\nOutputs: 2, Inputs: 1\n", true},
	}
	for _, s := range steps {
		var stdout, stderr string
		var code int
		if s.read {
			stdout, stderr, code = runUntil(t, s.want, s.args...)
		} else {
			stdout, stderr, code = runCaptured(t, false, s.args...)
		}
		if code != 0 || stdout != s.want {
			t.Errorf("%v: code %d stdout %q stderr %q, want %q", s.args, code, stdout, stderr, s.want)
		}
	}
}

func TestAudioWithoutTheShellSaysSo(t *testing.T) {
	dbustest.Start(t).UseAsSessionBus(t)
	if _, stderr, code := runCaptured(t, false, "audio", "status"); code != 1 || stderr != "Error: Audio service not running. Start lys-shell shell first.\n" {
		t.Errorf("not running: code %d %q", code, stderr)
	}
}

func TestAudioErrors(t *testing.T) {
	srv := audioFixture(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"audio", "output-volume", "loud"}, "Error: Invalid volume level: loud\n"},
		{[]string{"audio", "output-volume", "+a"}, "Error: Invalid volume delta: +a\n"},
		{[]string{"audio", "input-volume", "-"}, "Error: Invalid volume delta: -\n"},
	} {
		if _, stderr, code := runCaptured(t, false, c.args...); code != 1 || stderr != c.want {
			t.Errorf("%v: code %d %q, want %q", c.args, code, stderr, c.want)
		}
	}
	srv.FailNext(native.CmdSetSinkVolume, native.ErrAccess)
	if _, stderr, code := runCaptured(t, false, "audio", "output-volume", "10"); code != 1 || !strings.HasPrefix(stderr, "Error: Failed to set volume: ") {
		t.Errorf("server refusal: code %d %q, want a set-volume failure", code, stderr)
	}
}

func TestAudioWithoutADefaultInput(t *testing.T) {
	srv := audioFixture(t)
	srv.RemoveSource(10)
	runUntil(t, "No audio sources found\n", "audio", "sources")
	if _, stderr, _ := runCaptured(t, false, "audio", "input-mute"); stderr != "Error: Failed to toggle input mute: No default input device\n" {
		t.Errorf("no default: %q", stderr)
	}
	// Properties degrade to zero values without a default, as in Rust.
	if stdout, _, _ := runUntil(t, "Volume: 0%\n", "audio", "input-volume"); stdout != "Volume: 0%\n" {
		t.Errorf("no default volume: %q", stdout)
	}
}

func TestAudioEmptyLists(t *testing.T) {
	srv := audioFixture(t)
	srv.RemoveSink(1)
	srv.RemoveSink(3)
	srv.RemoveSource(10)
	if stdout, _, _ := runUntil(t, "No audio sinks found\n", "audio", "sinks"); stdout != "No audio sinks found\n" {
		t.Errorf("sinks: %q", stdout)
	}
	if stdout, _, _ := runUntil(t, "No audio sources found\n", "audio", "sources"); stdout != "No audio sources found\n" {
		t.Errorf("sources: %q", stdout)
	}
}

type recordingAudio struct {
	mu    sync.Mutex
	calls []string
	muted bool
}

func (a *recordingAudio) record(call string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call)
}

func (a *recordingAudio) recorded() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *recordingAudio) AdjustOutputVolume(delta float64) (float64, *dbus.Error) {
	a.record("AdjustOutputVolume")
	return 50 + delta, nil
}

func (a *recordingAudio) SetInputVolume(level float64) (float64, *dbus.Error) {
	a.record("SetInputVolume")
	return level, nil
}

type recordingAudioProps struct{ audio *recordingAudio }

func (p recordingAudioProps) Get(_, name string) (dbus.Variant, *dbus.Error) {
	p.audio.record("Get " + name)
	return dbus.MakeVariant(p.audio.muted), nil
}

func serveRecordingAudio(t *testing.T, muted bool) *recordingAudio {
	t.Helper()
	dbustest.Session(t)
	conn := dbustest.SessionConn(t)
	audio := &recordingAudio{muted: muted}
	if err := conn.Export(audio, pulse.ServicePath, pulse.Interface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(recordingAudioProps{audio}, pulse.ServicePath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(pulse.ServiceName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request %s: %v %v", pulse.ServiceName, reply, err)
	}
	return audio
}

func TestAVolumeChangeIsTheFirstCall(t *testing.T) {
	for _, c := range []struct {
		args  []string
		muted bool
		calls []string
		want  string
	}{
		{[]string{"audio", "output-volume", "+5"}, false, []string{"AdjustOutputVolume", "Get OutputMuted"}, "Volume: 55%\n"},
		{[]string{"audio", "input-volume", "30"}, true, []string{"SetInputVolume", "Get InputMuted"}, "Volume: 30% (muted)\n"},
	} {
		audio := serveRecordingAudio(t, c.muted)
		stdout, stderr, code := runCaptured(t, false, c.args...)
		if code != 0 || stdout != c.want {
			t.Errorf("%v: code %d stdout %q stderr %q, want %q", c.args, code, stdout, stderr, c.want)
		}
		if got := audio.recorded(); !slices.Equal(got, c.calls) {
			t.Errorf("%v: the daemon saw %v, want %v", c.args, got, c.calls)
		}
	}
}
