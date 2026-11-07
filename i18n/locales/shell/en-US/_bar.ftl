### Bar Module Labels

## Network
bar-network-connecting = Connecting...
bar-network-disconnected = Disconnected
bar-network-wired = Wired
bar-network-wifi-fallback = WiFi
bar-network-no-wifi = No WiFi
bar-network-no-ethernet = No Ethernet
bar-network-offline = Offline

## Battery
bar-battery-unavailable = N/A

## Bluetooth
bar-bluetooth-disabled = Off
bar-bluetooth-disconnected = Disconnected
bar-bluetooth-connected-count = { $count ->
    [one] { $count } Connected
   *[other] { $count } Connected
}

## Window Title
bar-window-title-empty = Desktop

## Idle Inhibit
bar-idle-inhibit-on = On
bar-idle-inhibit-off = Off
bar-idle-policy-off = The built-in idle policy is off
bar-idle-held-by = Idle held by { $holders }
bar-idle-held-by-you = you
bar-idle-all-reached = Every idle stage has run
bar-idle-next = Next: { $stage } after { $after } idle
bar-idle-stage-dim = dim
bar-idle-stage-lock = lock
bar-idle-stage-screen-off = screens off
bar-idle-stage-suspend = suspend

## Recorder
bar-recorder-idle = Idle
bar-recorder-recording = Recording
bar-recorder-paused = Paused
bar-recorder-replay = Replay armed

## Keybind Mode
bar-keybind-mode-default = default

## Night light
bar-nightlight-on = On
bar-nightlight-off = Off
bar-nightlight-no-location = Auto-schedule paused, no location: { $reason }

## Storage
bar-storage-multiple = Multiple

## Media
bar-media-playing = Playing
bar-media-paused = Paused
bar-media-stopped = Stopped

## Minimized windows and the dock

bar-task-restore = Restore
bar-task-close = Close
bar-task-launch = Open
bar-task-none = Nothing is minimized

## Global shortcuts the shell registers with the compositor

shortcut-launcher = Open or close the launcher

shortcut-dashboard = Open or close the dashboard

shortcut-bar-hide = Hide every bar

shortcut-bar-show = Show every bar

shortcut-bar-toggle = Hide or show every bar
