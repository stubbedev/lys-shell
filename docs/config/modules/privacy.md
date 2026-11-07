---
title: privacy
outline: [2, 3]
---

# privacy

<div v-pre>

Privacy indicator.

Shows while an app records a microphone, holds a camera open or shares
the screen, with an icon for each; its dropdown names each app, with a
button that mutes its recording, stops its camera or ends its share.

Add it to your layout with `privacy`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [privacy]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `icon` | string | `"ld-mic-symbolic"` | Icon shown while an app records a microphone. |
| `camera-icon` | string | `"ld-webcam-symbolic"` | Icon shown while an app holds a camera open. |
| `screen-icon` | string | `"ld-monitor-symbolic"` | Icon shown while an app shares the screen. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `false` | Display the number of apps beside the icons. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"yellow"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"yellow"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"fg-default"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:privacy"` | Action on left click. Default opens the dropdown. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  privacy:
    icon: ld-mic-symbolic
    camera-icon: ld-webcam-symbolic
    screen-icon: ld-monitor-symbolic
    border-show: false
    border-color: yellow
    icon-show: true
    icon-color: auto
    icon-bg-color: yellow
    label-show: false
    label-color: fg-default
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: dropdown:privacy
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
