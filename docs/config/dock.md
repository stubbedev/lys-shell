---
title: dock
outline: [2, 3]
---

# dock

<div v-pre>

A dock of pinned apps and open windows, on its own surface beside the bar.

Pinned apps launch on click. Open windows show under their app's icon
with a running indicator, dimmed while all of them are minimized; a click
focuses the app's window, bringing a minimized one back, and cycles
through its windows. Windows minimize into their dock icon. Works on any
compositor with the wlr foreign-toplevel protocol; hiding when a window
covers the dock needs lys-wm.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Show the dock. |
| `monitors` | array of string | `[...]` | Outputs that show the dock, by connector name (e.g. `"DP-1"`), or `"*"` for every output. |
| `edge` | [`DockEdge`](/config/types#dock-edge) | `"bottom"` | Screen edge the dock sits on. |
| `icon-size` | [`Size`](/config/types#size) | `2` | Size of each app's icon. Accepts a scale multiplier or pixels (e.g. `"48px"`). |
| `auto-hide` | bool | `false` | Hide the dock while a window covers it, revealing it when the pointer reaches its edge. When on, the dock reserves no space. Seeing which windows cover it needs lys-wm; elsewhere the dock shows only while the pointer is on it. |
| `pinned` | array of string | `[]` | Apps always on the dock, in order, by desktop entry id without `.desktop` (e.g. `"firefox"`, `"org.gnome.Nautilus"`), the app id their windows report. |
| `icon` | string | `"ld-app-window-symbolic"` | Icon for an app without a desktop entry icon. |

## Default configuration

```yaml
dock:
  enabled: false
  monitors:
    - '*'
  edge: bottom
  icon-size: 2.0
  auto-hide: false
  pinned: []
  icon: ld-app-window-symbolic
```


</div>
