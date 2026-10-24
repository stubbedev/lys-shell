---
title: Getting started on Arch
---

# Getting started on Arch Linux

## Install dependencies

Go, plus the libraries Wayle loads at runtime (GLib, GStreamer for the screen
recorder, PAM for the lock screen, PipeWire and GBM for screen sharing):

```sh
sudo pacman -S --needed go git just glib2 gstreamer pam libpipewire mesa \
  gst-plugins-base gst-plugins-good gst-plugins-bad gst-plugins-ugly \
  gst-libav gst-plugin-pipewire
```

If you want the battery, bluetooth, network, power, or audio modules, install their daemons (skip any you don't need):

```sh
sudo pacman -S --needed bluez bluez-utils networkmanager upower \
  power-profiles-daemon pipewire wireplumber pipewire-pulse
sudo systemctl enable --now bluetooth NetworkManager upower power-profiles-daemon
```

## Build

The build needs only Go: the binaries are static, and load the libraries above
at runtime. `go.mod` pins Go 1.27; an older `go` (1.21 or later) downloads that
toolchain on its own.

```sh
git clone https://github.com/stubbedev/wayle
cd wayle
just build   # or: CGO_ENABLED=0 go build -o dist/ ./cmd/...
sudo install -Dm755 dist/wayle dist/wayle-settings dist/wayle-greeter dist/wayle-lock -t /usr/local/bin
```

## Icon assets

The icons are built into the binary. Install them into your user data
directory once:

```sh
wayle icons setup
```

## Run

Start the panel in the background:

```sh
wayle panel start
```

Other lifecycle commands: `wayle panel status`, `wayle panel restart`, `wayle panel stop`.

For debugging, run the shell in the foreground so logs print to the terminal:

```sh
wayle shell
```

## Settings GUI

```sh
wayle panel settings
```

This launches `wayle-settings`, which edits the same config the shell reads. Changes apply live. Anything the GUI doesn't cover can still be edited by hand in `config.toml`.

## Configuration

Wayle reads `$XDG_CONFIG_HOME/wayle/config.toml`, falling back to `~/.config/wayle/config.toml`. On first run, if no config exists, Wayle writes a default one. A JSON schema is written to `~/.config/wayle/schema.json` at startup, which editors with a TOML LSP can use for validation and completion.
