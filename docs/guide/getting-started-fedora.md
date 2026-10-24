---
title: Getting started on Fedora
---

# Getting started on Fedora

## Install dependencies

Go, plus the libraries Wayle loads at runtime (GLib, GStreamer for the screen
recorder, PAM for the lock screen, PipeWire and GBM for screen sharing):

```sh
sudo dnf install golang git just glib2 gstreamer1 pam pipewire-libs \
  mesa-libgbm gstreamer1-plugins-base gstreamer1-plugins-good \
  gstreamer1-plugins-bad-free gstreamer1-plugins-ugly-free \
  gstreamer1-plugin-libav pipewire-gstreamer
```

Fedora Workstation already ships the runtime daemons. Minimal and Server installs need:

```sh
sudo dnf install pipewire-pulseaudio wireplumber NetworkManager bluez upower \
  power-profiles-daemon
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
