---
title: nightlight
outline: [2, 3]
---

# nightlight

<div v-pre>

Night light: warms the display colours, by hand or on a dawn/dusk schedule.

Add it to your layout with `nightlight`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [nightlight]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `format` | string | `"{{ status }}"` | Format string for the label. |
| `temperature` | u32 | `5000` | Color temperature in Kelvin when filter is enabled. Range: 1000-20000. |
| `gamma` | u32 | `100` | Display gamma percentage when filter is enabled. Range: 0-200. |
| `auto-schedule` | bool | `false` | Automatically ease the filter on at dusk and off at dawn, following the sun at the machine's location. |
| `transition-duration` | [`NightlightTransition`](/config/types#nightlight-transition) | `15` | Length of the transition between day and night, in whole minutes, centred on civil dawn or dusk. 0 switches at once. Range: 0-120. |
| `latitude` | f64 or null | `null` | Latitude for the schedule, in decimal degrees (north positive). Range: -90 to 90. Unset uses GeoClue. Set together with `longitude`, it overrides GeoClue. Only used when `auto-schedule` is on. |
| `longitude` | f64 or null | `null` | Longitude for the schedule, in decimal degrees (east positive). Range: -180 to 180. Unset uses GeoClue. Set together with `latitude`, it overrides GeoClue. Only used when `auto-schedule` is on. |
| `icon-off` | string | `"ld-sun-symbolic"` | Icon when filter is disabled (showing normal daylight colors). |
| `icon-on` | string | `"ld-moon-symbolic"` | Icon when filter is enabled (showing warm night colors). |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `tooltip` | bool | `true` | Show the module's state in a tooltip on hover. |
| `label-show` | bool | `true` | Display label. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

::: details More about `format`

#### Placeholders

- `{{ status }}` - Filter status (On, Off)
- `{{ temp }}` - Current temperature in Kelvin (-- while off)
- `{{ gamma }}` - Current gamma percentage (-- while off)
- `{{ config_temp }}` - Configured temperature in Kelvin
- `{{ config_gamma }}` - Configured gamma percentage

#### Examples

- `"{{ status }}"` - "On"
- `"{{ temp }}K {{ gamma }}%"` - "4500K 80%"
- `"{{ status }} ({{ temp }}K)"` - "On (4500K)"

:::

::: details More about `auto-schedule`

The schedule anchors to civil dawn and civil dusk, when the sun is 6
degrees below the horizon, with each transition centred on the event.
Where the sun never crosses that line (polar day and polar night) the
filter stays off or on with no transition.

The location comes from GeoClue, unless `latitude` and `longitude` are
both set: explicit coordinates win, and GeoClue is then never asked. A
spelled-out 0, 0 counts as unset. The last GeoClue fix is remembered in
the state directory. With no location at all, or with only one coordinate
or one off the globe, the schedule does not run: the module says why in
its tooltip and in the log, and the filter stays as it is.

GeoClue answers a client only once an agent serves the user, and only
GNOME and a few other desktops run one. With none running, lys-shell
registers as the agent itself for the length of the lookup, which GeoClue
allows only for names on its whitelist. The deb and rpm install that as
`/etc/geoclue/conf.d/50-lys-shell.conf`, and the NixOS module's
`autoInstallDependencies` sets it up too. From the release archive, install
the archive's `geoclue-lys-shell.conf` at that path yourself and run
`sudo systemctl restart geoclue`. GeoClue older than 2.7 does not read
`conf.d`: put the file's `lys-shell` entry in `/etc/geoclue/geoclue.conf`
and add `lys-shell` to `whitelist` under `[agent]` there. A `whitelist` in a
drop-in replaces the main file's, so the shipped file repeats upstream's
list; if you have changed yours, add your agents to the drop-in.

The log names what to check when the lookup fails. No answer usually means
the whitelist above. No fix after that usually means GeoClue has no source
that can place the machine, as upstream's default config names no Wi-Fi
location service: set `url` under `[wifi]`, describe the location in
`/etc/geolocation` for the static source, or set `latitude` and `longitude`.

A manual click cancels a running transition, applies at once, and holds
until the next transition begins, after which the schedule resumes.

:::

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
| `left-click` | [`ClickAction`](/config/types#click-action) | `":toggle"` | Action on left click. Default toggles blue light filter. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  nightlight:
    format: '{{ status }}'
    temperature: 5000
    gamma: 100
    auto-schedule: false
    transition-duration: 15
    icon-off: ld-sun-symbolic
    icon-on: ld-moon-symbolic
    border-show: false
    border-color: yellow
    icon-show: true
    tooltip: true
    icon-color: auto
    icon-bg-color: yellow
    label-show: true
    label-color: fg-default
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: :toggle
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
