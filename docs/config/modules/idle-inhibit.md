---
title: idle-inhibit
outline: [2, 3]
---

# idle-inhibit

<div v-pre>

Toggle that prevents screen dim, lock, and suspend while active.

Controllable from the CLI: `lys-shell idle on|off|duration|remaining|status`.

Add it to your layout with `idle-inhibit`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [idle-inhibit]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `startup-duration` | [`IdleInhibitDuration`](/config/types#idle-inhibit-duration) | `60` | Duration in minutes when service starts. 0 means indefinite. |
| `icon-inactive` | string | `"ld-bed-symbolic"` | Icon when idle inhibitor is inactive. |
| `icon-active` | string | `"ld-coffee-symbolic"` | Icon when idle inhibitor is active. |
| `format` | string | `"{{ state }}"` | Format string for the label. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `true` | Display label. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

::: details More about `format`

#### Placeholders

- `{{ state }}` - Inhibitor state text (On, Off)
- `{{ remaining }}` - Time remaining (e.g., "45m", shows "--" when indefinite)
- `{{ duration }}` - Total duration (e.g., "60m", shows "--" when indefinite)

#### Examples

- `"{{ state }}"` - "On"
- `"{{ remaining }}/{{ duration }}"` - "45m/60m"
- `"{{ state }} ({{ remaining }})"` - "On (45m)"

:::

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"green"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"green"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"green"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"lys-shell idle toggle --indefinite"` | Action on left click. Default toggles indefinite idle inhibit. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `"lys-shell idle toggle"` | Action on right click. Default toggles timed idle inhibit. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  idle-inhibit:
    startup-duration: 60
    icon-inactive: ld-bed-symbolic
    icon-active: ld-coffee-symbolic
    format: '{{ state }}'
    border-show: false
    border-color: green
    icon-show: true
    icon-color: auto
    icon-bg-color: green
    label-show: true
    label-color: green
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: lys-shell idle toggle --indefinite
    right-click: lys-shell idle toggle
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
