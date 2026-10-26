package config

import "math"

// DropdownsConfig is the [dropdowns] section
// (crates/wayle-config/src/schemas/dropdowns/mod.rs).
//
// Per-dropdown foldout panel sizing.
//
// Each field overrides the size of one bar widget dropdown. Unset fields keep
// the built-in default (scaled by the global scale).
type DropdownsConfig struct {
	// Audio dropdown panel size.
	Audio DropdownSize `cfg:"audio"`
	// Battery dropdown panel size.
	Battery DropdownSize `cfg:"battery"`
	// Bitwarden dropdown panel size.
	Bitwarden DropdownSize `cfg:"bitwarden"`
	// Bluetooth dropdown panel size.
	Bluetooth DropdownSize `cfg:"bluetooth"`
	// Brightness dropdown panel size.
	Brightness DropdownSize `cfg:"brightness"`
	// Calendar dropdown panel size.
	Calendar DropdownSize `cfg:"calendar"`
	// Dashboard dropdown panel size.
	Dashboard DropdownSize `cfg:"dashboard"`
	// Mail dropdown panel size.
	Mail DropdownSize `cfg:"mail"`
	// Media dropdown panel size.
	Media DropdownSize `cfg:"media"`
	// Network dropdown panel size.
	Network DropdownSize `cfg:"network"`
	// Notification dropdown panel size.
	Notification DropdownSize `cfg:"notification"`
	// Treeman dropdown panel size.
	Treeman DropdownSize `cfg:"treeman"`
	// Weather dropdown panel size.
	Weather DropdownSize `cfg:"weather"`
}

// ByName returns the size override of the named dropdown (the
// dropdown:<name> identifiers); ok is false for a dropdown without a
// sizing key.
func (d DropdownsConfig) ByName(name string) (DropdownSize, bool) {
	switch name {
	case "audio":
		return d.Audio, true
	case "battery":
		return d.Battery, true
	case "bitwarden":
		return d.Bitwarden, true
	case "bluetooth":
		return d.Bluetooth, true
	case "brightness":
		return d.Brightness, true
	case "calendar":
		return d.Calendar, true
	case "dashboard":
		return d.Dashboard, true
	case "mail":
		return d.Mail, true
	case "media":
		return d.Media, true
	case "network":
		return d.Network, true
	case "notification":
		return d.Notification, true
	case "treeman":
		return d.Treeman, true
	case "weather":
		return d.Weather, true
	}
	return DropdownSize{}, false
}

// DropdownSize is one panel's size override
// (crates/wayle-config/src/schemas/dropdowns/types.rs).
//
// Size override for a dropdown foldout panel.
//
// Each field is optional: when unset the dropdown keeps its built-in default
// width (scaled by the global scale) and the height of its content. The
// width is a [`Size`]: a scale multiplier of the built-in base (e.g. `1.5`)
// or an absolute pixel length (e.g. `"480px"`). The height is a pixel
// length only.
type DropdownSize struct {
	// Panel width override. Unset uses the built-in default width.
	Width *Size `cfg:"width"`
	// Panel height cap in pixels (e.g. `"400px"`): the height a panel grows
	// to before its lists scroll. Shorter content keeps the panel at its own
	// height. Unset grows the panel to its content, up to the monitor. A
	// multiplier is an error: the height has no built-in base to scale.
	Height *PixelLength `cfg:"height"`
}

func (DropdownSize) configValue() {}

// ResolveDimension applies an optional override to a built-in base
// size in pixels: unset scales the base, a multiplier scales it
// further, pixels replace it (wayle-shell-core bar::resolve_dimension).
func ResolveDimension(override *Size, base, scale float64) int {
	if override == nil {
		return int(math.Round(base * scale))
	}
	return int(math.Round(override.ResolvePx(base, scale)))
}

// ResolveContentHeight is the height of a dropdown that sizes to its
// content: the override's pixels, or -1 (natural size) without one.
func ResolveContentHeight(override *PixelLength) int {
	if override == nil {
		return -1
	}
	return int(math.Round(float64(*override)))
}
