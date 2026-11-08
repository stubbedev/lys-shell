---
title: Custom modules
outline: [2, 3]
---

# Custom modules

<div v-pre>

A custom module shows a command's output in the bar. Define it in `modules.custom` with a unique `id` and place it as `custom-<id>`. Every field is listed in [`modules.custom`](/config/modules/custom).

```yaml
bar:
  layout:
    - monitor: "*"
      left: [custom-cpu-temp]
modules:
  custom:
    - id: cpu-temp
      command: sensors | awk '/^Tctl:/ {sub(/\+/,"",$2); print $2}'
      interval-ms: 5000
      icon-name: ld-thermometer-symbolic
```

The command runs through `sh -c`. Stderr is discarded; a run is killed after 30 seconds. Without a `command`, the module shows a static icon and label and only reacts to clicks.

## Modes

| `mode` | Behaviour |
| --- | --- |
| `poll` (default) | Runs every `interval-ms` (default 5000). `0` runs it once at startup only. |
| `watch` | Runs once; each stdout line replaces the output. |

When a `watch` command exits, `restart-policy` decides what follows, after `restart-interval-ms` (default 1000):

| `restart-policy` | Behaviour |
| --- | --- |
| `never` (default) | Keep the last output |
| `on-exit` | Restart after any exit |
| `on-failure` | Restart after a non-zero exit or a signal |

## Output

Plain text is available as `{{ output }}`. Output starting with `{` or `[` is parsed as JSON: each top-level field becomes a template variable, and `{{ output }}` stays the raw text. These fields have a meaning of their own:

| Field | Effect |
| --- | --- |
| `text` | Replaces `format` |
| `tooltip` | Replaces `tooltip-format` |
| `percentage` | 0-100, picks from `icon-names` |
| `alt` | Key into `icon-map` and `color-map` |
| `class` | Whitespace-separated CSS classes |

## Templates

`format`, `tooltip-format` and `class-format` use a Jinja2-compatible engine:

```
{{ field }}                 a top-level variable
{{ nested.field }}          a field of an object
{{ items.0 }}               an array element
{{ val | default("?") }}    fallback for a missing value
{{ val | upper }}           also: lower, trim, replace, length, round
```

`class-format` is split on whitespace into CSS classes, added to those from `class`.

## Icons and colors

The icon is the first that matches:

1. `icon-map[alt]`, with `"default"` as a fallback key;
2. `icon-names[percentage]`, the list split evenly over 0-100 (four icons: 0-24, 25-49, 50-74, 75-100);
3. `icon-name`.

`icon-show: false` hides it.

`color-map` is keyed by `alt` like `icon-map`. The matched entry's colors (`icon-color`, `icon-bg-color`, `label-color`, `button-bg-color`, `border-color`) replace the module's; colors it leaves unset keep the module's.

```yaml
icon-map: {muted: ld-volume-x-symbolic}
color-map:
  muted: {icon-color: red}
  default: {icon-color: green}
```

## Clicks

`left-click`, `right-click`, `middle-click`, `scroll-up` and `scroll-down` each take a [click action](/guide/bars-and-layouts#click-actions): a shell command, `dropdown:<name>`, or a brightness step.

`on-action` is a command run after each click or scroll binding; its output replaces the module's at once, so the bar does not wait for the next poll. Scrolls are coalesced: it runs 50 ms after the last one.

`hide-if-empty: true` hides the module, and its gap, when the output is empty, `0` or `false` (any case).

## Pushing updates

Another program can push output to a module by `id`, parsed exactly like command output:

```sh
lys-shell widget update gpu '{"text":"72°C","alt":"hot","percentage":90}'
```

This works alongside `poll` and `watch`.

## Examples

### Disk usage

JSON output drives `icon-names` from `percentage` and fills the label from other fields.

```yaml
modules:
  custom:
    - id: disk
      interval-ms: 30000
      command: |
        df -h / | awk 'NR==2 {
          sub(/%/, "", $5)
          printf "{\"percentage\":%s,\"used\":\"%s\",\"total\":\"%s\"}\n", $5, $3, $2
        }'
      format: "{{ used }} / {{ total }}"
      tooltip-format: "{{ percentage }}% used"
      icon-names:
        - ld-hard-drive-symbolic
        - ld-hard-drive-symbolic
        - ld-hard-drive-download-symbolic
        - ld-triangle-alert-symbolic
```

### Event-driven volume

A `watch` command that prints a line whenever PulseAudio reports a change. (The built-in `volume` module does this natively.)

```yaml
modules:
  custom:
    - id: volume
      mode: watch
      restart-policy: on-exit
      command: |
        emit_state() {
          vol=$(pactl get-sink-volume @DEFAULT_SINK@ \
            | grep -oP '\d+(?=%)' | head -1)
          mute=$(pactl get-sink-mute @DEFAULT_SINK@ | awk '{print $2}')
          alt=normal
          [ "$mute" = "yes" ] && alt=muted
          printf '{"percentage":%s,"alt":"%s"}\n' "$vol" "$alt"
        }
        emit_state
        pactl subscribe | while read -r line; do
          case "$line" in *sink*) emit_state ;; esac
        done
      scroll-up: pactl set-sink-volume @DEFAULT_SINK@ +5%
      scroll-down: pactl set-sink-volume @DEFAULT_SINK@ -5%
      left-click: pactl set-sink-mute @DEFAULT_SINK@ toggle
      format: "{{ percentage }}%"
      icon-names:
        - ld-volume-x-symbolic
        - ld-volume-1-symbolic
        - ld-volume-2-symbolic
      icon-map: {muted: ld-volume-x-symbolic}
```

</div>
