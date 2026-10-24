---
title: portal
outline: [2, 3]
---

# portal

<div v-pre>

Desktop-portal policy for sandboxed apps.

## Lockdown

| Field | Type | Default | Description |
|---|---|---|---|
| `lockdown` | [`LockdownConfig`](/config/types#lockdown-config) | `{...}` | Features to lock down for every app, for kiosk or shared setups. |

## Dynamic launcher

| Field | Type | Default | Description |
|---|---|---|---|
| `dynamic-launcher` | [`DynamicLauncherConfig`](/config/types#dynamic-launcher-config) | `{...}` | Which apps may install launchers without asking. |

## Default configuration

```yaml
portal:
  lockdown:
    disable-printing: false
    disable-save-to-disk: false
    disable-application-handlers: false
    disable-location: false
    disable-camera: false
    disable-microphone: false
    disable-sound-output: false
  dynamic-launcher:
    install-token-apps: []
```


</div>
