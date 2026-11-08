package config

// DockEdge is the screen edge the dock sits on.
//
// Screen edge the dock sits on.
type DockEdge string

// Dock edges.
const (
	// Bottom edge of the screen.
	DockEdgeBottom DockEdge = "bottom"
	// Left edge of the screen.
	DockEdgeLeft DockEdge = "left"
	// Right edge of the screen.
	DockEdgeRight DockEdge = "right"
)

var _ = registerEnum(DockEdgeBottom, DockEdgeLeft, DockEdgeRight)

// IsVertical reports whether the dock runs down a side of the screen.
func (e DockEdge) IsVertical() bool { return e != DockEdgeBottom }

// DockConfig is the `dock` section.
//
// A dock of pinned apps and open windows, on its own surface beside the bar.
//
// Pinned apps launch on click. Open windows show under their app's icon
// with a running indicator, dimmed while all of them are minimized; a click
// focuses the app's window, bringing a minimized one back, and cycles
// through its windows. Windows minimize into their dock icon. Works on any
// compositor with the wlr foreign-toplevel protocol; hiding when a window
// covers the dock needs lys-wm.
type DockConfig struct {
	// Show the dock.
	Enabled bool `cfg:"enabled"`
	// Outputs that show the dock, by connector name (e.g. `"DP-1"`), or
	// `"*"` for every output.
	Monitors []string `cfg:"monitors"`
	// Screen edge the dock sits on.
	Edge DockEdge `cfg:"edge"`
	// Size of each app's icon. Accepts a scale multiplier or pixels (e.g. `"48px"`).
	IconSize Size `cfg:"icon-size"`
	// Hide the dock while a window covers it, revealing it when the
	// pointer reaches its edge. When on, the dock reserves no space. Seeing
	// which windows cover it needs lys-wm; elsewhere the dock shows only
	// while the pointer is on it.
	AutoHide bool `cfg:"auto-hide"`
	// How long an auto-hiding dock stays after the pointer leaves it, or
	// after a window comes to cover it, before it hides (milliseconds).
	AutoHideDelayMs uint32 `cfg:"auto-hide-delay-ms"`
	// Grow the icon under the pointer, and its neighbors halfway, so the
	// hovered app stands out.
	HoverZoom bool `cfg:"hover-zoom"`
	// Size the hovered app's icon grows to. Accepts a scale multiplier or
	// pixels (e.g. `"72px"`); one no larger than `icon-size` leaves it as is.
	HoverZoomSize Size `cfg:"hover-zoom-size"`
	// Apps always on the dock, in order, by desktop entry id without
	// `.desktop` (e.g. `"firefox"`, `"org.gnome.Nautilus"`), the app id
	// their windows report.
	Pinned []string `cfg:"pinned"`
	// Icon for an app without a desktop entry icon.
	Icon string `cfg:"icon"`
}

// DefaultsDock returns the schema defaults.
func DefaultsDock() DockConfig {
	return DockConfig{
		Enabled:         false,
		Monitors:        []string{"*"},
		Edge:            DockEdgeBottom,
		IconSize:        Scale(2),
		AutoHide:        false,
		AutoHideDelayMs: defaultAutoHideDelayMs,
		HoverZoom:       true,
		HoverZoomSize:   Scale(3),
		Pinned:          []string{},
		Icon:            "ld-app-window-symbolic",
	}
}

// ShowsOn reports whether the dock shows on the output named connector.
func (c DockConfig) ShowsOn(connector string) bool {
	if !c.Enabled {
		return false
	}
	for _, m := range c.Monitors {
		if m == "*" || m == connector {
			return true
		}
	}
	return false
}
