package bar

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/recorder/devicechoice"
	"github.com/stubbedev/lys-desktop/service/upower"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
)

type stuckCalls struct {
	release chan struct{}
	calls   atomic.Int64
}

func (h *stuckCalls) hold() {
	h.calls.Add(1)
	<-h.release
}

type stuckNetwork struct {
	*stuckCalls
}

func (f *stuckNetwork) Read(context.Context) (network.Snapshot, error) {
	f.hold()
	return network.Snapshot{}, nil
}

func (f *stuckNetwork) Subscribe(context.Context) (<-chan struct{}, func() error, error) {
	f.hold()
	return make(chan struct{}), func() error { return nil }, nil
}

type stuckBattery struct {
	*fakeBattery
	*stuckCalls
}

func (f *stuckBattery) Read(context.Context) (upower.Device, error) {
	f.hold()
	return upower.Device{}, nil
}

func (f *stuckBattery) Subscribe(context.Context) (<-chan struct{}, func() error, error) {
	f.hold()
	return make(chan struct{}), func() error { return nil }, nil
}

type stuckProfiles struct {
	fakePPSource
	*stuckCalls
}

func (f *stuckProfiles) Read(context.Context) (powerprofiles.Snapshot, error) {
	f.hold()
	return powerprofiles.Snapshot{}, nil
}

func (f *stuckProfiles) Subscribe(context.Context) (<-chan struct{}, func() error, error) {
	f.hold()
	return make(chan struct{}), func() error { return nil }, nil
}

type stuckBrightness struct {
	fakeBrightnessSource
	*stuckCalls
}

func (f *stuckBrightness) Devices(context.Context) ([]brightness.Device, error) {
	f.hold()
	return nil, nil
}

func (f *stuckBrightness) Subscribe(context.Context) (<-chan struct{}, func(), error) {
	f.hold()
	return make(chan struct{}), func() {}, nil
}

type stuckPulse struct {
	*fakePulseSource
	*stuckCalls
}

func (f *stuckPulse) Subscribe(context.Context) (<-chan struct{}, func(), error) {
	f.hold()
	return make(chan struct{}), func() {}, nil
}

func stuckServices(ctx *ModuleContext, h *stuckCalls) {
	ctx.Network = &stuckNetwork{stuckCalls: h}
	ctx.Battery = &stuckBattery{fakeBattery: newFakeBattery(upower.Device{}), stuckCalls: h}
	ctx.PowerProfiles = &stuckProfiles{stuckCalls: h}
	ctx.Brightness = &stuckBrightness{stuckCalls: h}
	ctx.Pulse = &stuckPulse{fakePulseSource: &fakePulseSource{ticks: make(chan struct{})}, stuckCalls: h}
}

func TestDropdownBuildersReturnWhileEveryServiceHolds(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	a := apptest.Run(t)
	ctx.App = a
	t.Cleanup(widget.SetAnimationsInstant(true))
	h := &stuckCalls{release: make(chan struct{})}
	t.Cleanup(func() { close(h.release) })
	stuckServices(&ctx, h)
	probing := make(chan struct{}, 1)
	prev := recorderCameras
	recorderCameras = func() []devicechoice.DeviceChoice {
		probing <- struct{}{}
		<-h.release
		return devicechoice.NoCameras()
	}
	t.Cleanup(func() { recorderCameras = prev })
	for name, build := range map[string]func(ModuleContext) widget.Widget{
		"battery":    batteryDropdown,
		"network":    networkDropdown,
		"brightness": brightnessDropdown,
		"audio":      audioDropdown,
		"recorder":   recorderDropdown,
		"dashboard":  dashboardDropdown,
	} {
		built := make(chan widget.Widget, 1)
		a.Invoke(func() { built <- build(ctx) })
		select {
		case w := <-built:
			c, ok := w.(dropdownCloser)
			if !ok {
				t.Fatalf("%s: %T does not release", name, w)
			}
			t.Cleanup(c.dropdownClosed)
		case <-time.After(10 * time.Second):
			t.Fatalf("the %s builder waited on a held service", name)
		}
		if name == "recorder" {
			apptest.Answer(t, "the camera probe, off the loop", probing)
		}
	}
	apptest.Until(t, a, "the services are being read off the loop", func() bool { return h.calls.Load() > 0 })
}

func TestRecorderCameraProbeFillsThePickerWhenItEnds(t *testing.T) {
	ctx := styledContext(t, config.Defaults())
	a := apptest.Run(t)
	ctx.App = a
	t.Cleanup(widget.SetAnimationsInstant(true))
	release := make(chan struct{})
	prev := recorderCameras
	recorderCameras = func() []devicechoice.DeviceChoice {
		<-release
		return []devicechoice.DeviceChoice{{ID: "", Label: "Automatic"}, {ID: "/dev/video0", Label: "Cam"}}
	}
	t.Cleanup(func() { recorderCameras = prev })
	v := apptest.On(a, func() *recorderView { return recorderDropdown(ctx).(*recorderView) })
	t.Cleanup(func() { apptest.On(a, func() bool { v.dropdownClosed(); return true }) })
	if apptest.On(a, func() bool { return v.webcam.Visible() }) {
		t.Fatal("the webcam section shows before the probe answered")
	}
	close(release)
	apptest.Until(t, a, "the probe's cameras in the picker", func() bool { return v.webcam.Visible() && len(v.cams) == 2 })
}

func TestFollowingEndsWithThePopover(t *testing.T) {
	var starts, stops atomic.Int64
	ctx := newTestContext(t, config.Defaults())
	f := newOpenFollow(ctx, func(life context.Context) {
		starts.Add(1)
		ctx.Go(func() {
			<-life.Done()
			stops.Add(1)
		})
	})
	f.startFollowing()
	f.dropdownOpened()
	if got := starts.Load(); got != 1 {
		t.Fatalf("a build then its open started %d follows, want one", got)
	}
	f.dropdownOpened()
	f.dropdownHidden()
	if f.hide == nil {
		t.Fatal("a popover closing as the next opens ended the follow")
	}
	f.dropdownHidden()
	if f.hide != nil {
		t.Fatal("the last popover closing left the follow running")
	}
	f.dropdownHidden()
	f.dropdownOpened()
	if got := starts.Load(); got != 2 {
		t.Fatalf("a reopen started %d follows in all, want 2", got)
	}
	f.dropdownClosed()
	ctx.gen.waitWork()
	if got := stops.Load(); got != 2 {
		t.Fatalf("closing ended %d of the 2 follows", got)
	}
	f.dropdownOpened()
	if got := starts.Load(); got != 2 {
		t.Fatalf("a released view followed again: %d follows", got)
	}
}

func TestClosedDropdownsAskNothingOfTheirServices(t *testing.T) {
	cfg := config.Defaults()
	ctx := styledContext(t, cfg)
	a := apptest.Run(t)
	ctx.App = a
	t.Cleanup(widget.SetAnimationsInstant(true))
	media := &countedMedia{fakeMediaSource: newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", State: mpris.StatePlaying})}
	net := newCountedNetwork()
	bat := &countedBattery{fakeBattery: newFakeBattery(upower.Device{IsPresent: true})}
	ctx.Media, ctx.Network, ctx.Battery = media, net, bat
	var hide []func()
	for _, build := range []func(ModuleContext) widget.Widget{mediaDropdown, networkDropdown, batteryDropdown, calendarDropdown} {
		w := apptest.On(a, func() widget.Widget { return build(ctx) })
		t.Cleanup(func() { apptest.On(a, func() bool { w.(dropdownCloser).dropdownClosed(); return true }) })
		hide = append(hide, func() { w.(dropdownHider).dropdownHidden() })
	}
	apptest.Until(t, a, "the open views' first reads", func() bool {
		return net.reads.Load() > 0 && bat.reads.Load() > 0 && media.positions.Load() > 0
	})
	apptest.On(a, func() bool {
		for _, h := range hide {
			h()
		}
		return true
	})
	apptest.Dispatched(t, a)
	reads, positions := net.reads.Load()+bat.reads.Load(), media.positions.Load()
	net.ticks <- struct{}{}
	bat.ticks <- struct{}{}
	media.feed <- struct{}{}
	time.Sleep(2*mediaPositionPollEvery + mediaPositionPollEvery/2)
	if got := net.reads.Load() + bat.reads.Load() - reads; got != 0 {
		t.Errorf("closed dropdowns read their services %d times", got)
	}
	if got := media.positions.Load() - positions; got != 0 {
		t.Errorf("a closed media dropdown polled Position %d times", got)
	}
}

func TestDashboardPollSamplesOffTheLoopAndPostsOnlyChanges(t *testing.T) {
	fixedProc(t)
	ctx := styledContext(t, config.Defaults())
	v := dashboardDropdown(ctx).(*dashboardView)
	a := apptest.Run(t)
	t.Cleanup(v.dropdownClosed)
	v.sample()
	if got := len(v.sample()); got != 0 {
		t.Errorf("unchanged stats posted %d updates", got)
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	v.pollers = append(v.pollers, func() func() {
		entered <- struct{}{}
		<-release
		return nil
	})
	v.ctx.App = a
	go v.poll()
	apptest.Answer(t, "the held read", entered)
	if !apptest.On(a, func() bool { return true }) {
		t.Fatal("unreachable")
	}
	close(release)
}

func (f *stuckPulse) InputDevices() []pulse.InputDevice {
	f.hold()
	return nil
}

func (f *stuckPulse) OutputDevices() []pulse.OutputDevice {
	f.hold()
	return nil
}

func (f *stuckPulse) PlaybackStreams() []pulse.AudioStream {
	f.hold()
	return nil
}
