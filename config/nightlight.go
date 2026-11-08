package config

import (
	"errors"

	"github.com/stubbedev/lys-shell/service/nightlight"
)

// NightlightConfig is the night light: a blue-light filter lys-shell
// applies itself, through the compositor's colour transform matrix
// (hyprland-ctm-control-v1) or gamma ramps (wlr-gamma-control-v1).
//
// Night light: warms the display colours, by hand or on a dawn/dusk schedule.
type NightlightConfig struct {
	// Format string for the label.
	//
	// ## Examples
	//
	// - `"{{ status }}"` - "On"
	// - `"{{ temp }}K {{ gamma }}%"` - "4500K 80%"
	// - `"{{ status }} ({{ temp }}K)"` - "On (4500K)"
	Format string `cfg:"format"`
	// Color temperature in Kelvin when filter is enabled. Range: 1000-20000.
	Temperature uint32 `cfg:"temperature"`
	// Display gamma percentage when filter is enabled. Range: 0-200.
	Gamma uint32 `cfg:"gamma"`
	// Automatically ease the filter on at dusk and off at dawn, following the
	// sun at the machine's location.
	//
	// Transitions are centred on civil dawn and dusk (sun 6 degrees below the
	// horizon). During polar day or night the filter stays off or on.
	//
	// The location is `latitude` and `longitude` when both are set (0, 0
	// counts as unset), otherwise GeoClue's, whose last fix is kept in the
	// state directory. Without a usable location the schedule does not run;
	// the tooltip and the log say why.
	//
	// When no GeoClue agent runs, lys-shell registers as one during the
	// lookup, which needs it on GeoClue's agent whitelist. The deb and rpm
	// install `/etc/geoclue/conf.d/50-lys-shell.conf`, and the NixOS module's
	// `autoInstallDependencies` sets it up. From the tarball, install its
	// `geoclue-lys-shell.conf` at that path and restart geoclue. GeoClue
	// before 2.7 reads no `conf.d`: add the file's `lys-shell` entry to
	// `/etc/geoclue/geoclue.conf` and `lys-shell` to `whitelist` under
	// `[agent]`. A drop-in `whitelist` replaces the main file's, so the
	// shipped one repeats upstream's list; add any agents you rely on.
	//
	// When GeoClue answers but finds no fix, it has no source that can place
	// the machine: set `url` under `[wifi]`, describe the location in
	// `/etc/geolocation`, or set `latitude` and `longitude`.
	//
	// A manual click cancels a running transition, applies at once, and holds
	// until the next transition begins.
	AutoSchedule bool `cfg:"auto-schedule"`
	// Length of the transition between day and night, in whole minutes, centred
	// on civil dawn or dusk. 0 switches at once. Range: 0-120.
	TransitionDuration NightlightTransition `cfg:"transition-duration"`
	// Latitude for the schedule, in decimal degrees (north positive). Range: -90
	// to 90. Unset uses GeoClue. Set together with `longitude`, it overrides
	// GeoClue. Only used when `auto-schedule` is on.
	Latitude *float64 `cfg:"latitude"`
	// Longitude for the schedule, in decimal degrees (east positive). Range:
	// -180 to 180. Unset uses GeoClue. Set together with `latitude`, it overrides
	// GeoClue. Only used when `auto-schedule` is on.
	Longitude *float64 `cfg:"longitude"`
	// Icon when filter is disabled (showing normal daylight colors).
	IconOff string `cfg:"icon-off"`
	// Icon when filter is enabled (showing warm night colors).
	IconOn string `cfg:"icon-on"`
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
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Default toggles blue light filter.
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

// DefaultsNightlight returns the schema defaults.
func DefaultsNightlight() NightlightConfig {
	return NightlightConfig{
		Format:             "{{ status }}",
		Temperature:        5000,
		Gamma:              100,
		AutoSchedule:       false,
		TransitionDuration: mustNightlightTransition(15),
		IconOff:            "ld-sun-symbolic",
		IconOn:             "ld-moon-symbolic",
		BorderShow:         false,
		BorderColor:        mustColor("yellow"),
		IconShow:           true,
		Tooltip:            true,
		IconColor:          mustColor("auto"),
		IconBgColor:        mustColor("yellow"),
		LabelShow:          true,
		LabelColor:         mustColor("fg-default"),
		LabelMaxLength:     0,
		ButtonBgColor:      mustColor("bg-surface-elevated"),
		LeftClick:          ParseClickAction(":toggle"),
		RightClick:         ClickAction{},
		MiddleClick:        ClickAction{},
		ScrollUp:           ClickAction{},
		ScrollDown:         ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c NightlightConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}

// PinnedLocation is the position latitude and longitude name: false with
// neither set, and an error with only one set, off the globe, or at 0, 0
// (nightlight.ErrNullIsland), which older default configs still spell
// out.
func (c NightlightConfig) PinnedLocation() (nightlight.Location, bool, error) {
	if c.Latitude == nil && c.Longitude == nil {
		return nightlight.Location{}, false, nil
	}
	if c.Latitude == nil || c.Longitude == nil {
		return nightlight.Location{}, false, errors.New("nightlight: set both latitude and longitude, or neither")
	}
	loc, err := nightlight.NewLocation(*c.Latitude, *c.Longitude)
	if err != nil {
		return nightlight.Location{}, false, err
	}
	return loc, true, nil
}

// NightlightTransition is the ease's length, refused at load outside
// its range rather than clamped.
//
// Night light transition length in whole minutes, 0 for an instant switch.
type NightlightTransition struct{ transition nightlight.Transition }

// mustNightlightTransition builds a schema-default length; a bad literal
// panics at init.
func mustNightlightTransition(minutes int64) NightlightTransition {
	t, err := nightlight.NewTransition(minutes)
	if err != nil {
		panic(err)
	}
	return NightlightTransition{transition: t}
}

// Transition is the validated length.
func (t NightlightTransition) Transition() nightlight.Transition { return t.transition }

// UnmarshalConfig implements Unmarshaler.
func (t *NightlightTransition) UnmarshalConfig(v any) error {
	minutes, err := decodeInteger[int64](v)
	if err != nil {
		return err
	}
	parsed, err := nightlight.NewTransition(minutes)
	if err != nil {
		return err
	}
	t.transition = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (t NightlightTransition) MarshalConfig() any { return int64(t.transition.Minutes()) }

func (NightlightTransition) configSchema(*schemaGen) Schema {
	return uint32RangeSchema(typeOf[NightlightTransition](), 0, nightlight.MaxTransitionMinutes)
}
