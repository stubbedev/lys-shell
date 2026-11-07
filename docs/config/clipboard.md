---
title: clipboard
outline: [2, 3]
---

# clipboard

<div v-pre>

Clipboard history settings.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `history-mib` | [`ClipboardMiB`](/config/types#clipboard-mi-b) | `64` | Most memory the history keeps, in MiB, across every entry that is not pinned. The oldest entries are forgotten to make room for a new one. The budget is never less than the largest single entry kept. |
| `pinned-mib` | [`ClipboardMiB`](/config/types#clipboard-mi-b) | `16` | Most the pinned entries hold, in MiB. Pinned entries are listed first, kept in `$XDG_DATA_HOME/lys-shell/clipboard-pinned/` across restarts and never forgotten to make room; a pin past this budget is refused. |
| `persist` | bool | `false` | Keep the whole history across restarts, in `$XDG_DATA_HOME/lys-shell/clipboard-history/`, rather than in memory only. Turning it off removes what was kept. Selections a password manager marks secret are never kept either way. |

## Default configuration

```yaml
clipboard:
  history-mib: 64
  pinned-mib: 16
  persist: false
```


</div>
