package bar

import (
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/strftime"
)

// clock renders the time through the configured strftime format and
// keeps itself current once a second, beside the clock icon.
type clock struct {
	btn *barButton
}

func newClock(ctx ModuleContext) (Module, error) {
	layout, err := strftime.Compile(ctx.Config.Clock.Format)
	if err != nil {
		return nil, err
	}
	btn := newBarButton(ctx, moduleIcon(ctx, ctx.Config.Clock.Icon()), layout.Format(time.Now()))
	ctx.Every(time.Second, func() {
		btn.SetLabel(layout.Format(time.Now()))
	})
	return &clock{btn: btn}, nil
}

func (c *clock) Root() widget.Widget { return c.btn }
