---
title: background-apps
outline: [2, 3]
---

# background-apps

<div v-pre>

Background apps indicator.

Shows while a sandboxed app keeps running without a window, as the
desktop portal's background monitor lists them; its dropdown names each
app with the status it reports and a button that stops it.

Add it to your layout with `background-apps`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [background-apps]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `icon` | string | `"ld-layers-symbolic"` | Bar button icon. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `true` | Display the number of background apps beside the icon. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"blue"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"blue"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"fg-default"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:background-apps"` | Action on left click. Default opens the dropdown. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  background-apps:
    icon: ld-layers-symbolic
    border-show: false
    border-color: blue
    icon-show: true
    icon-color: auto
    icon-bg-color: blue
    label-show: true
    label-color: fg-default
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: dropdown:background-apps
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
