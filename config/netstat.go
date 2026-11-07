package config

// NetstatConfig is ported from crates/wayle-config/src/schemas/modules/netstat/mod.rs.
//
// Network traffic counters (up/down rates).
type NetstatConfig struct {
	// Polling interval in milliseconds.
	//
	// Faster polling increases CPU usage.
	PollIntervalMs uint64 `cfg:"poll-interval-ms"`
	// Network interface to monitor.
	//
	// Use `"auto"` to select the first active interface, or specify an
	// interface name like `"eth0"` or `"wlan0"`.
	Interface string `cfg:"interface,type=NetInterface"`
	// Format string for the label.
	//
	// ## Examples
	//
	// - `"{{ down_auto }} {{ up_auto }}"` - "1.5 MiB/s 256 KiB/s"
	// - `"D:{{ down_mib }} U:{{ up_mib }}"` - "D:1.5 U:0.2"
	// - `"{{ interface }}: {{ down_auto }}"` - "wlan0: 1.5 MiB/s"
	Format string `cfg:"format"`
	// Icon name.
	IconName string `cfg:"icon-name"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Show the module's state in a tooltip on hover.
	Tooltip bool `cfg:"tooltip"`
	// Icon foreground color.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation. Set to 0 to disable.
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
}

// DefaultsNetstat returns the schema defaults.
func DefaultsNetstat() NetstatConfig {
	return NetstatConfig{
		PollIntervalMs: 2000,
		Interface:      "auto",
		Format:         "{{ down_auto }} {{ up_auto }}",
		IconName:       "ld-activity-symbolic",
		BorderShow:     false,
		BorderColor:    mustColor("red"),
		IconShow:       true,
		Tooltip:        true,
		IconColor:      mustColor("auto"),
		IconBgColor:    mustColor("red"),
		LabelShow:      true,
		LabelColor:     mustColor("fg-default"),
		LabelMaxLength: 0,
		ButtonBgColor:  mustColor("bg-surface-elevated"),
		LeftClick:      ClickAction{},
		RightClick:     ClickAction{},
		MiddleClick:    ClickAction{},
		ScrollUp:       ClickAction{},
		ScrollDown:     ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c NetstatConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
