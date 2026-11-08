package search

import (
	"context"
	"time"

	"github.com/stubbedev/lys-desktop/i18n"

	"github.com/stubbedev/lys-shell/service/launcher"
)

// controlTimeout bounds a control's call to the service behind it.
const controlTimeout = 5 * time.Second

// ControlEffect is what choosing a control does.
type ControlEffect interface{ isControlEffect() }

type (
	// RunNow runs while the panel stays open, so a failure can be said in
	// its footer. It closes on success.
	RunNow func(ctx context.Context) error
	// RunOnceClosed runs on the UI loop after the panel has left the
	// screen and given the keyboard back: what looks at the screen must
	// not find the panel on it. It must not wait.
	RunOnceClosed func()
)

func (RunNow) isControlEffect()        {}
func (RunOnceClosed) isControlEffect() {}

// Control is one thing the panel can switch or start in a service of the
// shell.
type Control struct {
	// ID stays the same across states: "Turn On" and "Turn Off" are one
	// control, so what is learned of it is kept.
	ID                        string
	Title, Subtitle, Keywords string
	Icon                      string
	Effect                    ControlEffect
}

// Controls offers rows of the shell's own services. Load reads their
// state each time the panel opens, as it decides what the rows say.
type Controls struct {
	load func(ctx context.Context) []Control
}

// NewControls offers what load returns.
func NewControls(load func(ctx context.Context) []Control) *Controls { return &Controls{load: load} }

// Spec implements Provider.
func (*Controls) Spec() Spec { return Spec{Section: SectionCommands, MinQuery: 2} }

// Load implements Provider.
func (c *Controls) Load(ctx context.Context, publish func([]Entry)) {
	controls := c.load(ctx)
	entries := make([]Entry, len(controls))
	for i, ctl := range controls {
		id := "control:" + ctl.ID
		entries[i] = Entry{
			ID: id, Learn: id, Title: ctl.Title, Subtitle: ctl.Subtitle, Keywords: ctl.Keywords,
			Icon: launcher.IconName(ctl.Icon), Data: ctl,
		}
	}
	publish(entries)
}

// Actions implements Provider.
func (*Controls) Actions(e Entry) []Action {
	ctl, ok := e.Data.(Control)
	if !ok {
		return nil
	}
	return []Action{{Title: ctl.Title, Icon: ctl.Icon, Run: func(ctx context.Context) Outcome {
		switch effect := ctl.Effect.(type) {
		case RunOnceClosed:
			return CloseThen{Run: effect}
		case RunNow:
			bounded, cancel := context.WithTimeout(ctx, controlTimeout)
			defer cancel()
			if err := effect(bounded); err != nil {
				return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", ctl.Title), i18n.Str("error", err.Error()))}
			}
			return Close{}
		}
		panic("search: a control without an effect: " + ctl.ID)
	}}}
}
