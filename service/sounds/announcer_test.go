package sounds

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/fakeclock"
	"github.com/stubbedev/lys-desktop/service/notifications/notifypolicy"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/sounds/soundpolicy"
	"github.com/stubbedev/lys-lib/pulse/native"
	"github.com/stubbedev/lys-lib/pulse/pulsetest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/notifytest"
	"github.com/stubbedev/lys-shell/service/notifications"
)

type rig struct {
	svc   *notifications.Service
	srv   *pulsetest.Server
	a     *Announcer
	theme string
	muted bool
	logs  *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newRig(t *testing.T, policy soundpolicy.Policy) *rig {
	t.Helper()
	t.Setenv("PATH", "")
	chirp, err := os.ReadFile("testdata/chirp.oga")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	theme := filepath.Join(data, "sounds/freedesktop/stereo")
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"message-new-instant", "dialog-warning"} {
		if err := os.WriteFile(filepath.Join(theme, name+".oga"), chirp, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "speakers", "Speakers", 2))
	srv.SetDefaults("speakers", "")
	audio, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audio.Close() })
	svc := notifytest.NewService(t)
	svc.UseClock(fakeclock.New())
	r := &rig{svc: svc, srv: srv, theme: theme, logs: &syncBuffer{}}
	r.a = NewAnnouncer(svc, audio, NewTheme("", []string{data}), func() bool { return r.muted }, policy)
	log.SetOutput(r.logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return r
}

func (r *rig) notify(app string, hints map[string]dbus.Variant) {
	id := r.svc.NotifyHints(app, 0, "", "summary", "", nil, hints, 0)
	for _, n := range r.svc.Popups() {
		if n.ID == id {
			r.a.Announce(n)
			return
		}
	}
	r.a.Announce(&notifications.Notification{ID: id, AppName: app})
}

func h(kv ...any) map[string]dbus.Variant {
	out := map[string]dbus.Variant{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = dbus.MakeVariant(kv[i+1])
	}
	return out
}

func TestASoundNamePlaysItsThemeFileOnceDecoded(t *testing.T) {
	r := newRig(t, soundpolicy.Policy{Mode: soundpolicy.All, Default: "dialog-warning", Volume: 0.5})
	r.notify("chat", h("sound-name", "message-new-instant"))
	r.notify("chat", h("sound-name", "message-new-instant"))
	plays := r.srv.Plays()
	if len(plays) != 2 || plays[0].Name != "lys-shell:"+filepath.Join(r.theme, "message-new-instant.oga") {
		t.Fatalf("plays = %+v", plays)
	}
	if plays[0].Volume != native.VolumeNorm/2 || plays[0].Sink != "" {
		t.Errorf("played on %q at %#x, want the default sink at half volume", plays[0].Sink, plays[0].Volume)
	}
	if plays[0].Props["media.role"] != "event" || plays[0].Props["event.id"] != "message-new-instant" {
		t.Errorf("props = %v", plays[0].Props)
	}
	if r.srv.Uploads() != 1 || r.a.Decodes() != 1 {
		t.Errorf("two plays of one sound: %d uploads, %d decodes; want one each", r.srv.Uploads(), r.a.Decodes())
	}
	sample := r.srv.Samples()[plays[0].Name]
	if sample.SampleSpec.Channels != 2 || len(sample.Data) == 0 {
		t.Errorf("uploaded %+v, %d bytes", sample.SampleSpec, len(sample.Data))
	}
}

func TestSuppressedDoNotDisturbedSharedMutedAndMutedAppsPlayNothing(t *testing.T) {
	r := newRig(t, soundpolicy.Policy{Mode: soundpolicy.All, Default: "message-new-instant", MuteApps: []string{"spam*"}})
	r.notify("chat", h("suppress-sound", true))
	r.notify("spammer", nil)
	r.muted = true
	r.notify("chat", nil)
	r.muted = false
	r.svc.SetFocusRules(notifypolicy.FocusRules{AllowCritical: true})
	r.svc.SetDND(true)
	r.notify("chat", nil)
	r.svc.SetDND(false)
	r.svc.SetSharing(true)
	r.notify("chat", nil)
	if n := len(r.srv.Plays()); n != 0 {
		t.Fatalf("%d sounds played; want none", n)
	}
	r.svc.SetSharing(false)
	r.notify("chat", nil)
	if n := len(r.srv.Plays()); n != 1 {
		t.Fatalf("an unhindered notification played %d sounds, want one", n)
	}
}

func TestCriticalModeSoundsOnlyCriticalOnes(t *testing.T) {
	r := newRig(t, soundpolicy.Policy{Mode: soundpolicy.Critical, Default: "message-new-instant"})
	r.notify("chat", nil)
	r.notify("battery", h("urgency", byte(2)))
	if n := len(r.srv.Plays()); n != 1 {
		t.Fatalf("critical mode played %d sounds, want only the critical one", n)
	}
	r.a.SetPolicy(soundpolicy.Policy{Mode: soundpolicy.Off, Default: "message-new-instant"})
	r.notify("battery", h("urgency", byte(2)))
	if n := len(r.srv.Plays()); n != 1 {
		t.Fatal("off still played")
	}
}

func TestAMissingNamePlaysTheDefaultAndLogsOnce(t *testing.T) {
	r := newRig(t, soundpolicy.Policy{Mode: soundpolicy.All, Default: "dialog-warning"})
	for range 3 {
		r.notify("chat", h("sound-name", "no-such-sound"))
	}
	plays := r.srv.Plays()
	if len(plays) != 3 || !strings.HasSuffix(plays[0].Name, "dialog-warning.oga") {
		t.Fatalf("plays = %+v, want the default three times", plays)
	}
	if n := strings.Count(r.logs.String(), `"no-such-sound"`); n != 1 {
		t.Errorf("the miss was logged %d times, want once:\n%s", n, r.logs.String())
	}
}

func TestAServerThatForgotItsSamplesAfterAReconnectGetsThemAgain(t *testing.T) {
	r := newRig(t, soundpolicy.Policy{Mode: soundpolicy.All, Default: "message-new-instant"})
	r.notify("chat", nil)
	if r.srv.Uploads() != 1 {
		t.Fatal("no upload")
	}
	r.srv.ForgetSamples()
	r.srv.Disconnect()
	waitPlay(t, r, 2)
	if r.srv.Uploads() != 2 || r.a.Decodes() != 2 {
		t.Errorf("after a server restart: %d uploads, %d decodes", r.srv.Uploads(), r.a.Decodes())
	}
}

func waitPlay(t *testing.T, r *rig, want int) {
	t.Helper()
	for range 500 {
		r.notify("chat", nil)
		if len(r.srv.Plays()) >= want {
			return
		}
		sleepBriefly()
	}
	t.Fatalf("plays = %d, want %d", len(r.srv.Plays()), want)
}

func sleepBriefly() { time.Sleep(10 * time.Millisecond) }
