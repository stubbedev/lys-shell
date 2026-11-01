package bar

import (
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/walltimer"
	"github.com/stubbedev/lys-shell/strftime"
)

// clock renders the time through the configured strftime format beside
// the clock icon, and wakes when the format's text can change: on the
// minute, or the second for a format that shows seconds.
type clock struct {
	btn *barButton
}

func newClock(ctx ModuleContext) (Module, error) {
	layout, err := strftime.Compile(ctx.Config.Clock.Format)
	if err != nil {
		return nil, err
	}
	btn := newBarButton(ctx, moduleIcon(ctx, ctx.Config.Clock.Icon()), layout.Format(time.Now()))
	if ctx.App == nil {
		return &clock{btn: btn}, nil
	}
	update := func() { btn.SetLabel(layout.Format(time.Now())) }
	if err := walltimer.Start(ctx.Life(), layout.Resolution(), func() { ctx.Invoke(update) }); err != nil {
		return nil, err
	}
	return &clock{btn: btn}, nil
}

func (c *clock) Root() widget.Widget { return c.btn }
