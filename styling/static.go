package styling

import (
	"embed"
	"strings"
)

// sheets is the built-in stylesheet, one plain CSS file per surface.
//
//go:embed css
var sheets embed.FS

// sheetOrder is the cascade order of the files under css/: later files
// override earlier ones at equal specificity, so the tokens come first
// and the surfaces after the primitives they build on (the palette and
// color tokens come from ThemeCSS and TokensCSS). TestSheetOrderCoversEveryFile pins it to the tree.
var sheetOrder = []string{
	"tokens/index.css",
	"base/index.css",
	"primitives/badge/index.css",
	"primitives/buttons/index.css",
	"primitives/dropdown/index.css",
	"primitives/progress_ring/index.css",
	"primitives/status-dot/index.css",
	"components/bar/container.css",
	"components/bar/layout.css",
	"components/bar_button/base.css",
	"components/bar_button/block_prefix.css",
	"components/bar_button/icon_square.css",
	"components/bar_button/modifiers.css",
	"components/bar_container/index.css",
	"components/settings/window.css",
	"components/settings/sidebar.css",
	"components/settings/controls/yaml_editor.css",
	"components/settings/controls/bar_layout.css",
	"components/settings/controls/theme_selector.css",
	"components/settings/controls/icon_picker.css",
	"components/settings/controls/list_controls.css",
	"components/settings/controls/card_form_list.css",
	"modules/cava.css",
	"modules/launcher.css",
	"modules/lock.css",
	"modules/notification_popup/content.css",
	"modules/notification_popup/index.css",
	"modules/osd/index.css",
	"modules/systray.css",
	"modules/treeman_dropdown/index.css",
	"modules/workspaces.css",
	"modules/audio_dropdown/section.css",
	"modules/audio_dropdown/device_picker.css",
	"modules/battery_dropdown/hero.css",
	"modules/battery_dropdown/details.css",
	"modules/brightness_dropdown/section.css",
	"modules/calendar_dropdown/clock.css",
	"modules/calendar_dropdown/calendar.css",
	"modules/dashboard_dropdown/dashboard.css",
	"modules/bluetooth_dropdown/device_list.css",
	"modules/bluetooth_dropdown/index.css",
	"modules/media_dropdown/player_view.css",
	"modules/media_dropdown/source_picker.css",
	"modules/network_dropdown/active_connection.css",
	"modules/network_dropdown/network_list.css",
	"modules/network_dropdown/vpn.css",
	"modules/network_dropdown/index.css",
	"modules/notification_dropdown/item.css",
	"modules/portal_surfaces.css",
	"modules/power_menu.css",
	"modules/recorder_dropdown/index.css",
	"modules/region_overlay.css",
	"modules/share_picker.css",
	"modules/weather_dropdown/index.css",
}

// StaticCSS is the built-in stylesheet: every file in sheetOrder,
// concatenated.
var StaticCSS = concatSheets()

func concatSheets() string {
	var b strings.Builder
	for _, name := range sheetOrder {
		data, err := sheets.ReadFile("css/" + name)
		if err != nil {
			panic("styling: built-in stylesheet " + name + ": " + err.Error())
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
