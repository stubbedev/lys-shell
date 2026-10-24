# CLI

Every command and subcommand takes `--help`.

Panel lifecycle and visibility:

```sh
lys-shell panel start
lys-shell panel stop
lys-shell panel restart
lys-shell panel status
lys-shell panel settings
lys-shell panel inspect

# Per-monitor visibility (omit the connector to affect all monitors)
lys-shell panel hide DP-1
lys-shell panel show DP-1
lys-shell panel toggle
```

Read and edit config values from the command line:

```sh
lys-shell config get bar.scale
lys-shell config set bar.scale 1.25
lys-shell config reset bar.scale

# Emit the JSON schema, default YAML, or markdown docs
lys-shell config schema --stdout
lys-shell config default --stdout
lys-shell config docs --out docs/config
```

Audio controls. Volume takes an absolute level (`0-100`) or a relative
adjustment (`+5`, `-10`):

```sh
lys-shell audio output-volume +5
lys-shell audio output-mute
lys-shell audio input-volume 50
lys-shell audio input-mute
lys-shell audio sinks
lys-shell audio sources
lys-shell audio status
```

Media player control. Most subcommands take an optional player id (a number or
partial name match); omit it to target the active player:

```sh
lys-shell media list
lys-shell media play-pause
lys-shell media next
lys-shell media previous
lys-shell media shuffle on
lys-shell media loop track
lys-shell media active spotify
lys-shell media info
```

Idle inhibition:

```sh
lys-shell idle on            # use the default duration
lys-shell idle on 30         # inhibit for 30 minutes
lys-shell idle on --indefinite
lys-shell idle off
lys-shell idle toggle
lys-shell idle duration +15  # +N / -N / N adjusts the upper limit
lys-shell idle remaining -5  # +N / -N / N adjusts the active timer
lys-shell idle status
```

Power profiles:

```sh
lys-shell power status
lys-shell power set balanced  # power-saver, balanced, performance
lys-shell power cycle
lys-shell power list
```

Notifications:

```sh
lys-shell notify list
lys-shell notify dismiss 42
lys-shell notify dismiss-all
lys-shell notify dnd          # toggle Do Not Disturb
lys-shell notify status
```

System tray:

```sh
lys-shell systray list
lys-shell systray activate <id>
lys-shell systray status
```

Wallpapers. Set a single image, or cycle through a directory:

```sh
lys-shell wallpaper set ~/Pictures/wall.png --fit fill --monitor DP-1
lys-shell wallpaper cycle ~/Pictures/walls --interval 300 --mode sequential
lys-shell wallpaper stop
lys-shell wallpaper next
lys-shell wallpaper previous
lys-shell wallpaper info --monitor DP-1
lys-shell wallpaper theming-monitor DP-1
```

`--fit` accepts `fill`, `fit`, `center`, `tile`, `stretch`; `--mode` accepts
`sequential` or `shuffle`.

Screen recorder:

```sh
lys-shell recorder start
lys-shell recorder stop
lys-shell recorder toggle
lys-shell recorder pause
lys-shell recorder resume
lys-shell recorder status
```

Screenshots:

```sh
lys-shell screenshot region        # drag a region or click a window, adjust, Enter
lys-shell screenshot output DP-1   # whole output (focused output if omitted)
lys-shell screenshot window        # the active window
```

Icons. Install bundled assets, pull from a CDN source, import local SVGs, or
sync everything referenced in your config:

```sh
lys-shell icons setup
lys-shell icons sources
lys-shell icons install tabler home settings bell
lys-shell icons import ~/Downloads/my-icon.svg my-icon
lys-shell icons list --source tb --interactive
lys-shell icons remove tb-home-symbolic
lys-shell icons export ~/exported-icons
lys-shell icons open
lys-shell icons sync --dry-run
```

Push a runtime update to a custom module by its `id`. The payload is parsed
exactly like the module's own command output, so it drives the label, icon, and
colors (see [custom modules](/guide/custom-modules)):

```sh
lys-shell widget update gpu '{"text":"72°C","alt":"hot","percentage":90}'
```

Show a custom on-screen toast. It reuses the OSD styling and position; add
`--percentage` for a progress bar, `--icon` for a leading icon, `--duration`
to override the OSD dismiss time, `--preset` to base it on a configured preset,
and `--class` for a custom CSS class:

```sh
lys-shell toast "Build finished"
lys-shell toast "Volume" --icon audio-volume-high-symbolic --percentage 65
lys-shell toast "Syncing…" --duration 5000
```

Run the desktop shell in the foreground:

```sh
lys-shell shell
```

Shell completions for any clap-supported shell (bash, fish, zsh, …):

```sh
lys-shell completions fish > ~/.config/fish/completions/lys-shell.fish
```

> `lys-shell share-picker` exists for the xdg-desktop-portal screencast flow and is
> invoked by the portal, not by hand.
