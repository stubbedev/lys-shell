package config

// WindowScope is which outputs' windows a window list shows.
//
// Which outputs' windows the module lists.
type WindowScope string

// Window scopes.
const (
	// Only the windows on the bar's own output.
	WindowScopeOutput WindowScope = "output"
	// The windows on every output.
	WindowScopeAll WindowScope = "all"
)

var _ = registerEnum(WindowScopeOutput, WindowScopeAll)

// TaskIconBaseRem is the rem an icon-size multiplier of 1 resolves
// against for the minimized-windows module's and the dock's icons.
const TaskIconBaseRem = 1.3

// MinimizedWindowsConfig is the minimized-windows module.
//
// Minimized windows, like a dock.
//
// Lists each minimized window as its app's icon, in the order the windows
// were minimized, with the window title as tooltip. A click restores and
// focuses the window, a middle click closes it, and a right click offers
// both. Windows minimize into their icon and come back out of it. Works
// on any compositor with the wlr foreign-toplevel protocol, lys-wm among
// them.
type MinimizedWindowsConfig struct {
	// Icon for a window whose app has no desktop entry icon, and the
	// module's own icon while nothing is minimized under show-when-empty.
	Icon string `cfg:"icon"`
	// Size of each window's icon. Accepts a scale multiplier or pixels (e.g. `"24px"`).
	IconSize Size `cfg:"icon-size"`
	// Keep the module on the bar, showing its icon, while nothing is
	// minimized.
	ShowWhenEmpty bool `cfg:"show-when-empty"`
	// Which outputs' minimized windows the module lists.
	Scope WindowScope `cfg:"scope"`
}

// DefaultsMinimizedWindows returns the schema defaults.
func DefaultsMinimizedWindows() MinimizedWindowsConfig {
	return MinimizedWindowsConfig{
		Icon:          "ld-app-window-symbolic",
		IconSize:      Scale(1),
		ShowWhenEmpty: false,
		Scope:         WindowScopeOutput,
	}
}
