---
title: Config reference
outline: [2]
---

<div v-pre>

# Config reference

Every config file lives at `~/.config/lys-shell/config.yaml`. Each page below covers one section. Every field has a default; start with an empty file and add only what you want to change.

::: tip
Editor intellisense via JSON Schema. Install the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) for VSCode or `yaml-language-server` for Neovim, Helix, or Zed. The schema is written to `~/.config/lys-shell/schema.json` on startup, and the config file lys-shell creates starts with the `# yaml-language-server: $schema=./schema.json` modeline that points the server at it.
:::

## Top-level sections

| Section | What it controls |
|---|---|
| [`animations`](/config/animations) | Enter/exit and change animations for transient surfaces. |
| [`bar`](/config/bar) | Bar chrome: per-monitor layout, spacing, colors, and button styling. |
| [`clipboard`](/config/clipboard) | Clipboard history settings. |
| [`dropdowns`](/config/dropdowns) | Per-dropdown foldout panel sizing. |
| [`general`](/config/general) | Shell-wide settings that don't belong to any specific module. |
| [`greeter`](/config/greeter) | Greeter (display manager): the pre-login screen `lys-greeter` renders as |
| [`launcher`](/config/launcher) | Application launcher / dmenu (rofi replacement). |
| [`lock`](/config/lock) | Lock screen: a secure session lock rendered by lys-shell via `ext-session-lock-v1`. |
| [`mail-account`](/config/mail-account) | One mail account in the `modules.mail` per-account breakdown. |
| [`osd`](/config/osd) | On-screen display overlay for transient events like volume and brightness. |
| [`portal`](/config/portal) | Desktop-portal policy for sandboxed apps. |
| [`share-picker`](/config/share-picker) | Screen-share picker shown by xdg-desktop-portal when an app requests a |
| [`styling`](/config/styling) | Theme, palette, and rounding tokens applied shell-wide. Changes recompile the stylesheet. |
| [`toast-preset`](/config/toast-preset) | A reusable toast preset, triggerable by id with `lys-shell toast --preset <id>`. |
| [`wallpaper`](/config/wallpaper) | Wallpaper rendering, cycling, and per-monitor overrides. |

## Bar modules

Modules appear inside the `bar.layout` list. Each row links to the full reference.

| Module | Purpose |
|---|---|
| [`battery`](/config/modules/battery) | Battery level, charging state, and a dropdown with power-profile controls. |
| [`bitwarden`](/config/modules/bitwarden) | Bitwarden vault: the bar shows whether it is locked; the dropdown logs |
| [`bluetooth`](/config/modules/bluetooth) | Bluetooth connection status with a dropdown for pairing and managing devices. |
| [`brightness`](/config/modules/brightness) | Backlight control bar module. |
| [`cava`](/config/modules/cava) | Audio frequency bars visualising the output stream. |
| [`clock`](/config/modules/clock) | Clock module configuration. |
| [`cpu`](/config/modules/cpu) | CPU usage, frequency, and temperature. |
| [`custom`](/config/modules/custom) | User-defined module that runs a shell command and renders the output in the bar. |
| [`dashboard`](/config/modules/dashboard) | Quick-access button with a distro icon; opens the dashboard dropdown. |
| [`dropdown-dashboard-user-session`](/config/modules/dropdown-dashboard-user-session) | Settings for user session the in dashboard |
| [`hyprland-workspaces`](/config/modules/hyprland-workspaces) | Hyprland workspace indicators with click-to-switch. |
| [`idle-inhibit`](/config/modules/idle-inhibit) | Toggle that prevents screen dim, lock, and suspend while active. |
| [`keybind-mode`](/config/modules/keybind-mode) | Current keybind-mode indicator for modal compositors. |
| [`keyboard-input`](/config/modules/keyboard-input) | Active keyboard layout indicator. |
| [`lys-wm-workspaces`](/config/modules/lys-wm-workspaces) | lys-wm workspace indicators with click-to-switch. |
| [`mail`](/config/modules/mail) | Unread mail count, backed by a notmuch query. |
| [`mango-workspaces`](/config/modules/mango-workspaces) | MangoWM tag switcher module configuration. |
| [`media`](/config/modules/media) | Now-playing title and playback controls for the active MPRIS player. |
| [`microphone`](/config/modules/microphone) | Microphone input level and mute toggle. |
| [`netstat`](/config/modules/netstat) | Network traffic counters (up/down rates). |
| [`network`](/config/modules/network) | Network connection status with a dropdown for switching connections. |
| [`nightlight`](/config/modules/nightlight) | Night light: warms the display colours, by hand or on a sunrise/sunset schedule. |
| [`niri-workspaces`](/config/modules/niri-workspaces) | Niri workspace indicators with click-to-switch. |
| [`notifications`](/config/modules/notifications) | Notification center: icon in the bar, dropdown with history, DND toggle. |
| [`power`](/config/modules/power) | Shutdown, reboot, and logout menu. |
| [`power-profiles`](/config/modules/power-profiles) | Power profile indicator and switcher (power-profiles-daemon). |
| [`ram`](/config/modules/ram) | Memory and swap usage. |
| [`recorder`](/config/modules/recorder) | Native screen recorder encoding on the GPU's video encoder. |
| [`screenshot`](/config/modules/screenshot) | Screenshot capture button. |
| [`separator`](/config/modules/separator) | A vertical rule between bar modules. |
| [`storage`](/config/modules/storage) | Disk usage for a mount point. |
| [`sway-workspaces`](/config/modules/sway-workspaces) | sway workspace indicators with click-to-switch. |
| [`systray`](/config/modules/systray) | System tray icons via the StatusNotifierItem protocol. |
| [`treeman`](/config/modules/treeman) | treeman worktree health across all registered repos, in a dropdown. |
| [`volume`](/config/modules/volume) | Output volume control with a dropdown for device and app volumes. |
| [`weather`](/config/modules/weather) | Current conditions with hourly and daily forecasts in a dropdown. |
| [`window-title`](/config/modules/window-title) | Active window title with optional app-icon prefix. |
| [`world-clock`](/config/modules/world-clock) | Multiple timezones shown together in a dropdown. |

## Shared types

Every named type referenced across the config (`Color`, `ClickAction`, `Size`, and others) is documented on the [types page](/config/types).

</div>
