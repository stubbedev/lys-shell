package bar

import (
	"context"
	"errors"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/pulse/native"
	"github.com/stubbedev/lys-shell/service/pulse/pulsetest"
)

type audioPresenceModule interface {
	Module
	setChrome(moduleChrome)
	refresh() error
}

func audioPresenceCases(ctx ModuleContext, source pulse.Source) []struct {
	name   string
	module audioPresenceModule
	btn    func() *barButton
} {
	v := &volumeModule{ctx: ctx, source: source}
	v.btn = newBarButton(ctx, nil, "")
	mic := &microphoneModule{ctx: ctx, source: source}
	mic.btn = newBarButton(ctx, nil, "")
	return []struct {
		name   string
		module audioPresenceModule
		btn    func() *barButton
	}{
		{"volume", v, func() *barButton { return v.btn }},
		{"microphone", mic, func() *barButton { return mic.btn }},
	}
}

func TestAudioModulesFollowTheDefaultDevice(t *testing.T) {
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "speakers", "Speakers", 2))
	srv.PutSource(pulsetest.Source(2, "speakers.monitor", "Monitor of Speakers", 1, "speakers"))
	srv.PutSource(pulsetest.Source(10, "mic", "Microphone", native.InvalidIndex, ""))
	svc, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = svc
	cases := audioPresenceCases(ctx, svc)
	chromes := make([]*widget.Box, len(cases))
	for i, tc := range cases {
		chromes[i] = widget.NewBox(widget.Row, 0, 0)
		if err := onHeadlessLoop(func() error {
			tc.module.setChrome(chromes[i])
			return followPulse(ctx, tc.name, tc.module.refresh)
		}); err != nil {
			t.Fatalf("%s without a default device: want a module, got %v", tc.name, err)
		}
		if onHeadlessLoop(chromes[i].Visible) {
			t.Errorf("%s without a default device is shown", tc.name)
		}
	}

	srv.SetDefaults("speakers", "mic")
	for i, tc := range cases {
		waitHeadless(t, tc.name+" shown once the default appears", func() bool {
			return chromes[i].Visible() && tc.btn().Label().Text() != ""
		})
	}

	srv.RemoveSink(1)
	srv.RemoveSource(10)
	for i, tc := range cases {
		waitHeadless(t, tc.name+" hidden once the default is gone", func() bool {
			return !chromes[i].Visible()
		})
	}
}

func TestAudioModulesFailOnOtherErrors(t *testing.T) {
	broken := errors.New("broken pipe")
	source := &fakePulseSource{err: broken, ticks: make(chan struct{})}
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = source
	for _, tc := range audioPresenceCases(ctx, source) {
		err := onHeadlessLoop(func() error { return followPulse(ctx, tc.name, tc.module.refresh) })
		if !errors.Is(err, broken) {
			t.Errorf("%s: error = %v, want it to wrap %v", tc.name, err, broken)
		}
	}
}
