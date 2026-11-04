package bar

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stubbedev/lys-shell/service/bluetooth"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/service/mpris"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

type fakeDND struct{ on bool }

func (f *fakeDND) DND() bool      { return f.on }
func (f *fakeDND) SetDND(on bool) { f.on = on }

type fakeRadio struct {
	on      bool
	readErr error
	setErr  error
}

func (f *fakeRadio) Enabled(context.Context) (bool, error) { return f.on, f.readErr }
func (f *fakeRadio) SetEnabled(_ context.Context, on bool) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.on = on
	return nil
}

type fakeRecorder struct {
	active        bool
	starts, stops int
}

func (f *fakeRecorder) Snapshot() recorder.Change { return recorder.Change{Active: f.active} }
func (f *fakeRecorder) Start()                    { f.starts++ }
func (f *fakeRecorder) Stop()                     { f.stops++ }

type fakeOutputs struct {
	pulse.Source
	outputs []pulse.OutputDevice
	current pulse.DeviceKey
	noneErr error
	set     []pulse.DeviceKey
}

func (f *fakeOutputs) OutputDevices() []pulse.OutputDevice { return f.outputs }
func (f *fakeOutputs) DefaultSink(context.Context) (pulse.OutputDevice, error) {
	return pulse.OutputDevice{Key: f.current}, f.noneErr
}

func (f *fakeOutputs) SetDefault(_ context.Context, key pulse.DeviceKey) error {
	f.set = append(f.set, key)
	return nil
}

func titlesOf(controls []search.Control) []string {
	out := make([]string, len(controls))
	for i, c := range controls {
		out[i] = c.Title
	}
	return out
}

func runNow(t *testing.T, c search.Control) error {
	t.Helper()
	now, ok := c.Effect.(search.RunNow)
	if !ok {
		t.Fatalf("%s runs %T", c.Title, c.Effect)
	}
	return now(context.Background())
}

func TestDoNotDisturbSaysWhatChoosingItDoes(t *testing.T) {
	dnd := &fakeDND{}
	s := controlSources{dnd: dnd}
	row := s.toggles(context.Background())[0]
	if row.Title != "Turn On Do Not Disturb" {
		t.Errorf("off: %q", row.Title)
	}
	if err := runNow(t, row); err != nil || !dnd.on {
		t.Errorf("err %v, on %v", err, dnd.on)
	}
	row = s.toggles(context.Background())[0]
	if row.Title != "Turn Off Do Not Disturb" {
		t.Errorf("on: %q", row.Title)
	}
	if err := runNow(t, row); err != nil || dnd.on {
		t.Errorf("err %v, on %v", err, dnd.on)
	}
}

func TestWifiTogglesAndReportsItsFailure(t *testing.T) {
	radio := &fakeRadio{on: true}
	s := controlSources{wifi: radio}
	row := s.toggles(context.Background())[0]
	if row.Title != "Turn Off Wi-Fi" {
		t.Errorf("title %q", row.Title)
	}
	radio.setErr = errors.New("denied")
	if err := runNow(t, row); !errors.Is(err, radio.setErr) {
		t.Errorf("err = %v", err)
	}
	radio.setErr = nil
	if err := runNow(t, row); err != nil || radio.on {
		t.Errorf("err %v, on %v", err, radio.on)
	}
}

func TestWifiWhoseStateCannotBeReadOffersNoRow(t *testing.T) {
	s := controlSources{wifi: &fakeRadio{readErr: errors.New("no NetworkManager")}}
	if got := s.toggles(context.Background()); len(got) != 0 {
		t.Errorf("rows %q", titlesOf(got))
	}
}

func TestBluetoothFollowsItsAdapter(t *testing.T) {
	bt := newFakeBluetooth(bluetooth.State{Available: true, Enabled: true})
	s := controlSources{bluetooth: bt}
	row := s.toggles(context.Background())[0]
	if row.Title != "Turn Off Bluetooth" {
		t.Errorf("title %q", row.Title)
	}
	if err := runNow(t, row); err != nil || !slices.Contains(bt.calls, "disable") {
		t.Errorf("err %v, calls %q", err, bt.calls)
	}
	bt.setState(bluetooth.State{Available: true})
	row = s.toggles(context.Background())[0]
	if err := runNow(t, row); err != nil || !slices.Contains(bt.calls, "enable") {
		t.Errorf("err %v, calls %q", err, bt.calls)
	}
	bt.setState(bluetooth.State{})
	if got := s.toggles(context.Background()); len(got) != 0 {
		t.Errorf("no adapter, rows %q", titlesOf(got))
	}
}

func TestAudioOutputsOfferEveryOneButTheCurrent(t *testing.T) {
	speakers := pulse.OutputDevice{Key: pulse.DeviceKey{Index: 1}, Name: "speakers", Description: "Speakers"}
	headset := pulse.OutputDevice{Key: pulse.DeviceKey{Index: 2}, Name: "headset"}
	audio := &fakeOutputs{outputs: []pulse.OutputDevice{speakers, headset}, current: speakers.Key}
	s := controlSources{audio: audio}
	rows := s.toggles(context.Background())
	if got, want := titlesOf(rows), []string{"Switch Audio to \u2068headset\u2069"}; !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	if err := runNow(t, rows[0]); err != nil || !slices.Equal(audio.set, []pulse.DeviceKey{headset.Key}) {
		t.Errorf("err %v, set %v", err, audio.set)
	}
	audio.current, audio.noneErr = pulse.DeviceKey{}, pulse.ErrNoDefaultDevice
	if got, want := titlesOf(s.toggles(context.Background())), []string{"Switch Audio to \u2068Speakers\u2069", "Switch Audio to \u2068headset\u2069"}; !slices.Equal(got, want) {
		t.Errorf("no default: rows %q, want %q", got, want)
	}
}

func TestMediaControlsFollowTheActivePlayer(t *testing.T) {
	s := controlSources{media: newFakeMedia()}
	if got := s.mediaControls(context.Background()); len(got) != 0 {
		t.Errorf("no player, rows %q", titlesOf(got))
	}
	playing := mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Identity: "X", Title: "Song", Artist: "Band", State: mpris.StatePlaying, CanGoNext: true}
	media := newFakeMedia(playing)
	s = controlSources{media: media}
	rows := s.mediaControls(context.Background())
	if got, want := titlesOf(rows), []string{"Pause Media", "Next Track"}; !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	if rows[0].Subtitle != "Song · Band" {
		t.Errorf("subtitle %q", rows[0].Subtitle)
	}
	if err := runNow(t, rows[1]); err != nil || len(media.calls) != 1 {
		t.Errorf("err %v, calls %q", err, media.calls)
	}
	paused := mpris.Player{BusName: "org.mpris.MediaPlayer2.y", Identity: "Y", State: mpris.StatePaused, CanGoPrevious: true}
	rows = controlSources{media: newFakeMedia(paused)}.mediaControls(context.Background())
	if got, want := titlesOf(rows), []string{"Play Media", "Previous Track"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if rows[0].Subtitle != "Y" {
		t.Errorf("subtitle %q", rows[0].Subtitle)
	}
}

func TestCaptureWaitsForThePanelToClose(t *testing.T) {
	var shots []string
	picks := 0
	rec := &fakeRecorder{}
	s := controlSources{
		screenshot: func(mode, _ string) { shots = append(shots, mode) },
		recorder:   rec,
		pickColor:  func() { picks++ },
	}
	rows := s.captureControls(context.Background())
	want := []string{"Screenshot Region", "Screenshot Screen", "Screenshot Window", "Start Recording", "Pick Color"}
	if got := titlesOf(rows); !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	if len(shots) != 0 || rec.starts != 0 || picks != 0 {
		t.Error("listing started something")
	}
	for _, r := range rows {
		closed, ok := r.Effect.(search.RunOnceClosed)
		if !ok {
			t.Fatalf("%s runs %T, with the panel on the screen", r.Title, r.Effect)
		}
		closed()
	}
	if !slices.Equal(shots, []string{screenshot.ModeRegion, screenshot.ModeOutput, screenshot.ModeWindow}) || rec.starts != 1 || picks != 1 {
		t.Errorf("shots %q, starts %d, picks %d", shots, rec.starts, picks)
	}
}

func TestARunningRecordingOffersToStop(t *testing.T) {
	rec := &fakeRecorder{active: true}
	rows := controlSources{recorder: rec}.captureControls(context.Background())
	if got, want := titlesOf(rows), []string{"Stop Recording"}; !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	if err := runNow(t, rows[0]); err != nil || rec.stops != 1 || rec.starts != 0 {
		t.Errorf("err %v, stops %d, starts %d", err, rec.stops, rec.starts)
	}
}

func TestNoServicesOfferNoRows(t *testing.T) {
	var s controlSources
	ctx := context.Background()
	if n := len(s.toggles(ctx)) + len(s.mediaControls(ctx)) + len(s.captureControls(ctx)); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

func TestAPickedColorIsHex(t *testing.T) {
	for _, tc := range []struct {
		c    [3]float64
		want string
	}{
		{[3]float64{0, 0, 0}, "#000000"},
		{[3]float64{1, 1, 1}, "#FFFFFF"},
		{[3]float64{1.0 / 255, 128.0 / 255, 254.0 / 255}, "#0180FE"},
		{[3]float64{-1, 2, 0.5}, "#00FF80"},
	} {
		if got := hexOfColor(tc.c); got != tc.want {
			t.Errorf("hexOfColor(%v) = %s, want %s", tc.c, got, tc.want)
		}
	}
}
