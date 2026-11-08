package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/stubbedev/lys-desktop/strftime"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/internal/walltimer"
)

// worldClockTemplate is helpers.rs's format_world_clock over one format:
// the format renders as a template with tz(zone, strftime) formatting now in
// that zone. An unknown zone fails the render, which shows as nothing
// (format_world_clock(...).unwrap_or_default()). Each zone and layout loads
// once; one goroutine renders it.
type worldClockTemplate struct {
	format  string
	env     jinja.Env
	load    func(string) (*time.Location, error)
	zones   map[string]*time.Location
	layouts map[string]*strftime.Layout
	names   strftime.Names
	now     time.Time
	finest  time.Duration
}

func newWorldClockTemplate(format string, names strftime.Names, load func(string) (*time.Location, error)) *worldClockTemplate {
	t := &worldClockTemplate{format: format, names: names, load: load, zones: map[string]*time.Location{}, layouts: map[string]*strftime.Layout{}, finest: time.Minute}
	t.env.AddFunction("tz", t.tz)
	return t
}

func (t *worldClockTemplate) tz(args []any, _ map[string]any) (any, error) {
	if len(args) != 2 {
		return nil, errors.New("tz takes a timezone and a format")
	}
	zone, _ := args[0].(string)
	layout, _ := args[1].(string)
	location, ok := t.zones[zone]
	if !ok {
		loaded, err := t.load(zone)
		if err != nil || zone == "" {
			log.Printf("world-clock: invalid timezone identifier %q", zone)
			return nil, fmt.Errorf("invalid timezone: %s", zone)
		}
		location = loaded
		t.zones[zone] = loaded
	}
	formatted, ok := t.layouts[layout]
	if !ok {
		compiled, err := strftime.Compile(layout)
		if err != nil {
			return "", nil //nolint:nilerr // a bad layout renders as nothing, as the Rust module does
		}
		formatted = compiled
		t.layouts[layout] = compiled
	}
	t.finest = min(t.finest, formatted.Resolution())
	return formatted.FormatIn(t.now.In(location), t.names), nil
}

func (t *worldClockTemplate) render(now time.Time) string {
	t.now = now
	out, err := t.env.Render(t.format, nil)
	if err != nil {
		return ""
	}
	return out
}

func worldClockRender(format string, now time.Time) string {
	return newWorldClockTemplate(format, shellClockNames(), time.LoadLocation).render(now)
}

// worldClock is the module: the format re-renders when its text can
// change.
type worldClockModule struct {
	btn *barButton
}

func newWorldClock(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("world-clock: requires the application loop")
	}
	cfg := ctx.Config.WorldClock
	m := &worldClockModule{}
	m.btn = newBarButton(ctx, moduleIcon(ctx, cfg.Icon()), "")
	template := newWorldClockTemplate(cfg.Format, shellClockNames(), time.LoadLocation)
	spec := samplerSpec[string]{
		key:   configKey("world-clock", ctx.Config),
		pause: pauseLocked,
		source: func(life context.Context, fire func()) error {
			go func() {
				fire()
				if err := walltimer.Start(life, template.finest, fire); err != nil {
					log.Printf("world-clock: %v", err)
				}
			}()
			return nil
		},
		sample: func() (string, bool) { return template.render(time.Now()), true },
		same:   func(a, b string) bool { return a == b },
	}
	if err := joinSampler(ctx.Life(), ctx.Polls, spec, func(text string) { ctx.Invoke(func() { m.btn.SetLabel(text) }) }); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *worldClockModule) Root() widget.Widget { return m.btn }
