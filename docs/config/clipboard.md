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
| `history-mib` | [`ClipboardHistoryMiB`](/config/types#clipboard-history-mi-b) | `64` | Most memory the history keeps, in MiB, across every entry. The oldest entries are forgotten to make room for a new one. The budget is never less than the largest single entry kept. |

## Default configuration

```yaml
clipboard:
  history-mib: 64
```


</div>
