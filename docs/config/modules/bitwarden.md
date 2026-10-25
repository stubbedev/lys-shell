---
title: bitwarden
outline: [2, 3]
---

# bitwarden

<div v-pre>

Bitwarden vault: the bar shows whether it is locked; the dropdown logs
in, unlocks, searches the vault and copies an item's username,
password or one-time code.

The shell keeps only the session (the server, the email and the
refresh token) under `$XDG_STATE_HOME/lys-shell/bitwarden`, never the
master password, a key or an item. Copied secrets are marked so
clipboard managers, the shell's own history included, do not keep
them.

Add it to your layout with `bitwarden`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [bitwarden]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `server` | [`BitwardenServer`](/config/types#bitwarden-server) | `"bitwarden.com"` | Server the login form logs in to. A logged-in account keeps the server it logged in to. |
| `email` | string | `""` | Email the login form starts with. |
| `lock-after` | u32 | `15` | Lock the vault after this many minutes without a copy. 0 keeps it open until locked by hand. |
| `lock-with-session` | bool | `true` | Lock the vault when the session locks. |
| `clipboard-clear` | u32 | `30` | Clear a copied secret from the clipboard after this many seconds, unless something else was copied since. 0 leaves it there. |
| `sync-interval` | u32 | `30` | Sync the open vault every this many minutes. 0 syncs only on unlock and by hand. |
| `format` | string | `"{{ count }}"` | Format string for the label. |
| `icon-locked` | string | `"ld-lock-symbolic"` | Icon while the vault is locked or logged out. |
| `icon-unlocked` | string | `"ld-lock-open-symbolic"` | Icon while the vault is open. |
| `border-show` | bool | `false` | Display border around button. |
| `icon-show` | bool | `true` | Display module icon. |
| `label-show` | bool | `false` | Display label. |
| `label-max-length` | u32 | `0` | Max label characters before truncation with ellipsis. Set to 0 to disable. |

::: details More about `format`

#### Placeholders

- `{{ count }}` - Number of items in the open vault (empty while
  locked)

#### Examples

- `"{{ count }}"` - "142"

:::

## Colors

| Field | Type | Default | Description |
|---|---|---|---|
| `border-color` | [`ColorValue`](/config/types#color-value) | `"blue"` | Border color token. |
| `icon-color` | [`ColorValue`](/config/types#color-value) | `"auto"` | Icon foreground color. Auto selects based on variant for contrast. |
| `icon-bg-color` | [`ColorValue`](/config/types#color-value) | `"blue"` | Icon container background color token. |
| `label-color` | [`ColorValue`](/config/types#color-value) | `"blue"` | Label text color token. |
| `button-bg-color` | [`ColorValue`](/config/types#color-value) | `"bg-surface-elevated"` | Button background color token. |

## Click actions

| Field | Type | Default | Description |
|---|---|---|---|
| `left-click` | [`ClickAction`](/config/types#click-action) | `"dropdown:bitwarden"` | Action on left click. Defaults to opening the vault dropdown. |
| `right-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on right click. |
| `middle-click` | [`ClickAction`](/config/types#click-action) | `""` | Action on middle click. |
| `scroll-up` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll up. |
| `scroll-down` | [`ClickAction`](/config/types#click-action) | `""` | Action on scroll down. |

## Default configuration

```yaml
modules:
  bitwarden:
    server: bitwarden.com
    email: ""
    lock-after: 15
    lock-with-session: true
    clipboard-clear: 30
    sync-interval: 30
    format: '{{ count }}'
    icon-locked: ld-lock-symbolic
    icon-unlocked: ld-lock-open-symbolic
    border-show: false
    border-color: blue
    icon-show: true
    icon-color: auto
    icon-bg-color: blue
    label-show: false
    label-color: blue
    label-max-length: 0
    button-bg-color: bg-surface-elevated
    left-click: dropdown:bitwarden
    right-click: ""
    middle-click: ""
    scroll-up: ""
    scroll-down: ""
```


</div>
