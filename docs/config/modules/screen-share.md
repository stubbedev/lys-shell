---
title: screen-share
outline: [2, 3]
---

# screen-share

<div v-pre>

Screen share indicator.

Shows while an app captures the screen or a window through the desktop
portal; its dropdown lists each share's app and what it captures, with
a button that stops it.

Add it to your layout with `screen-share`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [screen-share]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `icon` | string | `"ld-monitor-symbolic"` | Bar button icon. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `false` | Display the number of shares beside the icon. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"red"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"red"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"fg-default"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:screen-share"` | Action on left click. Default opens the dropdown. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  screen-share:
    icon: ld-monitor-symbolic
    border-show: false
    border-color: red
    icon-show: true
    icon-color: auto
    icon-bg-color: red
    label-show: false
    label-color: fg-default
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: dropdown:screen-share
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
