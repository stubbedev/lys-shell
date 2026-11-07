---
title: passwords
outline: [2, 3]
---

# passwords

<div v-pre>

The Passwords page over the keyring.

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `reveal-reprompt-minutes` | u32 | `0` | Minutes after which revealing a secret asks for the collection's password again, counted from when the page last saw it unlocked; 0 never asks again. |

## Default configuration

```yaml
passwords:
  reveal-reprompt-minutes: 0
```


</div>
