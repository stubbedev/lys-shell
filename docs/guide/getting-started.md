---
title: Getting started
---

# Getting started

Wayle is a Wayland desktop shell written in Go on [gelm](https://github.com/stubbedev/gelm), a pure-Go Wayland toolkit. It provides a top bar, notification center, on-screen display, wallpaper management and much more. Built-in mechanisms to handle audio, Bluetooth, network, media, battery and power controls.

Settings can be edited in `config.toml`, through the `wayle-settings` GUI, or with the `wayle config` CLI. All three surfaces provided hot reloaded updates, reflected in the shell immediately.

Wayle requires a Wayland compositor that implements the `wlr-layer-shell` protocol. Compositor-specific modules target Hyprland, Niri, Mango and Sway.

On Hyprland, the built-in lock screen needs one extra compositor setting: `misc:allow_session_lock_restore = true`. Wayle keeps its lock client inside the shell process, so a shell restart while the session is locked (for example a `home-manager switch` bouncing `wayle.service`) has to re-take the lock, and Hyprland refuses that takeover unless the flag is set — the session would stay stuck on Hyprland's crashed-lockscreen render.

<a href="/wayle-preview.png" target="_blank" rel="noopener">
  <img src="/wayle-preview.png" alt="Wayle desktop shell" style="margin-bottom: 1.5rem;">
</a>

<a href="/wayle-settings-preview.png" target="_blank" rel="noopener">
  <img src="/wayle-settings-preview.png" alt="Wayle settings GUI">
</a>

## Install

- [Arch Linux](/guide/getting-started-arch)
- [Debian / Ubuntu](/guide/getting-started-debian)
- [Fedora](/guide/getting-started-fedora)
- [NixOS](/guide/getting-started-nixos)

<details>
<summary>Other distros</summary>

Building needs only Go (1.27, or any Go from 1.21 that fetches it). At runtime
Wayle loads these libraries, so install them from your distribution:

| Library   | Used by                         |
| --------- | ------------------------------- |
| GLib 2    | the screen recorder             |
| GStreamer | the screen recorder             |
| PAM       | the lock screen and greeter     |
| PipeWire  | screen sharing (ScreenCast)     |
| libgbm    | zero-copy screen sharing        |

The screen recorder also loads GStreamer's base, good, bad, ugly, libav and
PipeWire plugin sets.

</details>
