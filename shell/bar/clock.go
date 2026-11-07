package bar

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/walltimer"
)

// clock renders the time through the configured strftime format beside
// the clock icon, and wakes when the format's text can change: on the
// minute, or the second for a format that shows seconds.
type clock struct {
	btn *barButton
}

func newClock(ctx ModuleContext) (Module, error) {
	layout := ctx.Config.Clock.Format.Layout()
	if layout == nil {
		return nil, errors.New("clock: no format")
	}
	cfg, store := ctx.Config.Clock, ctx.Calendar
	btn := newBarButton(ctx, moduleIcon(ctx, cfg.Icon()), clockLabel(cfg, store, time.Now()))
	if ctx.App == nil {
		return &clock{btn: btn}, nil
	}
	spec := samplerSpec[string]{
		key:   configKey("clock", ctx.Config),
		pause: pauseLocked,
		source: func(life context.Context, fire func()) error {
			if store != nil && strings.Contains(cfg.Format.String(), nextEventPlaceholder) {
				changes, stop := store.Changes()
				go func() {
					defer stop()
					for {
						select {
						case <-life.Done():
							return
						case <-changes:
							fire()
						}
					}
				}()
			}
			return walltimer.Start(life, layout.Resolution(), fire)
		},
		sample: func() (string, bool) { return clockLabel(cfg, store, time.Now()), true },
		same:   func(a, b string) bool { return a == b },
	}
	if err := joinSampler(ctx.Life(), ctx.Polls, spec, func(text string) { ctx.Invoke(func() { btn.SetLabel(text) }) }); err != nil {
		return nil, err
	}
	return &clock{btn: btn}, nil
}

func (c *clock) Root() widget.Widget { return c.btn }
