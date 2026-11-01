package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/internal/walltimer"
	"github.com/stubbedev/lys-shell/strftime"
)

// worldClockRender is helpers.rs's format_world_clock: the format
// renders as a template with tz(zone, strftime) formatting now in that
// zone. An unknown zone fails the render, which shows as nothing
// (format_world_clock(...).unwrap_or_default()).
func worldClockRender(format string, now time.Time) string {
	return worldClockRenderTracking(format, now, func(*strftime.Layout) {})
}

// worldClockRenderTracking is worldClockRender, reporting each strftime
// layout the template uses.
func worldClockRenderTracking(format string, now time.Time, used func(*strftime.Layout)) string {
	var env jinja.Env
	env.AddFunction("tz", func(args []any, _ map[string]any) (any, error) {
		if len(args) != 2 {
			return nil, errors.New("tz takes a timezone and a format")
		}
		zone, _ := args[0].(string)
		layout, _ := args[1].(string)
		location, err := time.LoadLocation(zone)
		if err != nil || zone == "" {
			log.Printf("world-clock: invalid timezone identifier %q", zone)
			return nil, fmt.Errorf("invalid timezone: %s", zone)
		}
		formatted, err := strftime.Compile(layout)
		if err != nil {
			return "", nil
		}
		used(formatted)
		return formatted.Format(now.In(location)), nil
	})
	out, err := env.Render(format, nil)
	if err != nil {
		return ""
	}
	return out
}

// worldClockResolution is how often the template's text can change: the
// finest of the layouts it formats with.
func worldClockResolution(format string) time.Duration {
	resolution := time.Minute
	worldClockRenderTracking(format, time.Now(), func(l *strftime.Layout) {
		resolution = min(resolution, l.Resolution())
	})
	return resolution
}

// worldClock is the module: the format re-renders when its text can
// change.
type worldClockModule struct {
	ctx  ModuleContext
	btn  *barButton
	stop func()
}

func newWorldClock(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("world-clock: requires the application loop")
	}
	cfg := ctx.Config.WorldClock
	m := &worldClockModule{ctx: ctx}
	m.btn = newBarButton(ctx, moduleIcon(ctx, cfg.Icon()), "")
	m.apply(cfg.Format)
	runCtx, cancel := context.WithCancel(ctx.Life())
	m.stop = cancel
	update := func() { m.apply(m.ctx.Config.WorldClock.Format) }
	if err := walltimer.Start(runCtx, worldClockResolution(cfg.Format), func() { m.ctx.Invoke(update) }); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}

// apply renders the current format; an unchanged label is a no-op for
// the damage collector.
func (m *worldClockModule) apply(format string) {
	text := ""
	if m.ctx.Config.WorldClock.LabelShow {
		text = worldClockRender(format, time.Now())
	}
	m.btn.SetLabel(text)
}

func (m *worldClockModule) Root() widget.Widget { return m.btn }

// Stop ends the render ticker.
func (m *worldClockModule) Stop() { m.stop() }
