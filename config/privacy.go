package config

// PrivacyConfig is the privacy module.
//
// Privacy indicator.
//
// Shows while an app records a microphone, holds a camera open or shares
// the screen, with an icon for each; its dropdown names each app, with a
// button that mutes its recording, stops its camera or ends its share.
type PrivacyConfig struct {
	// Icon shown while an app records a microphone.
	Icon string `cfg:"icon"`
	// Icon shown while an app holds a camera open.
	CameraIcon string `cfg:"camera-icon"`
	// Icon shown while an app shares the screen.
	ScreenIcon string `cfg:"screen-icon"`
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
	// Display the number of apps beside the icons.
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

// DefaultsPrivacy returns the schema defaults.
func DefaultsPrivacy() PrivacyConfig {
	return PrivacyConfig{
		Icon:           "ld-mic-symbolic",
		CameraIcon:     "ld-webcam-symbolic",
		ScreenIcon:     "ld-monitor-symbolic",
		BorderShow:     false,
		BorderColor:    mustColor("yellow"),
		IconShow:       true,
		IconColor:      mustColor("auto"),
		IconBgColor:    mustColor("yellow"),
		LabelShow:      false,
		LabelColor:     mustColor("fg-default"),
		LabelMaxLength: 0,
		ButtonBgColor:  mustColor("bg-surface-elevated"),
		LeftClick:      ParseClickAction("dropdown:privacy"),
		RightClick:     ClickAction{},
		MiddleClick:    ClickAction{},
		ScrollUp:       ClickAction{},
		ScrollDown:     ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c PrivacyConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
