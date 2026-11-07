---
title: clock
outline: [2, 3]
---

# clock

<div v-pre>

Clock module configuration.

Add it to your layout with `clock`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [clock]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `format` | [`StrftimeFormat`](/config/types#strftime-format) | `"%a %b %d %I:%M %p"` | Format string using strftime syntax. |
| `icon-name` | string | `"ld-calendar-clock-symbolic"` | Symbolic icon name. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `true` | Display text label. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |
| `calendar-weekday-start` | [`WeekStart`](/config/types#week-start) | `"sunday"` | First day of the week in the calendar dropdown. |
| `calendars` | array of [`CalendarSource`](/config/types#calendar-source) | `[]` | vdir calendars whose events the calendar dropdown shows: directories of .ics files, one per event, as vdirsyncer and khal keep them (`~/` is the home directory). Edits show without a restart; syncing stays with vdirsyncer, the shell never goes online for them. The bar label shows the next event through a `{{ next_event }}` placeholder in `format`, and event reminders (VALARM) become notifications. |

::: details More about `format`

#### Common Specifiers

- `%H` - Hour (00-23)
- `%I` - Hour (01-12)
- `%M` - Minute (00-59)
- `%S` - Second (00-59)
- `%p` - AM/PM
- `%a` - Abbreviated weekday (Mon, Tue)
- `%A` - Full weekday (Monday)
- `%b` - Abbreviated month (Jan, Feb)
- `%B` - Full month (January)
- `%d` - Day of month (01-31)
- `%Y` - Year (2024)

#### Examples

- `"%H:%M"` - "14:30"
- `"%I:%M %p"` - "02:30 PM"
- `"%a %b %d %I:%M %p"` - "Mon Jan 15 02:30 PM"

:::

::: details More about `calendars`

#### Example

```yaml
modules:
  clock:
    calendars:
      - {path: ~/.local/share/calendars/work, color: blue}
      - {path: ~/.local/share/calendars/home}
```

:::

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"border-accent"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"accent"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"fg-default"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:calendar"` | Action on left click. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:weather"` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Dropdown

| Field | Type | Default | Description |
|---|---|---|---|
| `dropdown-show-seconds` | bool | `false` | Show seconds in the calendar dropdown clock display. |

## Default configuration

```yaml
modules:
  clock:
    format: '%a %b %d %I:%M %p'
    icon-name: ld-calendar-clock-symbolic
    border-show: false
    border-color: border-accent
    icon-show: true
    icon-color: auto
    icon-bg-color: accent
    label-show: true
    label-color: fg-default
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: dropdown:calendar
    right-click: dropdown:weather
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
    dropdown-show-seconds: false
    calendar-weekday-start: sunday
    calendars: []
```


</div>
