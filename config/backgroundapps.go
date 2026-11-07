package config

// BackgroundAppsConfig is the background-apps module.
//
// Background apps indicator.
//
// Shows while a sandboxed app keeps running without a window, as the
// desktop portal's background monitor lists them; its dropdown names each
// app with the status it reports and a button that stops it.
type BackgroundAppsConfig struct {
	// Bar button icon.
	Icon string `cfg:"icon"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Icon foreground color. Auto selects based on variant for contrast.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Display the number of background apps beside the icon.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Default opens the dropdown.
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

// DefaultsBackgroundApps returns the schema defaults.
func DefaultsBackgroundApps() BackgroundAppsConfig {
	return BackgroundAppsConfig{
		Icon:           "ld-layers-symbolic",
		BorderShow:     false,
		BorderColor:    mustColor("blue"),
		IconShow:       true,
		IconColor:      mustColor("auto"),
		IconBgColor:    mustColor("blue"),
		LabelShow:      true,
		LabelColor:     mustColor("fg-default"),
		LabelMaxLength: 0,
		ButtonBgColor:  mustColor("bg-surface-elevated"),
		LeftClick:      ParseClickAction("dropdown:background-apps"),
		RightClick:     ClickAction{},
		MiddleClick:    ClickAction{},
		ScrollUp:       ClickAction{},
		ScrollDown:     ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c BackgroundAppsConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
