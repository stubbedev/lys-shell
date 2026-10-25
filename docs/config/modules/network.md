---
title: network
outline: [2, 3]
---

# network

<div v-pre>

Network connection status with a dropdown for switching connections.

Add it to your layout with `network`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [network]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `wifi-disabled-icon` | string | `"ld-wifi-off-symbolic"` | WiFi icon when disabled. |
| `wifi-acquiring-icon` | string | `"ld-wifi-sync-symbolic"` | WiFi icon when connecting. |
| `wifi-offline-icon` | string | `"ld-wifi-zero-symbolic"` | WiFi icon when disconnected. |
| `wifi-connected-icon` | string | `"ld-wifi-symbolic"` | WiFi icon when connected but signal strength unavailable. |
| `wifi-signal-icons` | array of string | `[...]` | WiFi signal strength icons from weak to excellent. |
| `wired-connected-icon` | string | `"ld-ethernet-port-symbolic"` | Wired icon when connected. |
| `wired-acquiring-icon` | string | `"ld-loader-circle-symbolic"` | Wired icon when connecting. |
| `wired-disconnected-icon` | string | `"ld-unplug-symbolic"` | Wired icon when disconnected. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `true` | Display connection label (SSID for WiFi, "Wired" for ethernet). |
| `label-max-length` | u32 | `15` | Max label characters before truncation with ellipsis. Set to 0 to disable. |
| `vpn-connected-icon` | string | `"ld-lock-symbolic"` | Icon when a VPN is connected. |
| `vpn-connecting-icon` | string | `"ld-refresh-cw-symbolic"` | Icon while a VPN connection is in flight. |
| `vpn-disconnected-icon` | string | `"ld-unplug-symbolic"` | Icon when a VPN is configured but disconnected. |
| `vpn-show` | [`VpnShow`](/config/types#vpn-show) | `"auto"` | When the VPN state replaces the wifi/wired icon. |

::: details More about `wifi-signal-icons`

The signal percentage maps to icons: 0-25% uses icons\[0\], 26-50% uses
icons\[1\], etc.

:::

::: details More about `vpn-show`

`auto` shows it only once NetworkManager holds a VPN profile, so adding
the key changes nothing on a machine with no VPN.

:::

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"accent"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"accent"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"accent"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:network"` | Action on left click. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  network:
    wifi-disabled-icon: ld-wifi-off-symbolic
    wifi-acquiring-icon: ld-wifi-sync-symbolic
    wifi-offline-icon: ld-wifi-zero-symbolic
    wifi-connected-icon: ld-wifi-symbolic
    wifi-signal-icons:
      - ld-wifi-zero-symbolic
      - ld-wifi-low-symbolic
      - ld-wifi-high-symbolic
      - ld-wifi-symbolic
    wired-connected-icon: ld-ethernet-port-symbolic
    wired-acquiring-icon: ld-loader-circle-symbolic
    wired-disconnected-icon: ld-unplug-symbolic
    border-show: false
    border-color: accent
    icon-show: true
    icon-color: auto
    icon-bg-color: accent
    label-show: true
    label-color: accent
    label-max-length: 15
    button-bg-color: bg-surface-elevated
    vpn-connected-icon: ld-lock-symbolic
    vpn-connecting-icon: ld-refresh-cw-symbolic
    vpn-disconnected-icon: ld-unplug-symbolic
    vpn-show: auto
    left-click: dropdown:network
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
