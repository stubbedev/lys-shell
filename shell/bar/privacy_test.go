package bar

import (
	"context"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/feed"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-lib/pulse/native"
	"github.com/stubbedev/lys-lib/pulse/pulsetest"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/service/privacy"
)

// micServer is a private audio server with a microphone and a monitor.
func micServer(t *testing.T) (*pulsetest.Server, *pulse.Service) {
	t.Helper()
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "speakers", "Speakers", 2))
	srv.PutSource(pulsetest.Source(2, "speakers.monitor", "Monitor of Speakers", 1, "speakers"))
	srv.PutSource(pulsetest.Source(3, "mic", "Built-in Microphone", native.InvalidIndex, ""))
	srv.SetDefaults("speakers", "mic")
	svc, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return srv, svc
}

func recording(index, source uint32, app string, pid int) native.StreamInfo {
	return native.StreamInfo{
		Index: index, Name: "capture", Device: source, OwnerModule: native.InvalidIndex, Client: native.InvalidIndex,
		SampleSpec: pulsetest.StereoSpec(), ChannelMap: pulsetest.StereoMap(),
		Volume: native.CVolume{native.VolumeNorm, native.VolumeNorm},
		Props:  native.PropList{"application.name": app, "application.process.id": strconv.Itoa(pid)},
		Format: native.FormatInfo{Encoding: native.EncodingPCM, Props: native.PropList{}},
	}
}

// fakeCameras is a camera list the test sets.
type fakeCameras struct {
	mu    sync.Mutex
	users []privacy.CameraUser
	tick  feed.Tick
}

func (f *fakeCameras) Users() []privacy.CameraUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.users)
}

func (f *fakeCameras) Subscribe() (<-chan struct{}, func()) { return f.tick.Subscribe() }

func (f *fakeCameras) set(users ...privacy.CameraUser) {
	f.mu.Lock()
	f.users = users
	f.mu.Unlock()
	feed.Notify(&f.tick)
}

func TestThePrivacyIndicatorNamesTheAppRecordingTheMicrophone(t *testing.T) {
	srv, svc := micServer(t)
	cams := &fakeCameras{}
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse, ctx.Cameras = svc, cams
	mod, err := newPrivacy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := mod.(*privacyModule)
	defer m.Stop()
	chrome := widget.NewBox(widget.Row, 0, 0)
	onHeadlessLoop(func() bool { m.setChrome(chrome); return true })
	if onHeadlessLoop(chrome.Visible) {
		t.Fatal("the indicator shows with nothing recording")
	}

	srv.PutSourceOutput(recording(40, 2, "OBS", 4242))
	srv.PutSourceOutput(recording(41, 3, "lys-shell", os.Getpid()))
	waitHeadless(t, "the streams in the service", func() bool { return len(svc.RecordingStreams()) == 2 })
	if onHeadlessLoop(chrome.Visible) {
		t.Fatal("a monitor recording or lys-shell's own recorder shows the indicator")
	}

	srv.PutSourceOutput(recording(42, 3, "Firefox", 4343))
	waitHeadless(t, "the microphone indicator", func() bool { return chrome.Visible() && m.mic.Visible() && !m.camera.Visible() })
	v := onHeadlessLoop(func() *privacyView { return privacyDropdown(ctx).(*privacyView) })
	defer onHeadlessLoop(func() bool { v.dropdownClosed(); return true })
	labels := onHeadlessLoop(func() []string { return treetest.Labels(v) })
	if !slices.Contains(labels, "Firefox") || slices.Contains(labels, "OBS") || slices.Contains(labels, "lys-shell") {
		t.Fatalf("dropdown labels %q, want Firefox alone", labels)
	}
	actions := onHeadlessLoop(func() []widget.Widget { return treetest.WithClass(v, "privacy-action") })
	if len(actions) != 1 {
		t.Fatalf("%d actions, want Firefox's mute", len(actions))
	}
	onHeadlessLoop(func() bool { actions[0].(*widget.Button).OnClick(); return true })
	waitHeadless(t, "Firefox's stream muted", func() bool {
		st, _ := srv.SourceOutput(42)
		return st.Mute
	})
	for _, other := range []uint32{40, 41} {
		if st, _ := srv.SourceOutput(other); st.Mute {
			t.Errorf("muting Firefox muted stream %d too", other)
		}
	}
	if src, _ := srv.Source(3); src.Mute {
		t.Error("muting Firefox muted the microphone")
	}

	srv.RemoveSourceOutput(42)
	waitHeadless(t, "the indicator gone", func() bool { return !chrome.Visible() })

	cams.set(privacy.CameraUser{PID: 777, Name: "zoom", Device: "/dev/video0"})
	waitHeadless(t, "the camera indicator", func() bool { return chrome.Visible() && m.camera.Visible() && !m.mic.Visible() })
	var signalled []int
	onHeadlessLoop(func() bool {
		v.signal = func(pid int, sig syscall.Signal) error {
			if sig == syscall.SIGTERM {
				signalled = append(signalled, pid)
			}
			return nil
		}
		v.rebuild()
		return true
	})
	labels = onHeadlessLoop(func() []string { return treetest.Labels(v) })
	if !slices.Contains(labels, "zoom") {
		t.Fatalf("dropdown labels %q, want the camera's process", labels)
	}
	onHeadlessLoop(func() bool { treetest.WithClass(v, "privacy-action")[0].(*widget.Button).OnClick(); return true })
	if !slices.Equal(signalled, []int{777}) {
		t.Errorf("ended %v, want the camera's process", signalled)
	}
	cams.set()
	waitHeadless(t, "the indicator gone again", func() bool { return !chrome.Visible() })
}
