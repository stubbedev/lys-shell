---
title: Bars and layouts
---

# Bars and layouts

`bar.layout` is a list of entries, each describing the bar on one monitor. Spacing, colors and button styling are in [`bar`](/config/bar); the full entry type is [`BarLayout`](/config/types#bar-layout).

| Field | Meaning |
| --- | --- |
| `monitor` | Connector name (`"DP-1"`, `"HDMI-A-1"`) or `"*"` |
| `left`, `center`, `right` | Module lists |
| `extends` | Inherit from the entry with this `monitor` value |
| `show` | `false` hides the bar on this monitor |
| `auto-hide` | Overrides `bar.auto-hide` on this monitor |

An output uses the entry whose `monitor` equals its connector name, else the `"*"` entry. With neither, it gets no bar.

```yaml
bar:
  layout:
    - monitor: "*"
      left: [dashboard]
      center: [clock]
      right: [battery, network, volume, systray]
```

## Inheritance

An entry with `extends` takes every section it leaves empty (and `auto-hide`, if unset) from its parent. Chains are followed; a cycle or a missing parent stops the walk.

```yaml
bar:
  layout:
    - monitor: "*"
      left: [dashboard]
      center: [clock]
      right: [battery, network, volume, systray]
    - monitor: HDMI-1
      extends: "*"
      right: [volume, systray]
    - monitor: HDMI-2
      show: false
```

## Module slots

A slot in `left`, `center` or `right` is one of:

| Form | Example |
| --- | --- |
| Module | `clock` |
| Module with a CSS class | `{module: clock, class: primary}` |
| Group | `{name: status, modules: [battery, network]}` |

A group draws its modules in one container whose CSS ID is its `name` (`#status`). Its modules may carry classes. A class goes on that one module instance, so two instances of a module can be [styled](/guide/custom-styles) apart.

Custom modules are referenced as `custom-<id>`; see [Custom modules](/guide/custom-modules).

## Click actions

Module fields such as `left-click`, `right-click`, `middle-click`, `scroll-up` and `scroll-down` take a click action string:

| Value | Does |
| --- | --- |
| `""` | Nothing |
| `dropdown:<name>` | Toggle a dropdown under this module: `audio`, `background-apps`, `battery`, `bitwarden`, `bluetooth`, `brightness`, `calendar`, `dashboard`, `mail`, `media`, `network`, `notification`, `privacy`, `recorder`, `screen-share`, `treeman`, `weather` |
| `brightness:<N>` | Change every backlight by `N` percent (`+5`, `-5`), never below `modules.brightness.min-brightness` |
| `brightness:toggle` | Turn all backlights off, or back to 100% when all are off |
| anything else | Run as a shell command |
