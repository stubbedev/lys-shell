package bar

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/config"
)

func tickSource(ticks chan struct{}) func(context.Context) (<-chan struct{}, func(), error) {
	return func(context.Context) (<-chan struct{}, func(), error) { return ticks, func() {}, nil }
}

func TestFollowRefreshFailsOnTheFirstRender(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	broken := errors.New("daemon gone")
	subscribed := false
	subscribe := func(context.Context) (<-chan struct{}, func(), error) {
		subscribed = true
		return nil, nil, nil
	}
	err := followRefresh(ctx, "probe", subscribe, func() error { return broken })
	if !errors.Is(err, broken) || !strings.HasPrefix(err.Error(), "probe: ") {
		t.Errorf("error = %v, want it to name the module and wrap %v", err, broken)
	}
	if subscribed {
		t.Error("a module whose first render failed subscribed anyway")
	}
}

func TestFollowRefreshLogsALaterFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	logged := func() string { return onHeadlessLoop(buf.String) }

	ctx := newTestContext(t, config.Defaults())
	ticks := make(chan struct{})
	var fail error
	renders := 0
	refresh := func() error {
		renders++
		return fail
	}
	if err := followRefresh(ctx, "probe", tickSource(ticks), refresh); err != nil {
		t.Fatal(err)
	}
	ticks <- struct{}{}
	waitHeadless(t, "the healthy tick renders", func() bool { return renders == 2 })
	if got := logged(); got != "" {
		t.Errorf("a healthy render logged %q", got)
	}

	onHeadlessLoop(func() bool { fail = errors.New("daemon gone"); return true })
	ticks <- struct{}{}
	waitHeadless(t, "the failing tick renders", func() bool { return renders == 3 })
	if got := logged(); !strings.Contains(got, "probe: refresh: daemon gone") {
		t.Errorf("log = %q, want the failure with its module", got)
	}

	onHeadlessLoop(func() bool { fail = nil; return true })
	ticks <- struct{}{}
	waitHeadless(t, "the module keeps following after a failure", func() bool { return renders == 4 })
}
