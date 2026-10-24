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
// override earlier ones at equal specificity, so the palette and the
// tokens come first and the surfaces after the primitives they build
// on. TestSheetOrderCoversEveryFile pins it to the tree.
var sheetOrder = []string{
	"palette.css",
	"tokens/index.css",
	"base/index.css",
	"primitives/badge/index.css",
	"primitives/buttons/index.css",
	"primitives/dropdown/index.css",
	"primitives/empty_state/index.css",
	"primitives/modal/index.css",
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
	"components/settings/page.css",
	"components/settings/row.css",
	"components/settings/controls/dropdown.css",
	"components/settings/controls/spinbutton.css",
	"components/settings/controls/entry.css",
	"components/settings/controls/color_value.css",
	"components/settings/controls/yaml_editor.css",
	"components/settings/controls/font.css",
	"components/settings/controls/bar_layout.css",
	"components/settings/controls/monitor_wallpaper.css",
	"components/settings/controls/theme_selector.css",
	"components/settings/controls/icon_picker.css",
	"components/settings/controls/list_controls.css",
	"components/settings/controls/string_list.css",
	"components/settings/controls/threshold_list.css",
	"components/settings/controls/card_form_list.css",
	"modules/cava.css",
	"modules/launcher.css",
	"modules/lock.css",
	"modules/notification_popup/content.css",
	"modules/notification_popup/actions.css",
	"modules/notification_popup/index.css",
	"modules/osd/index.css",
	"modules/systray.css",
	"modules/treeman_dropdown/index.css",
	"modules/workspaces.css",
	"modules/audio_dropdown/section.css",
	"modules/audio_dropdown/slider.css",
	"modules/audio_dropdown/device_picker.css",
	"modules/audio_dropdown/app_volume.css",
	"modules/battery_dropdown/hero.css",
	"modules/battery_dropdown/gauge.css",
	"modules/battery_dropdown/details.css",
	"modules/battery_dropdown/charge_limit.css",
	"modules/battery_dropdown/power_profile.css",
	"modules/brightness_dropdown/section.css",
	"modules/calendar_dropdown/clock.css",
	"modules/calendar_dropdown/calendar.css",
	"modules/dashboard_dropdown/dashboard.css",
	"modules/dashboard_dropdown/quick_actions.css",
	"modules/dashboard_dropdown/controls.css",
	"modules/dashboard_dropdown/media.css",
	"modules/dashboard_dropdown/battery.css",
	"modules/dashboard_dropdown/network.css",
	"modules/dashboard_dropdown/system_stats.css",
	"modules/dashboard_dropdown/user_session.css",
	"modules/bluetooth_dropdown/device_list.css",
	"modules/bluetooth_dropdown/pairing_card.css",
	"modules/bluetooth_dropdown/index.css",
	"modules/mail_dropdown/index.css",
	"modules/media_dropdown/player_view.css",
	"modules/media_dropdown/source_picker.css",
	"modules/network_dropdown/active_connection.css",
	"modules/network_dropdown/network_list.css",
	"modules/network_dropdown/password_form.css",
	"modules/network_dropdown/vpn.css",
	"modules/network_dropdown/index.css",
	"modules/notification_dropdown/group.css",
	"modules/notification_dropdown/item.css",
	"modules/notification_dropdown/states.css",
	"modules/notification_dropdown/header.css",
	"modules/portal_surfaces.css",
	"modules/power_menu.css",
	"modules/recorder_dropdown/index.css",
	"modules/region_overlay.css",
	"modules/share_picker.css",
	"modules/weather_dropdown/header.css",
	"modules/weather_dropdown/stats.css",
	"modules/weather_dropdown/hourly.css",
	"modules/weather_dropdown/daily.css",
	"modules/weather_dropdown/sun_times.css",
	"modules/weather_dropdown/states.css",
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
