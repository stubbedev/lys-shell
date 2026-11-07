package config

// BatteryConfig is ported from crates/wayle-config/src/schemas/modules/battery/mod.rs.
//
// Battery level, charging state, and a dropdown with the batteries of
// peripherals UPower reports (wireless mice, keyboards, controllers,
// headsets) and power-profile controls. Low and critical battery levels
// send critical notifications whether or not the module is placed.
//
// ::: warning
//
// This module uses `upower` (D-Bus) for battery information. Ensure `upower` daemon is running and exposes a battery device (verify with `upower --battery` or `upower --dump`).
//
// :::
type BatteryConfig struct {
	// Icons for battery levels from empty to full.
	//
	// The percentage is divided evenly among icons. With 5 icons:
	// 0-20% uses icons\[0\], 21-40% uses icons\[1\], etc.
	LevelIcons []string `cfg:"level-icons"`
	// Icon shown when battery is charging.
	ChargingIcon string `cfg:"charging-icon"`
	// Icon shown when battery is not present or in an error state.
	AlertIcon string `cfg:"alert-icon"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Show the module's state in a tooltip on hover.
	Tooltip bool `cfg:"tooltip"`
	// Icon foreground color. Auto selects based on variant for contrast.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Display percentage label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Format string for the label.
	//
	// ## Examples
	//
	// - `"{{ percent }}%"` - "45%"
	Format string `cfg:"format"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click.
	LeftClick ClickAction `cfg:"left-click"`
	// Action on right click.
	RightClick ClickAction `cfg:"right-click"`
	// Action on middle click.
	MiddleClick ClickAction `cfg:"middle-click"`
	// Action on scroll up.
	ScrollUp ClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown ClickAction `cfg:"scroll-down"`
	// Dynamic color thresholds based on battery percentage.
	//
	// Entries are checked in order; the last matching entry wins for each
	// color slot. Use `below` for low-value warnings (e.g., low battery).
	//
	// ## Example
	//
	// ```yaml
	// modules:
	//   battery:
	//     thresholds:
	//       - below: 40
	//         icon-color: status-warning
	//       - below: 20
	//         icon-color: status-error
	//         label-color: status-error
	// ```
	Thresholds []ThresholdEntry `cfg:"thresholds"`
	// Send a critical notification when the battery, or a peripheral UPower
	// reports such as a wireless mouse, enters UPower's low or critical
	// warning level.
	//
	// The levels follow UPower's `PercentageLow` and `PercentageCritical`.
	// Each level notifies once; plugging in and unplugging again re-arms them.
	NotifyLow bool `cfg:"notify-low"`
	// Notify once when the charging battery reaches this percentage. Set to
	// 0 to disable.
	NotifyChargedAt Percentage `cfg:"notify-charged-at"`
}

// DefaultsBattery returns the schema defaults.
func DefaultsBattery() BatteryConfig {
	return BatteryConfig{
		LevelIcons: []string{
			"ld-battery-symbolic",
			"ld-battery-low-symbolic",
			"ld-battery-medium-symbolic",
			"ld-battery-full-symbolic",
		},
		ChargingIcon:   "ld-battery-charging-symbolic",
		AlertIcon:      "ld-battery-warning-symbolic",
		BorderShow:     false,
		BorderColor:    mustColor("yellow"),
		IconShow:       true,
		Tooltip:        true,
		IconColor:      mustColor("auto"),
		IconBgColor:    mustColor("yellow"),
		LabelShow:      true,
		LabelColor:     mustColor("fg-default"),
		Format:         "{{ percent }}%",
		LabelMaxLength: 0,
		ButtonBgColor:  mustColor("bg-surface-elevated"),
		LeftClick:      ParseClickAction("dropdown:battery"),
		RightClick:     ClickAction{},
		MiddleClick:    ClickAction{},
		ScrollUp:       ClickAction{},
		ScrollDown:     ClickAction{},
		Thresholds:     []ThresholdEntry{},
		NotifyLow:      true,
	}
}

// Clicks returns the five input bindings.
func (c BatteryConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
