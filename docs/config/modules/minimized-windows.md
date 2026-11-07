---
title: minimized-windows
outline: [2, 3]
---

# minimized-windows

<div v-pre>

Minimized windows, like a dock.

Lists each minimized window as its app's icon, in the order the windows
were minimized, with the window title as tooltip. A click restores and
focuses the window, a middle click closes it, and a right click offers
both. Windows minimize into their icon and come back out of it. Works
on any compositor with the wlr foreign-toplevel protocol, lys-wm among
them.

Add it to your layout with `minimized-windows`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [minimized-windows]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `icon` | string | `"ld-app-window-symbolic"` | Icon for a window whose app has no desktop entry icon, and the module's own icon while nothing is minimized under show-when-empty. |
| `icon-size` | [`Size`](/config/types#size) | `1` | Size of each window's icon. Accepts a scale multiplier or pixels (e.g. `"24px"`). |
| `show-when-empty` | bool | `false` | Keep the module on the bar, showing its icon, while nothing is minimized. |
| `scope` | [`WindowScope`](/config/types#window-scope) | `"output"` | Which outputs' minimized windows the module lists. |

## Default configuration

```yaml
modules:
  minimized-windows:
    icon: ld-app-window-symbolic
    icon-size: 1.0
    show-when-empty: false
    scope: output
```


</div>
