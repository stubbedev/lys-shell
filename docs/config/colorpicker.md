---
title: colorpicker
outline: [2, 3]
---

# colorpicker

<div v-pre>

Color picker settings.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `format` | [`ColorFormat`](/config/types#color-format) | `"hex"` | The format the picker's readout starts in, and the one a pick is copied and printed in. Tab in the picker cycles the readout through the formats; the one shown at the click is used, and kept for the next pick. |

## Default configuration

```yaml
colorpicker:
  format: hex
```


</div>
