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
	// ## Placeholders
	//
	// - `{{ status }}` - Filter status text (On, Off)
	// - `{{ temp }}` - Current temperature in Kelvin (shows "--" when disabled)
	// - `{{ gamma }}` - Current gamma percentage (shows "--" when disabled)
	// - `{{ config_temp }}` - Configured temperature (always available)
	// - `{{ config_gamma }}` - Configured gamma (always available)
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
	// The schedule anchors to civil dawn and civil dusk, when the sun is 6
	// degrees below the horizon, with each transition centred on the event.
	// Where the sun never crosses that line (polar day and polar night) the
	// filter stays off or on with no transition.
	//
	// The location comes from GeoClue, unless `latitude` and `longitude` are
	// both set: explicit coordinates win, and GeoClue is then never asked. A
	// spelled-out 0, 0 counts as unset. The last GeoClue fix is remembered in
	// the state directory. With no location at all, or with only one coordinate
	// or one off the globe, the schedule does not run: the module says why in
	// its tooltip and in the log, and the filter stays as it is.
	//
	// GeoClue answers a client only once an agent serves the user, and only
	// GNOME and a few other desktops run one. With none running, lys-shell
	// registers as the agent itself for the length of the lookup, which GeoClue
	// allows only for names on its whitelist. The deb and rpm install that as
	// `/etc/geoclue/conf.d/50-lys-shell.conf`, and the NixOS module's
	// `autoInstallDependencies` sets it up too. From the release archive, install
	// the archive's `geoclue-lys-shell.conf` at that path yourself and run
	// `sudo systemctl restart geoclue`. GeoClue older than 2.7 does not read
	// `conf.d`: put the file's `lys-shell` entry in `/etc/geoclue/geoclue.conf`
	// and add `lys-shell` to `whitelist` under `[agent]` there. A `whitelist` in a
	// drop-in replaces the main file's, so the shipped file repeats upstream's
	// list; if you have changed yours, add your agents to the drop-in.
	//
	// The log names what to check when the lookup fails. No answer usually means
	// the whitelist above. No fix after that usually means GeoClue has no source
	// that can place the machine, as upstream's default config names no Wi-Fi
	// location service: set `url` under `[wifi]`, describe the location in
	// `/etc/geolocation` for the static source, or set `latitude` and `longitude`.
	//
	// A manual click cancels a running transition, applies at once, and holds
	// until the next transition begins, after which the schedule resumes.
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
