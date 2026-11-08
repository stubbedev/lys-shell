package config

// BarConfig is the `bar` section
// (crates/wayle-config/src/schemas/bar/mod.rs).
//
// Bar chrome: per-monitor layout, spacing, colors, and button styling.
type BarConfig struct {
	// Per-monitor bar layouts. Each entry targets a monitor by connector name
	// (e.g., `"DP-1"`) or `"*"` for all monitors, and may inherit from another
	// entry with `extends`.
	//
	// ## Example
	//
	// ```yaml
	// bar:
	//   layout:
	//     - monitor: "*"
	//       left: [dashboard]
	//       center: [clock]
	//       right: [battery, network, volume, systray]
	//     - monitor: HDMI-1
	//       extends: "*"
	//       right: [volume, systray]
	// ```
	Layout []BarLayout `cfg:"layout"`
	// Bar-specific scale multiplier for spacing, radius, and other bar elements.
	Scale ScaleFactor `cfg:"scale"`
	// Gap between bar and its attached screen edge.
	//
	// - **Orientation**: Distance from top (horizontal bar) or left (vertical bar)
	InsetEdge Size `cfg:"inset-edge"`
	// Gap at the bar's ends.
	//
	// - **Orientation**: Left/right (horizontal bar), top/bottom (vertical bar)
	InsetEnds Size `cfg:"inset-ends"`
	// Internal spacing along bar thickness.
	//
	// - **Orientation**: Top/bottom (horizontal bar), left/right (vertical bar)
	Padding Size `cfg:"padding"`
	// Internal spacing at bar ends.
	//
	// - **Orientation**: Left/right (horizontal bar), top/bottom (vertical bar)
	PaddingEnds Size `cfg:"padding-ends"`
	// Gap between modules and groups on the bar.
	ModuleGap Size `cfg:"module-gap"`
	// Bar position on screen edge.
	Location Location `cfg:"location"`
	// Reserve screen space for the bar.
	//
	// When disabled, windows may overlap the bar and the bar draws over them.
	// An auto-hiding bar never reserves space.
	Exclusive bool `cfg:"exclusive"`
	// When the bar hides, leaving a thin strip at its edge that reveals it
	// under the pointer. A layout entry's `auto-hide` overrides it for its
	// monitors.
	AutoHide BarAutoHide `cfg:"auto-hide"`
	// How long the bar stays after the pointer leaves it, or after it stops
	// being needed, before it hides (milliseconds).
	AutoHideDelayMs uint32 `cfg:"auto-hide-delay-ms"`
	// Layer-shell layer the bar is placed on.
	Layer Layer `cfg:"layer"`
	// Bar background color.
	BG ColorValue `cfg:"bg"`
	// Bar background opacity (0-100). Below 100 the compositor blurs what is
	// behind the bar; where it offers no blur the bar rests on its opaque color.
	BackgroundOpacity Percentage `cfg:"background-opacity"`
	// Border placement for bar.
	BorderLocation BorderLocation `cfg:"border-location"`
	// Border width for bar (pixels).
	BorderWidth uint8 `cfg:"border-width"`
	// Border color for the bar.
	BorderColor ColorValue `cfg:"border-color"`
	// Corner rounding level for the bar.
	Rounding RoundingLevel `cfg:"rounding"`
	// Shadow style for the bar.
	Shadow ShadowPreset `cfg:"shadow"`
	// Visual style variant for bar buttons.
	ButtonVariant BarButtonVariant `cfg:"button-variant"`
	// Button opacity (0-100).
	ButtonOpacity Percentage `cfg:"button-opacity"`
	// Button background opacity (0-100).
	ButtonBGOpacity Percentage `cfg:"button-bg-opacity"`
	// Button icon size. Accepts a scale multiplier or pixels (e.g. `"24px"`).
	ButtonIconSize Size `cfg:"button-icon-size"`
	// Button icon container padding. Only applies to `block-prefix` and `icon-square` variants.
	// Accepts a scale multiplier or pixels (e.g. `"8px"`).
	ButtonIconPadding Size `cfg:"button-icon-padding"`
	// Button label text size. Accepts a scale multiplier or pixels (e.g. `"16px"`).
	ButtonLabelSize Size `cfg:"button-label-size"`
	// Button label font weight.
	ButtonLabelWeight FontWeightClass `cfg:"button-label-weight"`
	// Button label container padding. Accepts a scale multiplier or pixels (e.g. `"8px"`).
	ButtonLabelPadding Size `cfg:"button-label-padding"`
	// Corner rounding level for the buttons in the bar.
	ButtonRounding RoundingLevel `cfg:"button-rounding"`
	// Gap between button icon and label. Accepts a scale multiplier or pixels (e.g. `"4px"`).
	ButtonGap Size `cfg:"button-gap"`
	// Icon position relative to label in bar buttons.
	ButtonIconPosition IconPosition `cfg:"button-icon-position"`
	// Border placement for bar buttons.
	ButtonBorderLocation BorderLocation `cfg:"button-border-location"`
	// Border width for bar buttons (pixels).
	ButtonBorderWidth uint8 `cfg:"button-border-width"`
	// Border placement for button groups.
	ButtonGroupBorderLocation BorderLocation `cfg:"button-group-border-location"`
	// Border width for button groups (pixels).
	ButtonGroupBorderWidth uint8 `cfg:"button-group-border-width"`
	// Internal padding for button groups.
	ButtonGroupPadding Size `cfg:"button-group-padding"`
	// Gap between modules within a group.
	ButtonGroupModuleGap Size `cfg:"button-group-module-gap"`
	// Background color for button groups.
	ButtonGroupBackground ColorValue `cfg:"button-group-background"`
	// Button group opacity (0-100).
	ButtonGroupOpacity Percentage `cfg:"button-group-opacity"`
	// Border color for button groups.
	ButtonGroupBorderColor ColorValue `cfg:"button-group-border-color"`
	// Corner rounding level for button groups.
	ButtonGroupRounding RoundingLevel `cfg:"button-group-rounding"`
	// Enable dropdown panel shadow.
	DropdownShadow bool `cfg:"dropdown-shadow"`
	// Dropdown panel opacity (0-100). Below 100 the compositor blurs what is
	// behind the panel; where it offers no blur the panel is opaque.
	DropdownOpacity Percentage `cfg:"dropdown-opacity"`
	// Close dropdown when clicking outside it.
	//
	// Esc closes an open dropdown too. On lys-wm it does so beside the
	// dropdown, with the keys still on the focused window, and the window
	// sees neither the press nor the release, unless a bind on bare Esc, a
	// window inhibiting shortcuts, an exclusive layer or a grab has the key.
	// Elsewhere Esc reaches a dropdown only after a click into it, as keys
	// stay with the focused window until then.
	DropdownAutohide bool `cfg:"dropdown-autohide"`
	// Freeze the bar button label while its dropdown is open.
	//
	// Prevents the button from resizing mid-interaction, which keeps the
	// dropdown anchored in place.
	DropdownFreezeLabel bool `cfg:"dropdown-freeze-label"`
}

// DefaultsBar returns the schema defaults: PlainBar made a floating pane of
// glass whose buttons are ink on the glass.
func DefaultsBar() BarConfig {
	b := PlainBar()
	b.InsetEdge, b.InsetEnds = Scale(0.5), Scale(0.5)
	b.BackgroundOpacity = 60
	b.BorderLocation, b.BorderColor = BorderAll, mustColor("border-default")
	b.Rounding = RoundingDefault
	b.ButtonVariant, b.ButtonBGOpacity = ButtonBasic, 0
	return b
}

// PlainBar is a flat opaque bar along its edge, with colored block buttons.
func PlainBar() BarConfig {
	return BarConfig{
		Layout:                    []BarLayout{DefaultBarLayout()},
		Scale:                     1,
		InsetEdge:                 Scale(0),
		InsetEnds:                 Scale(0),
		Padding:                   Scale(0.35),
		PaddingEnds:               Scale(0.5),
		ModuleGap:                 Scale(0.5),
		Location:                  LocationTop,
		Exclusive:                 true,
		AutoHide:                  AutoHideNever,
		AutoHideDelayMs:           defaultAutoHideDelayMs,
		Layer:                     LayerTop,
		BG:                        mustColor("bg-surface"),
		BackgroundOpacity:         100,
		BorderLocation:            BorderNone,
		BorderWidth:               1,
		BorderColor:               mustColor("border-accent"),
		Rounding:                  RoundingNone,
		Shadow:                    ShadowNone,
		ButtonVariant:             ButtonBlockPrefix,
		ButtonOpacity:             100,
		ButtonBGOpacity:           100,
		ButtonIconSize:            Scale(1),
		ButtonIconPadding:         Scale(1),
		ButtonLabelSize:           Scale(1),
		ButtonLabelWeight:         WeightSemibold,
		ButtonLabelPadding:        Scale(1),
		ButtonRounding:            RoundingSm,
		ButtonGap:                 Scale(1),
		ButtonIconPosition:        IconStart,
		ButtonBorderLocation:      BorderAll,
		ButtonBorderWidth:         1,
		ButtonGroupBorderLocation: BorderNone,
		ButtonGroupBorderWidth:    1,
		ButtonGroupPadding:        Scale(0),
		ButtonGroupModuleGap:      Scale(0.25),
		ButtonGroupBackground:     mustColor("bg-elevated"),
		ButtonGroupOpacity:        100,
		ButtonGroupBorderColor:    mustColor("border-accent"),
		ButtonGroupRounding:       RoundingSm,
		DropdownShadow:            true,
		DropdownOpacity:           75,
		DropdownAutohide:          true,
		DropdownFreezeLabel:       true,
	}
}

// BarLayout is the bar layout for one monitor
// (crates/wayle-config/src/schemas/bar/types/mod.rs). Keys a layout
// entry omits take BarLayout's own defaults (#[serde(default)] with the
// default layout's sections), so only an explicitly empty section
// inherits through `extends` — the Rust behavior.
type BarLayout struct {
	// Monitor connector name (e.g., `"DP-1"`) or `"*"` for all monitors.
	Monitor string `cfg:"monitor"`
	// Inherit from another layout by its monitor value (e.g., `"*"`).
	Extends *string `cfg:"extends"`
	// Whether the bar is visible on this monitor.
	Show bool `cfg:"show"`
	// When the bar hides on this monitor; unset follows `bar.auto-hide`.
	AutoHide *BarAutoHide `cfg:"auto-hide"`
	// Modules in the left section.
	Left []BarItem `cfg:"left"`
	// Modules in the center section.
	Center []BarItem `cfg:"center"`
	// Modules in the right section.
	Right []BarItem `cfg:"right"`
}

// DefaultBarLayout is BarLayout::default(): every monitor, the workspaces
// and media left, clock centered, the status modules right.
func DefaultBarLayout() BarLayout {
	return BarLayout{
		Monitor: "*",
		Show:    true,
		Left:    []BarItem{{Module: ModuleWorkspaces}, {Module: ModuleMedia}},
		Center:  []BarItem{{Module: ModuleClock}},
		Right: []BarItem{
			{Module: ModuleSystray},
			{Module: ModuleBackgroundApps},
			{Module: ModuleBattery},
			{Module: ModuleBluetooth},
			{Module: ModuleNetwork},
			{Module: ModuleMicrophone},
			{Module: ModuleVolume},
			{Module: ModuleNotifications},
			{Module: ModulePower},
		},
	}
}

func (l *BarLayout) setDefaults() { *l = DefaultBarLayout() }

// ExtendsName is the parent layout's monitor value, "" when unset.
func (l BarLayout) ExtendsName() (string, bool) {
	if l.Extends == nil {
		return "", false
	}
	return *l.Extends, true
}

func (BarLayout) configDescription() string {
	return "Layout configuration for a bar on a specific monitor.\n\n## Examples\n\n```yaml\n# Single modules\nbar:\n  layout:\n    - monitor: \"*\"\n      left: [dashboard]\n      center: [clock]\n      right: [systray]\n\n    # Module with custom CSS class for per-instance styling\n    - monitor: DP-1\n      left:\n        - {module: clock, class: primary-clock}\n        - clock\n      center: [media]\n\n    # Grouped modules (share a visual container, CSS-targetable by name)\n    - monitor: DP-2\n      left:\n        - {name: status, modules: [battery, network]}\n\n    # Groups can also contain classed modules\n    - monitor: DP-3\n      left:\n        - name: clocks\n          modules:\n            - {module: clock, class: local}\n            - {module: world-clock, class: remote}\n\n    # Inherit from another layout\n    - monitor: \"*\"\n      left: [dashboard]\n      center: [clock]\n      right: [systray]\n    - monitor: HDMI-1\n      extends: \"*\"\n      right: [volume, systray]  # Override just this section\n\n    # Hide bar on a specific monitor\n    - monitor: HDMI-2\n      show: false\n```"
}

// BarButtonVariant is the bar button structure.
//
// Visual style variants for bar buttons.
type BarButtonVariant string

// Button variants.
const (
	// Icon + label, minimal background.
	ButtonBasic BarButtonVariant = "basic"
	// Icon in colored pill container that blends into button edge.
	ButtonBlockPrefix BarButtonVariant = "block-prefix"
	// Button background with colored icon container inside.
	ButtonIconSquare BarButtonVariant = "icon-square"
)

var _ = registerEnum(ButtonBasic, ButtonBlockPrefix, ButtonIconSquare)

// IconPosition places a button's icon.
//
// Icon position within bar buttons.
type IconPosition string

// Icon positions.
const (
	// Icon before label (left for horizontal, top for vertical bars).
	IconStart IconPosition = "start"
	// Icon after label (right for horizontal, bottom for vertical bars).
	IconEnd IconPosition = "end"
)

var _ = registerEnum(IconStart, IconEnd)

// ShadowPreset is the bar shadow style
// (crates/wayle-config/src/schemas/bar/types/shadow.rs).
//
// Shadow style for the bar.
type ShadowPreset string

// Shadow presets.
const (
	// No shadow.
	ShadowNone ShadowPreset = "none"
	// Directional shadow opposite the anchor edge.
	ShadowDrop ShadowPreset = "drop"
	// All-around shadow.
	ShadowFloating ShadowPreset = "floating"
)

var _ = registerEnum(ShadowNone, ShadowDrop, ShadowFloating)

// BarAutoHide is when an auto-hiding bar hides.
type BarAutoHide string

// Bar auto-hide modes.
const (
	// The bar always shows.
	AutoHideNever BarAutoHide = "never"
	// The bar hides whenever the pointer is not on it.
	AutoHideAlways BarAutoHide = "always"
	// The bar hides while a window overlaps it (lys-wm; elsewhere as always).
	AutoHideWindowsOverlap BarAutoHide = "windows-overlap"
	// The bar hides while a window fills its monitor (lys-wm; elsewhere as always).
	AutoHideFullscreen BarAutoHide = "fullscreen"
)

var _ = registerEnum(AutoHideNever, AutoHideAlways, AutoHideWindowsOverlap, AutoHideFullscreen)

// defaultAutoHideDelayMs is how long an auto-hiding bar or dock stays
// after it stops being needed.
const defaultAutoHideDelayMs uint32 = 300

// AutoHideOn is the auto-hide mode of a monitor showing layout: the
// layout's own, else the bar's.
func (b BarConfig) AutoHideOn(layout BarLayout) BarAutoHide {
	if layout.AutoHide != nil {
		return *layout.AutoHide
	}
	return b.AutoHide
}

// MarginPx is the layer margin the shadow needs to render unclipped.
func (s ShadowPreset) MarginPx() uint32 {
	if s == ShadowNone {
		return 0
	}
	return 4
}

// CSSShadow is the box-shadow value for the bar's location.
func (s ShadowPreset) CSSShadow(loc Location) string {
	switch s {
	case ShadowDrop:
		switch loc {
		case LocationBottom:
			return "0 -1px 2px 1px rgba(0, 0, 0, 0.25)"
		case LocationLeft:
			return "1px 0 2px 1px rgba(0, 0, 0, 0.25)"
		case LocationRight:
			return "-1px 0 2px 1px rgba(0, 0, 0, 0.25)"
		}
		return "0 1px 2px 1px rgba(0, 0, 0, 0.25)"
	case ShadowFloating:
		return "0 1px 2px 1px rgba(0, 0, 0, 0.25)"
	}
	return "none"
}

// FontWeightClass is a typography weight
// (crates/wayle-config/src/schemas/styling/types/typography.rs).
//
// Font weight class for typography.
//
// Maps to CSS classes like `.weight-normal`, `.weight-bold`, etc.
// Uses the existing `--weight-*` tokens defined in the stylesheet.
type FontWeightClass string

// Font weights.
const (
	// Normal weight (--weight-normal: 400).
	WeightNormal FontWeightClass = "normal"
	// Medium weight (--weight-medium: 500).
	WeightMedium FontWeightClass = "medium"
	// Semi-bold weight (--weight-semibold: 600).
	WeightSemibold FontWeightClass = "semibold"
	// Bold weight (--weight-bold: 700).
	WeightBold FontWeightClass = "bold"
)

var _ = registerEnum(WeightNormal, WeightMedium, WeightSemibold, WeightBold)
