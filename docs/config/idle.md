---
title: idle
outline: [2, 3]
---

# idle

<div v-pre>

Built-in idle policy: dim, lock, screens off, suspend, lock before sleep.

Each stage runs after the session has been idle for its timeout, with
separate timeouts on AC and on battery.

Idle inhibitors hold every stage: a video or game holding the
compositor's idle inhibitor, the idle-inhibit module's toggle, a
sandboxed app through the portal, and apps calling
`org.freedesktop.ScreenSaver.Inhibit`. Turn `enable` off to leave idle to
swayidle or hypridle instead.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `enable` | bool | `true` | Run the built-in idle policy. Turn it off when swayidle or hypridle handles idle, so the two do not both act. |
| `dim` | [`IdleTimeouts`](/config/types#idle-timeouts) | `{...}` | Idle time before the backlights dim. |
| `dim-percent` | [`IdleDimPercent`](/config/types#idle-dim-percent) | `30` | Share of its level, in percent, a dimmed backlight keeps. Input restores the exact level it had. |
| `lock` | [`IdleTimeouts`](/config/types#idle-timeouts) | `{...}` | Idle time before the session locks (through logind, which starts the lock screen). |
| `screen-off` | [`IdleTimeouts`](/config/types#idle-timeouts) | `{...}` | Idle time before the outputs power off. Input powers them back on. |
| `suspend` | [`IdleTimeouts`](/config/types#idle-timeouts) | `{...}` | Idle time before the system suspends. |
| `lock-before-sleep` | bool | `true` | Lock the session before the system sleeps (a lid close, a suspend from any program), and hold the sleep until the lock screen is up, so the machine always wakes locked. |

## Default configuration

```yaml
idle:
  enable: true
  dim:
    ac: 4m
    battery: 2m
  dim-percent: 30
  lock:
    ac: 5m
    battery: 3m
  screen-off:
    ac: 10m
    battery: 5m
  suspend:
    ac: never
    battery: 15m
  lock-before-sleep: true
```


</div>
