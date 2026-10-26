---
title: Getting started on Fedora
---

# Getting started on Fedora

## Install dependencies

Go, plus the libraries lys-shell loads at runtime (GLib, GStreamer for the screen
recorder, PAM for the lock screen, Vulkan for GPU rendering and zero-copy screen
sharing). Screen sharing speaks to the PipeWire daemon directly and needs
PipeWire 1.2 or newer:

```sh
sudo dnf install golang git just glib2 gstreamer1 pam \
  vulkan-loader gstreamer1-plugins-base gstreamer1-plugins-good \
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
git clone https://github.com/stubbedev/lys-shell
cd lys-shell
just build   # or: CGO_ENABLED=0 go build -o dist/ ./cmd/...
sudo install -Dm755 dist/lys-shell dist/lys-settings dist/lys-greeter dist/lys-lock -t /usr/local/bin
```

## Icon assets

The icons are built into the binary. Install them into your user data
directory once:

```sh
lys-shell icons setup
```

## Run

Start the panel in the background:

```sh
lys-shell panel start
```

Other lifecycle commands: `lys-shell panel status`, `lys-shell panel restart`, `lys-shell panel stop`.

For debugging, run the shell in the foreground so logs print to the terminal:

```sh
lys-shell shell
```

## Settings GUI

```sh
lys-shell panel settings
```

This launches `lys-settings`, which edits the same config the shell reads. Changes apply live. Anything the GUI doesn't cover can still be edited by hand in `config.yaml`.

## Configuration

lys-shell reads `$XDG_CONFIG_HOME/lys-shell/config.yaml`, falling back to `~/.config/lys-shell/config.yaml`. On first run, if no config exists, lys-shell writes a default one. A JSON schema is written to `~/.config/lys-shell/schema.json` at startup, which editors running the YAML language server can use for validation and completion (see [Editor setup](/guide/editing-config#editor-setup)).
