package config

import (
	"reflect"

	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

// IdleInhibitConfig is ported from crates/wayle-config/src/schemas/modules/idle_inhibit/mod.rs.
//
// Toggle that prevents screen dim, lock, and suspend while active.
//
// Controllable from the CLI: `lys-shell idle on|off|duration|remaining|status`.
type IdleInhibitConfig struct {
	// Duration in minutes when service starts. 0 means indefinite.
	StartupDuration IdleInhibitDuration `cfg:"startup-duration"`
	// Icon when idle inhibitor is inactive.
	IconInactive string `cfg:"icon-inactive"`
	// Icon when idle inhibitor is active.
	IconActive string `cfg:"icon-active"`
	// Format string for the label.
	//
	// ## Placeholders
	//
	// - `{{ state }}` - Inhibitor state text (On, Off)
	// - `{{ remaining }}` - Time remaining (e.g., "45m", shows "--" when indefinite)
	// - `{{ duration }}` - Total duration (e.g., "60m", shows "--" when indefinite)
	//
	// ## Examples
	//
	// - `"{{ state }}"` - "On"
	// - `"{{ remaining }}/{{ duration }}"` - "45m/60m"
	// - `"{{ state }} ({{ remaining }})"` - "On (45m)"
	Format string `cfg:"format"`
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
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Default toggles indefinite idle inhibit.
	LeftClick ClickAction `cfg:"left-click"`
	// Action on right click. Default toggles timed idle inhibit.
	RightClick ClickAction `cfg:"right-click"`
	// Action on middle click.
	MiddleClick ClickAction `cfg:"middle-click"`
	// Action on scroll up.
	ScrollUp ClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown ClickAction `cfg:"scroll-down"`
}

// DefaultsIdleInhibit returns the schema defaults.
func DefaultsIdleInhibit() IdleInhibitConfig {
	return IdleInhibitConfig{
		StartupDuration: mustIdleInhibitDuration(60),
		IconInactive:    "ld-bed-symbolic",
		IconActive:      "ld-coffee-symbolic",
		Format:          "{{ state }}",
		BorderShow:      false,
		BorderColor:     mustColor("green"),
		IconShow:        true,
		IconColor:       mustColor("auto"),
		IconBgColor:     mustColor("green"),
		LabelShow:       true,
		LabelColor:      mustColor("green"),
		LabelMaxLength:  0,
		ButtonBgColor:   mustColor("bg-surface-elevated"),
		LeftClick:       ParseClickAction("lys-shell idle toggle --indefinite"),
		RightClick:      ParseClickAction("lys-shell idle toggle"),
		MiddleClick:     ClickAction{},
		ScrollUp:        ClickAction{},
		ScrollDown:      ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c IdleInhibitConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}

// IdleInhibitDuration is a timed inhibition's length, refused at load
// above the longest the service counts down rather than clamped: its
// seconds must fit the service's uint32 timer.
//
// Idle inhibit duration in whole minutes, 0 for indefinite.
type IdleInhibitDuration struct{ duration idleinhibit.Duration }

// parseIdleInhibitDuration validates minutes as a duration.
func parseIdleInhibitDuration(minutes int64) (IdleInhibitDuration, error) {
	d, err := idleinhibit.NewDuration(minutes)
	if err != nil {
		return IdleInhibitDuration{}, err
	}
	return IdleInhibitDuration{duration: d}, nil
}

// mustIdleInhibitDuration builds a schema-default duration; a bad
// literal panics at init.
func mustIdleInhibitDuration(minutes int64) IdleInhibitDuration {
	d, err := parseIdleInhibitDuration(minutes)
	if err != nil {
		panic(err)
	}
	return d
}

// Duration is the validated duration.
func (d IdleInhibitDuration) Duration() idleinhibit.Duration { return d.duration }

// UnmarshalConfig implements Unmarshaler.
func (d *IdleInhibitDuration) UnmarshalConfig(v any) error {
	var minutes int64
	if err := decodeInto(reflect.ValueOf(&minutes).Elem(), v); err != nil {
		return err
	}
	parsed, err := parseIdleInhibitDuration(minutes)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (d IdleInhibitDuration) MarshalConfig() any { return int64(d.duration.Minutes()) }

func (IdleInhibitDuration) configSchema(*schemaGen) Schema {
	return Schema{
		"description": typeDoc(typeOf[IdleInhibitDuration]()),
		"type":        "integer",
		"format":      "uint32",
		"minimum":     int64(0),
		"maximum":     int64(idleinhibit.MaxMinutes),
	}
}
