package bar

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/pulse/native"
	"github.com/stubbedev/lys-desktop/service/pulse/pulsetest"
	"github.com/stubbedev/lys-ui/widget"
)

type audioPresenceModule interface {
	Module
	setChrome(moduleChrome)
}

func audioPresenceCases(ctx ModuleContext, source pulse.Source) []struct {
	name   string
	module audioPresenceModule
	btn    func() *barButton
	follow func()
} {
	v := &volumeModule{ctx: ctx, source: source}
	v.btn = newBarButton(ctx, nil, "")
	mic := &microphoneModule{ctx: ctx, source: source}
	mic.btn = newBarButton(ctx, nil, "")
	return []struct {
		name   string
		module audioPresenceModule
		btn    func() *barButton
		follow func()
	}{
		{"volume", v, func() *barButton { return v.btn }, func() {
			followReads(ctx, "volume", &firstRead{}, source.Subscribe, v.read, v.apply)
		}},
		{"microphone", mic, func() *barButton { return mic.btn }, func() {
			followReads(ctx, "microphone", &firstRead{}, source.Subscribe, mic.read, mic.apply)
		}},
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
			return nil
		}); err != nil {
			t.Fatalf("%s without a default device: want a module, got %v", tc.name, err)
		}
		tc.follow()
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

func TestAudioModulesStayHiddenOnOtherErrors(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	source := &fakePulseSource{err: errors.New("broken pipe"), ticks: make(chan struct{})}
	ctx := newTestContext(t, config.Defaults())
	ctx.Pulse = source
	for _, tc := range audioPresenceCases(ctx, source) {
		chrome := widget.NewBox(widget.Row, 0, 0)
		tc.module.setChrome(chrome)
		tc.follow()
		if chrome.Visible() {
			t.Errorf("%s: shown after a failed read", tc.name)
		}
		if got := onHeadlessLoop(buf.String); !strings.Contains(got, tc.name+": first read: ") || !strings.Contains(got, "broken pipe") {
			t.Errorf("%s: log = %q, want the failure with its module", tc.name, got)
		}
	}
}
