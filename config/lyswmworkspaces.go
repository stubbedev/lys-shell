package config

// LysWMWorkspacesConfig is the lys-wm module: lys-wm's numbered
// workspaces, per output, with click-to-switch through lys-wm's IPC.
//
// lys-wm workspace indicators with click-to-switch.
type LysWMWorkspacesConfig struct {
	// Show only workspaces on this bar's monitor.
	//
	// When `true` (default), each bar shows only its own output's
	// workspaces. When `false`, all workspaces from every output are shown.
	MonitorSpecific bool `cfg:"monitor-specific"`
	// What identifies each workspace button.
	//
	// - `label` (default): show the workspace number
	// - `icon`: show an icon from `workspace-map` (falls back to label if unmapped)
	// - `none`: show nothing — only app icons visible (if enabled)
	DisplayMode DisplayMode `cfg:"display-mode"`
	// Pulse animation on workspaces with urgent windows.
	UrgentShow bool `cfg:"urgent-show"`
	// Where the urgent pulse is applied.
	//
	// - `workspace` (default): whole button pulses
	// - `application`: only the urgent app icon pulses, falling back to
	//   `workspace` when app icons are disabled
	UrgentMode UrgentMode `cfg:"urgent-mode"`
	// Visual indicator for the active workspace.
	ActiveIndicator ActiveIndicator `cfg:"active-indicator"`
	// Text separator between workspace identity and app icons.
	Divider string `cfg:"divider"`
	// Show application icons for windows on each workspace.
	AppIconsShow bool `cfg:"app-icons-show"`
	// Deduplicate application icons within a workspace.
	//
	// When `true`, one icon per unique `app_id`. When `false`, one icon
	// per window.
	AppIconsDedupe bool `cfg:"app-icons-dedupe"`
	// Fallback icon for applications not matched by `app-icon-map`.
	AppIconsFallback string `cfg:"app-icons-fallback"`
	// Icon shown when a workspace has no application windows.
	AppIconsEmpty string `cfg:"app-icons-empty"`
	// Gap between app icons within a workspace button. Accepts a scale multiplier or pixels (e.g. `"4px"`).
	IconGap Size `cfg:"icon-gap"`
	// Padding for workspace content along the bar direction. Accepts a scale multiplier or pixels (e.g. `"8px"`).
	WorkspacePadding Size `cfg:"workspace-padding"`
	// Workspace icon size. Accepts a scale multiplier or pixels (e.g. `"16px"`).
	//
	// Applies to identity icons and custom icons from `workspace-map`.
	IconSize Size `cfg:"icon-size"`
	// Workspace label and divider size. Accepts a scale multiplier or pixels (e.g. `"16px"`).
	LabelSize Size `cfg:"label-size"`
	// Workspaces to hide from the display.
	//
	// Glob patterns matched against the workspace number. Example:
	// - `"1?"` — hide numbers 10-19
	WorkspaceIgnore []string `cfg:"workspace-ignore"`
	// Color for the active (visible on its output) workspace.
	//
	// In `background` indicator mode, also used as the button background.
	ActiveColor ColorValue `cfg:"active-color"`
	// Color for occupied workspaces (have windows but not active).
	OccupiedColor ColorValue `cfg:"occupied-color"`
	// Color for empty workspaces and placeholder slots.
	EmptyColor ColorValue `cfg:"empty-color"`
	// Background color for the workspaces container.
	ContainerBgColor ColorValue `cfg:"container-bg-color"`
	// Display border around the workspaces container.
	BorderShow bool `cfg:"border-show"`
	// Border color for the workspaces container.
	BorderColor ColorValue `cfg:"border-color"`
	// Per-workspace icon and color overrides, keyed by workspace number.
	//
	// ## Example
	//
	// ```yaml
	// modules:
	//   lys-wm-workspaces:
	//     workspace-map:
	//       "1": {icon: ld-globe-symbolic, color: "#4a90d9"}
	//       "2": {icon: ld-terminal-symbolic}
	// ```
	WorkspaceMap NamedWorkspaceMap `cfg:"workspace-map"`
	// Application icon mapping with glob pattern support.
	//
	// Maps window `app_id` or title to symbolic icon names. Supports:
	// - No prefix: matches `app_id` (e.g. `"*firefox*"`)
	// - `app:` prefix: explicit `app_id` match (e.g. `"app:org.mozilla.*"`)
	// - `title:` prefix: matches window title (e.g. `"title:*YouTube*"`)
	//
	// ## Example
	//
	// ```yaml
	// modules:
	//   lys-wm-workspaces:
	//     app-icon-map:
	//       "*firefox*": ld-globe-symbolic
	//       "title:*YouTube*": ld-youtube-symbolic
	// ```
	AppIconMap map[string]string `cfg:"app-icon-map"`
	// Action on left click.
	LeftClick WorkspaceClickAction `cfg:"left-click"`
	// Action on middle click.
	MiddleClick WorkspaceClickAction `cfg:"middle-click"`
	// Action on right click.
	RightClick WorkspaceClickAction `cfg:"right-click"`
	// Action on scroll up.
	ScrollUp WorkspaceClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown WorkspaceClickAction `cfg:"scroll-down"`
}

// DefaultsLysWMWorkspaces returns the schema defaults.
func DefaultsLysWMWorkspaces() LysWMWorkspacesConfig {
	return LysWMWorkspacesConfig{
		MonitorSpecific:  true,
		DisplayMode:      DisplayModeLabel,
		UrgentShow:       true,
		UrgentMode:       UrgentWorkspace,
		ActiveIndicator:  ActiveBackground,
		Divider:          " ",
		AppIconsShow:     false,
		AppIconsDedupe:   true,
		AppIconsFallback: "ld-app-window-symbolic",
		AppIconsEmpty:    "ld-minus-symbolic",
		IconGap:          Size{Value: 0.3, Unit: SizeMultiplier},
		WorkspacePadding: Size{Value: 0.5, Unit: SizeMultiplier},
		IconSize:         Size{Value: 1, Unit: SizeMultiplier},
		LabelSize:        Size{Value: 1, Unit: SizeMultiplier},
		WorkspaceIgnore:  []string{},
		ActiveColor:      mustColor("accent"),
		OccupiedColor:    mustColor("fg-muted"),
		EmptyColor:       mustColor("fg-subtle"),
		ContainerBgColor: mustColor("bg-surface-elevated"),
		BorderShow:       false,
		BorderColor:      mustColor("border-default"),
		WorkspaceMap:     NamedWorkspaceMap{},
		AppIconMap:       map[string]string{},
		LeftClick:        ParseWorkspaceClickAction("focus:this"),
		MiddleClick:      ParseWorkspaceClickAction(""),
		RightClick:       ParseWorkspaceClickAction(""),
		ScrollUp:         ParseWorkspaceClickAction("focus:previous"),
		ScrollDown:       ParseWorkspaceClickAction("focus:next"),
	}
}

// Clicks returns the five input bindings.
func (c LysWMWorkspacesConfig) Clicks() WorkspaceClicks {
	return WorkspaceClicks{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
