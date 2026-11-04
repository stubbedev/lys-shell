---
title: Getting started
---

# Getting started

lys-shell is a Wayland desktop shell written in Go on [lys-ui](https://github.com/stubbedev/lys-ui), a pure-Go Wayland toolkit. It provides a top bar, notification center, on-screen display, wallpaper management and much more. Built-in mechanisms to handle audio, Bluetooth, network, media, battery and power controls.

Settings can be edited in `config.yaml`, through the [`lys-settings`](https://github.com/stubbedev/lys-settings) GUI, or with the `lys-shell config` CLI. All three apply live: changes reach the running shell without a restart.

lys-shell requires a Wayland compositor that implements the `wlr-layer-shell` protocol. Compositor-specific modules target lys-wm, Hyprland, Niri, Mango and Sway.

The lock screen is [lys-login](https://github.com/stubbedev/lys-login). lys-shell links no lock screen code: `lys-shell lock`, logind's Lock signal and lock-on-start each run `lys-login lock` from the PATH as a process of its own, which outlives a shell restart. On Hyprland, set `misc:allow_session_lock_restore = true`: when the lock process dies with the session still locked, the shell starts it again, and that new process has to take over the lock.

<a href="../lys-shell-preview.png" target="_blank" rel="noopener">
  <img src="/lys-shell-preview.png" alt="lys-shell desktop shell" style="margin-bottom: 1.5rem;">
</a>

<a href="../lys-settings-preview.png" target="_blank" rel="noopener">
  <img src="/lys-settings-preview.png" alt="lys-shell settings GUI">
</a>

## Install

Each [release](https://github.com/stubbedev/lys-shell/releases) publishes a `.deb`, a Fedora `.rpm` and a tarball, for x86_64 and aarch64. To build from source instead, follow your distribution's guide:

- [Arch Linux](/guide/getting-started-arch)
- [Debian / Ubuntu](/guide/getting-started-debian)
- [Fedora](/guide/getting-started-fedora)
- [NixOS](/guide/getting-started-nixos)

<details>
<summary>Other distros</summary>

Building needs only Go (1.27, or any Go from 1.21 that fetches it). At runtime
lys-shell loads these libraries, so install them from your distribution:

| Library   | Used by                                                    |
| --------- | ---------------------------------------------------------- |
| Vulkan    | GPU rendering, zero-copy screen sharing, the screen recorder |
| VA-API    | the screen recorder on GPUs whose Vulkan driver encodes no video |

The screen recorder needs Vulkan and a GPU that encodes H.264, through its
Vulkan driver (Vulkan Video) or, where that has no encoder (Intel Xe2), its
VA-API driver (libva with intel-media-driver or Mesa's); without either it
reports that it cannot record, and why.
Everything else falls back to the CPU without Vulkan. Screen sharing
(ScreenCast) speaks to the PipeWire daemon, 1.0 or newer, directly and loads
no PipeWire library.

</details>
