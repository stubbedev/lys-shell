package bar

import (
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
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

// dropdownFont is the dropdowns' face and text size: lys-ui's text
// size, which its own components set, times the styling scale, so a
// dropdown's labels match the lys-ui parts around them.
func dropdownFont(ctx ModuleContext) (render.Font, float64) {
	return ctx.Font, widget.Current().TextSize * float64(ctx.Config.Styling.Scale)
}
