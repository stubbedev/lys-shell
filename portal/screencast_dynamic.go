package portal

import (
	"fmt"

	"github.com/stubbedev/lys-lib/capture"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// dynamicCaptureOffered asks the compositor, on a capture connection of
// its own, whether it offers its cast target as a capture source.
func dynamicCaptureOffered() (bool, error) {
	c, err := capture.Connect()
	if err != nil {
		return false, fmt.Errorf("connect to ask for the cast target: %w", err)
	}
	defer func() { _ = c.Close() }()
	return c.HasDynamicCapture(), nil
}

// offersDynamic reports whether the picker offers the cast target; a
// compositor that cannot be asked offers none, with the reason logged.
func (c *screenCast) offersDynamic() bool {
	offered, err := c.dynamicOffered()
	if err != nil {
		warnf("screencast: the cast target is not offered: %v", err)
	}
	return offered
}

func isDynamic(t sharepicker.Target) bool { return t.Kind == sharepicker.TargetDynamic }

// onOutput reports whether t is a whole output or a region of one.
func onOutput(t sharepicker.Target) bool {
	return t.Kind == sharepicker.TargetOutput || t.Kind == sharepicker.TargetRegion
}

// sessionTargetName names a window or the cast target in the log.
func sessionTargetName(t sharepicker.Target) string {
	if isDynamic(t) {
		return "the cast target"
	}
	return "window " + t.Name
}
