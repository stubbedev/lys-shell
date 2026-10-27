package bar

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// dropdownBuilders maps the Rust registry's names onto content
// builders; each returns the popover's card tree.
func dropdownBuilders() map[string]func(ctx ModuleContext) widget.Widget {
	return map[string]func(ctx ModuleContext) widget.Widget{
		"calendar":     calendarDropdown,
		"audio":        audioDropdown,
		"volume":       audioDropdown,
		"microphone":   audioDropdown,
		"brightness":   brightnessDropdown,
		"battery":      batteryDropdown,
		"network":      networkDropdown,
		"bluetooth":    bluetoothDropdown,
		"media":        mediaDropdown,
		"notification": notificationDropdown,
		"recorder":     recorderDropdown,
		"treeman":      treemanDropdown,
		"mail":         mailDropdown,
		"bitwarden":    bitwardenDropdown,
		"weather":      weatherDropdown,
		"dashboard":    dashboardDropdown,
	}
}

// dropdownFont is the dropdowns' face and text size: the body size
// times the styling scale, the --text-body the stylesheet sizes the
// lys-ui parts around them by.
func dropdownFont(ctx ModuleContext) (render.Font, float64) {
	return ctx.Font, widgets.TextPx(styling.Body, float64(ctx.Config.Styling.Scale))
}
