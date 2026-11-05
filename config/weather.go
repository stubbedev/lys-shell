package config

// WeatherConfig is ported from crates/wayle-config/src/schemas/modules/weather/mod.rs.
//
// Current conditions with hourly and daily forecasts in a dropdown.
type WeatherConfig struct {
	// Weather data provider.
	Provider WeatherProvider `cfg:"provider"`
	// Location for weather data (city name or "lat,lon" coordinates).
	Location string `cfg:"location"`
	// Temperature unit.
	Units TemperatureUnit `cfg:"units"`
	// Format string for the label.
	//
	// ## Examples
	//
	// - `"{{ temp }}{{ temp_unit }}"` - "22°C"
	// - `"{{ temp }}{{ temp_unit }} {{ condition }}"` - "22°C Partly Cloudy"
	// - `"{{ temp }}{{ temp_unit }} H:{{ high }} L:{{ low }}"` - "22°C H:25 L:18"
	Format string `cfg:"format"`
	// Time display format for sunrise/sunset and hourly forecast.
	TimeFormat TimeFormat `cfg:"time-format"`
	// Polling interval in seconds.
	RefreshIntervalSeconds uint32 `cfg:"refresh-interval-seconds"`
	// Visual Crossing API key. Supports `$VAR_NAME` syntax to reference
	// environment variables from `.*.env` files in the config directory.
	VisualCrossingKey *string `cfg:"visual-crossing-key,type=Secret"`
	// WeatherAPI.com API key. Supports `$VAR_NAME` syntax to reference
	// environment variables from `.*.env` files in the config directory.
	WeatherapiKey *string `cfg:"weatherapi-key,type=Secret"`
	// Fallback icon for weather.
	IconName string `cfg:"icon-name"`
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
	// Display temperature label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
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
}

// DefaultsWeather returns the schema defaults.
func DefaultsWeather() WeatherConfig {
	return WeatherConfig{
		Provider:               WeatherProviderOpenMeteo,
		Location:               "San Francisco",
		Units:                  TemperatureUnitMetric,
		Format:                 "{{ temp }}{{ temp_unit }}",
		TimeFormat:             TimeFormat12h,
		RefreshIntervalSeconds: 1800,
		IconName:               "ld-sun-symbolic",
		BorderShow:             false,
		BorderColor:            mustColor("border-accent"),
		IconShow:               true,
		IconColor:              mustColor("auto"),
		IconBgColor:            mustColor("accent"),
		LabelShow:              true,
		LabelColor:             mustColor("fg-default"),
		LabelMaxLength:         0,
		ButtonBgColor:          mustColor("bg-surface-elevated"),
		LeftClick:              ParseClickAction("dropdown:weather"),
		RightClick:             ClickAction{},
		MiddleClick:            ClickAction{},
		ScrollUp:               ClickAction{},
		ScrollDown:             ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c WeatherConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}

// WeatherProvider is ported from crates/wayle-config/src/schemas/modules/weather/mod.rs.
//
// Weather data provider selection.
type WeatherProvider string

// WeatherProvider values.
const (
	// Open-Meteo (no API key required).
	WeatherProviderOpenMeteo WeatherProvider = "open-meteo"
	// Visual Crossing (requires API key).
	WeatherProviderVisualCrossing WeatherProvider = "visual-crossing"
	// WeatherAPI.com (requires API key).
	WeatherProviderWeatherApi WeatherProvider = "weather-api"
)

var _ = registerEnum(WeatherProviderOpenMeteo, WeatherProviderVisualCrossing, WeatherProviderWeatherApi)

// TemperatureUnit is ported from crates/wayle-config/src/schemas/modules/weather/mod.rs.
//
// Temperature unit for display.
type TemperatureUnit string

// TemperatureUnit values.
const (
	// Celsius (metric).
	TemperatureUnitMetric TemperatureUnit = "metric"
	// Fahrenheit (imperial).
	TemperatureUnitImperial TemperatureUnit = "imperial"
)

var _ = registerEnum(TemperatureUnitMetric, TemperatureUnitImperial)
