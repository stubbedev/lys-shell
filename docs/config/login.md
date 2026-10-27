---
title: login
outline: [2, 3]
---

# login

<div v-pre>

Login screen: the greeter and the lock screen, one screen in two modes.

The greeter reads the system config (`/etc/lys-shell/config.yaml`), the
lock screen the user's: copy or symlink your user config if you want the
greeter to follow it.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `background-mode` | [`LockBackground`](/config/types#lock-background) | `"color"` | How the background is drawn: solid color, an image, or the wallpaper. |
| `background-image` | string | `""` | Background image path (used when `background-mode: image`). |
| `background-color` | [`HexColor`](/config/types#hex-color) | `"#000000"` | Background fill color (used when `background-mode: color`). |
| `blur` | [`LockBlur`](/config/types#lock-blur) | `0` | Gaussian blur radius applied to image/wallpaper backgrounds (0 = none). |
| `show-clock` | bool | `true` | Show a clock above the credential prompt. |
| `clock-format` | string | `"%H:%M"` | `strftime` format for the time. |
| `date-format` | string | `"%A, %B %-d"` | `strftime` format for the date. |
| `max-attempts` | u32 | `0` | Maximum failed password attempts before further input is blocked (`0` = unlimited). A locked session stays locked regardless. |
| `show-failed-attempts` | bool | `true` | Show the failed-attempt count. |
| `lock` | [`LoginLockConfig`](/config/types#login-lock-config) | `{...}` | The lock screen's own settings. |
| `greeter` | [`LoginGreeterConfig`](/config/types#login-greeter-config) | `{...}` | The greeter's own settings. |

## Default configuration

```yaml
login:
  background-mode: color
  background-image: ""
  background-color: '#000000'
  blur: 0
  show-clock: true
  clock-format: '%H:%M'
  date-format: '%A, %B %-d'
  max-attempts: 0
  show-failed-attempts: true
  lock:
    enabled: true
    lock-on-start: false
    grace-period-ms: 0
    blank-timeout-ms: 0
    pam-service: system-auth
  greeter:
    show-user-list: true
    show-power-buttons: true
    cursor-theme: ""
    cursor-size: 24
```


</div>
