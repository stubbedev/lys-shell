package bar

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-ui/widget"
)

func tickSource(ticks chan struct{}) sourceSubscription {
	return func(context.Context) (<-chan struct{}, func() error, error) {
		return ticks, func() error { return nil }, nil
	}
}

func TestFollowReadsFailedFirstReadStaysHiddenAndNamesTheModule(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	ctx := newTestContext(t, config.Defaults())
	var broken atomic.Bool
	broken.Store(true)
	ticks := make(chan struct{})
	gate := &firstRead{}
	chrome := widget.NewBox(widget.Row, 0, 0)
	gate.hideUntilRead(chrome)
	read := func(context.Context) (int, error) {
		if broken.Load() {
			return 0, errors.New("daemon gone")
		}
		return 1, nil
	}
	followReads(ctx, "probe", gate, tickSource(ticks), read, func(int) {})
	if chrome.Visible() {
		t.Error("a failed first read showed the module")
	}
	if got := onHeadlessLoop(buf.String); !strings.Contains(got, "probe: first read: daemon gone") {
		t.Errorf("log = %q, want the failure with its module", got)
	}
	broken.Store(false)
	ticks <- struct{}{}
	waitHeadless(t, "a later good read shows the module", chrome.Visible)
}

func TestFollowReadsLogsALaterFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	logged := func() string { return onHeadlessLoop(buf.String) }

	ctx := newTestContext(t, config.Defaults())
	ticks := make(chan struct{})
	var fail atomic.Pointer[error]
	var reads atomic.Int32
	read := func(context.Context) (int, error) {
		reads.Add(1)
		if err := fail.Load(); err != nil {
			return 0, *err
		}
		return 1, nil
	}
	renders := 0
	followReads(ctx, "probe", &firstRead{}, tickSource(ticks), read, func(int) { renders++ })
	ticks <- struct{}{}
	waitHeadless(t, "the healthy tick renders", func() bool { return renders == 2 })
	if got := logged(); got != "" {
		t.Errorf("a healthy render logged %q", got)
	}

	broken := errors.New("daemon gone")
	fail.Store(&broken)
	ticks <- struct{}{}
	waitHeadless(t, "the failing tick is logged", func() bool {
		return strings.Contains(buf.String(), "probe: refresh: daemon gone")
	})

	fail.Store(nil)
	ticks <- struct{}{}
	waitHeadless(t, "the module keeps following after a failure", func() bool { return renders == 3 })
}

func TestFollowReadsLogsWhatStoppingMetAndNothingElse(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	ctx := newTestContext(t, config.Defaults())
	read := func(context.Context) (int, error) { return 1, nil }
	var stops atomic.Int32
	follow := func(module string, stopErr error) chan struct{} {
		ticks := make(chan struct{})
		subscribe := func(context.Context) (<-chan struct{}, func() error, error) {
			return ticks, func() error {
				stops.Add(1)
				return stopErr
			}, nil
		}
		followReads(ctx, module, &firstRead{}, subscribe, read, func(int) {})
		return ticks
	}
	close(follow("clean", nil))
	close(follow("probe", errors.New("match rule gone")))
	waitHeadless(t, "both subscriptions to stop", func() bool { return stops.Load() == 2 })
	waitHeadless(t, "the failed stop is logged", func() bool {
		return strings.Contains(buf.String(), "probe: stop following: match rule gone")
	})
	if got := onHeadlessLoop(buf.String); strings.Contains(got, "clean:") {
		t.Errorf("log = %q, want nothing of the clean stop", got)
	}
}
