---
title: osd
outline: [2, 3]
---

# osd

<div v-pre>

On-screen display overlay for transient events like volume and brightness.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Show OSD overlays for volume, brightness, keyboard layout switches, and lock keys (Caps, Num and Scroll Lock need read access to /dev/input, the input group). |
| `position` | [`OsdPosition`](/config/types#osd-position) | `"bottom"` | Screen anchor position. |
| `text-align` | [`OsdTextAlign`](/config/types#osd-text-align) | `"center"` | Horizontal alignment of toast and toggle overlay content. Sliders (volume/brightness) keep their own label+value layout. |
| `duration` | u32 | `2500` | Auto-dismiss delay in milliseconds. |
| `monitor` | [`OsdMonitor`](/config/types#osd-monitor) | `"primary"` | Target monitor: "primary" or a connector name like "DP-1". |
| `margin` | [`Size`](/config/types#size) | `1` | Margin from screen edges: a multiplier of the default 150px (`1.0` = default) or absolute pixels (e.g. `"150px"`). |
| `border` | bool | `true` | Show a border around the OSD. |
| `layer` | [`Layer`](/config/types#layer) | `"overlay"` | Layer-shell layer the OSD is placed on. |
| `presets` | array of [`ToastPreset`](/config/types#toast-preset) | `[]` | Reusable toast presets, each triggerable with `lys-shell toast --preset <id>`. |

::: details More about `layer`

When `general.tearing-mode` is enabled, `overlay` is demoted to `top`
to allow fullscreen tearing.

:::

## Default configuration

```yaml
osd:
  enabled: true
  position: bottom
  text-align: center
  duration: 2500
  monitor: primary
  margin: 1.0
  border: true
  layer: overlay
  presets: []
```


</div>
