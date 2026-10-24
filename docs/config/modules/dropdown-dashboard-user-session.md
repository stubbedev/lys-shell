---
title: dropdown-dashboard-user-session
outline: [2, 3]
---

# dropdown-dashboard-user-session

<div v-pre>

Settings for user session the in dashboard
## Examples

```yaml
modules: {dashboard: {user-session: {actions: [lock, log-out, reboot, power-off]}}}
```

Add it to your layout with `user-session`:

```yaml
bar:
  layout:
    - monitor: "*"
      right: [user-session]
```

## General

| Field | Type | Default | Description |
|---|---|---|---|
| `actions` | array of [`SessionAction`](/config/types#session-action) | `[...]` | Session actions to show on dashboard |

## Default configuration

```yaml
modules:
  dropdown-dashboard-user-session:
    actions:
      - lock
      - log-out
      - reboot
      - power-off
```


</div>
