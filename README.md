<p align="center">
  <img src="assets/lys-shell.svg" width="200" alt="lys-shell">
</p>

# lys-shell

<p align="center">
  <a href="https://github.com/stubbedev/lys-shell/actions"><img src="https://img.shields.io/github/actions/workflow/status/stubbedev/lys-shell/ci.yml?branch=master&style=for-the-badge" alt="CI"></a>
  <a href="https://github.com/stubbedev/lys-shell/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=for-the-badge" alt="License"></a>
</p>

A Wayland desktop shell with the bar, notifications, OSD, wallpaper, and device controls built in. Written in Go on [lys-ui](https://github.com/stubbedev/lys-ui), a pure-Go Wayland toolkit.

lys-shell started as a fork of [wayle-rs/wayle](https://github.com/wayle-rs/wayle) and is now an independent Go implementation that shares none of its code. Beyond the original it adds a YAML config, pixel sizing, per-state icon/color cycling, per-workspace icons, custom toasts, a JSON-RPC widget socket, and animations. It is the desktop shell of the lys family, beside [lys-ui](https://github.com/stubbedev/lys-ui) (the toolkit) and [lys-wm](https://github.com/stubbedev/lys-wm) (the compositor).

Configure it in `config.toml` or `config.yaml`, through the `lys-settings` GUI, or with the `lys-shell config` CLI.

## Features

- **Bar** with per-monitor layouts, groups, and CSS classes.
- **Built-in modules** — clock, battery, bluetooth, network, audio/volume, microphone, brightness, power, power profiles, system tray, storage, CPU/RAM, media player, audio visualizer (cava), notifications, weather, world clock, mail, screen recorder, idle inhibit, and Hyprland / Niri / Mango workspaces.
- **Custom modules** — back any bar widget with a shell command (poll or watch), with [icon and color cycling by state](docs/guide/custom-modules.md) (`icon-map` / `color-map` keyed on the output's `alt`).
- **Per-workspace icons** — give individual workspaces their own icon, shown even in label mode.
- **Notifications, OSD, and custom toasts** — `lys-shell toast "…"` shows an on-screen toast (icon + label, or a progress bar) reusing the OSD styling.
- **Animations** — configurable enter/exit transitions (fade / slide) for the OSD, toasts, and notification cards via `[animations]`.
- **Pixel or scale sizing** — every size accepts a scale multiplier or absolute pixels (`"24px"`), HiDPI-correct.
- **TOML or YAML config** with imports, live reload, and a published [JSON schema](schema/lys-shell-config.schema.json) for editor autocomplete.
- **Scriptable** — a JSON-RPC unix socket and CLI push live updates to any widget by id (`lys-shell widget update <id> …`).
- **Wallpaper and dynamic theming** — Matugen, Pywal, and Wallust palette extraction.

<p align="center">
  <img src="assets/lys-shell-preview.png" alt="lys-shell desktop shell">
</p>

<p align="center">
  <img src="assets/lys-settings-preview.png" alt="lys-shell settings GUI">
</p>

## Documentation

Guides and reference live in [`docs/`](docs/):

- [Getting started](docs/guide/getting-started.md) - Installation instructions
- [Editing config](docs/guide/editing-config.md) - File layout, live reload, imports, CLI editing
- [Bars and layouts](docs/guide/bars-and-layouts.md) - Per monitor layouts, groups, classes
- [Themes](docs/guide/themes.md) - Color tokens, theme files
- [Custom icons](docs/guide/custom-icons.md) - Installing icons, icon sources
- [Custom modules](docs/guide/custom-modules.md) - Shell-backed bar modules
- [CLI](docs/guide/cli.md) - Every subcommand
- [Config reference](docs/config/) - Full config documentation

## Install

Build from source (no prebuilt packages are published for this fork). The build needs only Go; the binaries are static and load GLib, GStreamer, PAM, PipeWire and GBM at runtime. The per-distro guides list the packages:

- [Arch Linux](docs/guide/getting-started-arch.md)
- [Debian / Ubuntu](docs/guide/getting-started-debian.md)
- [Fedora](docs/guide/getting-started-fedora.md)
- [NixOS](docs/guide/getting-started-nixos.md)
- [Other distros](docs/guide/getting-started.md)

### Build and launch:

```sh
git clone https://github.com/stubbedev/lys-shell
cd lys-shell
just build
sudo install -Dm755 dist/lys-shell dist/lys-settings dist/lys-greeter dist/lys-lock -t /usr/local/bin
lys-shell icons setup
lys-shell panel start
```

## Configuration

The config file is at `~/.config/lys-shell/config.toml`. Changes reload on save:

```toml
[bar]
location = "top"
scale = 1.25

[[bar.layout]]
monitor = "*"
left = ["dashboard"]
center = ["clock"]
right = ["volume", "network", "bluetooth", "battery"]

[modules.clock]
format = "%H:%M"
```

Every field is documented in the [config reference](docs/config/).

## Requirements

A Wayland compositor that implements the `wlr-layer-shell` protocol. Compositor-specific modules (such as workspaces) target Hyprland, Niri, Mango and Sway.

## Credits

Logo by [@M70v](https://www.instagram.com/m70v.art/).

## License

MIT
