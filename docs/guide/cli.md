# CLI

Every command takes `--help`. Commands that act on the shell need it running. A `MONITOR` or `NAME` argument is a connector name such as `DP-1`.

## panel

| Command | Does |
| --- | --- |
| `panel start` / `stop` / `restart` / `status` | Manage the shell process (`start` runs `lys-shell shell` in the background) |
| `panel settings` | Open lys-settings |
| `panel inspect` | Open the widget inspector (see [Custom styles](/guide/custom-styles#finding-selectors)) |
| `panel hide` / `show` / `toggle [MONITOR]` | Bar visibility; all monitors when omitted |
| `panel reveal [MONITOR]` | Show an auto-hiding bar for a few seconds |
| `panel dashboard [MONITOR]` | Toggle the dashboard; focused monitor when omitted |
| `panel focus [MONITOR]` | Move the keyboard into the bar: arrows walk modules, Enter or Space opens one, Esc leaves |

## config

| Command | Does |
| --- | --- |
| `config get PATH` | Print a value (`bar.scale`) |
| `config set PATH VALUE` | Write an override to `runtime.yaml`; `VALUE` is YAML |
| `config reset PATH` | Remove the override |
| `config schema [--stdout]` | Write `schema.json` to the config directory, or print it |
| `config default [--stdout]` | Write `config.yaml.example`, or print it |
| `config docs [--out DIR] [--only MODULE]` | Generate the config reference pages (default `docs/config`) |

See [Editing config](/guide/editing-config).

## audio

| Command | Does |
| --- | --- |
| `audio output-volume [LEVEL]` / `input-volume [LEVEL]` | Print, or set: `50` absolute, `+5` / `-10` relative |
| `audio output-mute` / `input-mute` | Toggle mute |
| `audio sinks` / `sources` / `status` | List outputs, inputs, current state |

## brightness

```sh
lys-shell brightness +10
lys-shell brightness 50 --output DP-1
```

`N` sets, `+N` raises, `-N` lowers, in percent, on the focused output or `--output`/`-o`. Never below `modules.brightness.min-brightness`. Laptop panels change through logind (no root needed); external monitors over DDC/CI when `modules.brightness.enable-external` is on. The shell applies the change, so the OSD and the bar module follow.

## media

| Command | Does |
| --- | --- |
| `media list` | List players |
| `media play-pause` / `next` / `previous [PLAYER]` | Control a player |
| `media shuffle [on\|off\|toggle] [PLAYER]` | Shuffle; toggles when omitted |
| `media loop none\|track\|playlist [PLAYER]` | Loop mode |
| `media active [PLAYER]` | Print or set the active player |
| `media info [PLAYER]` | Show player details |

`PLAYER` is a number or part of a name; the active player when omitted. `next` and `previous` show the now-playing OSD (see [`osd.media`](/config/osd)).

## idle

| Command | Does |
| --- | --- |
| `idle on [MINUTES] [-i]` | Inhibit idle for `MINUTES`, the default duration, or indefinitely with `--indefinite` |
| `idle off` | Stop inhibiting |
| `idle toggle [-i]` | Toggle |
| `idle duration VALUE` | Adjust the timer's upper limit: `N`, `+N`, `-N` |
| `idle remaining VALUE` | Adjust the running timer: `N`, `+N`, `-N` |
| `idle status` | Print the state |

## power, notify, systray

| Command | Does |
| --- | --- |
| `power status` / `list` / `cycle` | Current profile, available profiles, next profile |
| `power set PROFILE` | `power-saver`, `balanced` or `performance` |
| `notify list` / `status` | List notifications, print state |
| `notify dismiss ID` / `dismiss-all` | Dismiss |
| `notify dnd [--until TIME]` | Toggle Do Not Disturb; `--until` turns it on until `HH:MM` or for `30m`, `1h` |
| `systray list` / `status` | List tray items, print state |
| `systray activate ID` | Activate a tray item |

## wallpaper

| Command | Does |
| --- | --- |
| `wallpaper set PATH [--fit MODE] [--monitor NAME]` | Set an image; `MODE` is `fill`, `fit`, `center`, `tile` or `stretch`; all monitors when omitted |
| `wallpaper cycle DIR [-i SECONDS] [-m MODE]` | Cycle a folder every `SECONDS` (default 300), `sequential` (default) or `shuffle` |
| `wallpaper next` / `previous` / `stop` | Control cycling |
| `wallpaper info [--monitor NAME]` | Print the current state |
| `wallpaper theming-monitor NAME` | Monitor whose wallpaper drives color extraction; `""` for the default |

`lys-shell launcher -show wallpaper` opens the [wallpaper picker](/guide/launcher#wallpaper-picker). Wallpapers resampled for an output are cached in `$XDG_CACHE_HOME/lys-shell/wallpaper` (up to 256 MiB). An image that fails to decode, or takes over a second, shows the built-in wallpaper meanwhile and is logged.

## recorder

| Command | Does |
| --- | --- |
| `recorder start` / `stop` / `toggle` | Record |
| `recorder pause` / `resume` | Pause a recording |
| `recorder status` | Print the state |
| `recorder replay start` | Arm the instant replay |
| `recorder replay save` | Save the buffered seconds; prints the file |
| `recorder replay stop` | Disarm it and free its memory |

The instant replay keeps the last `modules.recorder.replay.seconds` (default 30) of an output encoded in memory, with keyframes every 2 s. `replay save` writes from the oldest keyframe to now as `lys-shell-replay-<time>.<format>` in the output directory, without interrupting the capture. Memory use is the bitrate times the seconds plus one keyframe interval; the recorder dropdown shows it. A normal recording can run at the same time on its own encoder. See [`modules.recorder`](/config/modules/recorder).

## screenshot

| Command | Does |
| --- | --- |
| `screenshot region` | Drag a region or click a window, adjust, Enter |
| `screenshot output [NAME]` | A whole output; the focused one when omitted |
| `screenshot window` | The active window |
| `screenshot text` | Copy the text in a dragged region (OCR with tesseract) |
| `screenshot qr` | Decode the QR code in a dragged region |

`text` and `qr` save no image and print what they read. `text` uses the languages in `modules.screenshot.ocr-languages` (the session language when empty), copies the text and shows its first line in a toast; Esc stops it. It fails if tesseract is missing. `qr` offers Copy, and Open for a link; with no code found nothing is copied.

## colorpicker

```sh
lys-shell colorpicker                        # print and copy, in colorpicker.format
lys-shell colorpicker --format oklch         # hex, rgb, hsl, hsv, oklch or cmyk
lys-shell colorpicker --no-copy --no-loupe   # print only; crosshair instead of the magnifier
```

Tab cycles the readout format; the format shown at the click is printed and copied, and the next pick starts in it. The recent swatches at the bottom can be picked again. Esc prints nothing and exits with status 1.

## icons

See [Custom icons](/guide/custom-icons).

## widget, toast

```sh
lys-shell widget update gpu '{"text":"72°C","alt":"hot"}'
lys-shell toast "Volume" --icon audio-volume-high-symbolic --percentage 65
```

`widget update ID OUTPUT` pushes output to a [custom module](/guide/custom-modules#pushing-updates). `toast [LABEL]` shows an on-screen toast styled and placed like the OSD:

| Flag | Effect |
| --- | --- |
| `--icon NAME` | Icon beside the text |
| `--percentage N` | Progress bar, 0-100 |
| `--duration MS` | Display time; the OSD's when unset |
| `--preset ID` | Start from an [`osd.presets`](/config/toast-preset) entry, which may supply the label |
| `--class NAME` | Extra CSS class |

## launcher

`lys-shell launcher` opens the search panel; with rofi flags it is a rofi-compatible launcher and dmenu. See [Launcher](/guide/launcher).

## lock, logout

| Command | Does |
| --- | --- |
| `lock` | Ask the shell to start the lock screen. `lys-lock` does the same as a single executable, for configs that take one word |
| `logout` | End the session through lys-wm when running, otherwise logind |

## portal

| Command | Does |
| --- | --- |
| `portal` / `portal run` | Run the xdg-desktop-portal backend (what the D-Bus and systemd units start) |
| `portal show DIALOG` | Preview a portal dialog: `file-chooser`, `screenshot`, `color`, `print`, `screen-cast`, `access`, `account`, `app-chooser`, `dynamic-launcher`, `authenticate`, `wallpaper` |
| `portal permissions list` | List what each app was granted |
| `portal permissions revoke APP TABLE [ENTRY]` | Revoke a table of grants, or one entry |

Permission tables: `background`, `devices`, `gamemode`, `global-shortcuts`, `inhibit`, `input-capture`, `location`, `notifications`, `realtime`, `remote-desktop`, `screencast`, `screenshot`, `usb`, `wallpaper`.

`portal share-picker` and `vpn sso-callback` are started by xdg-desktop-portal and by the browser after a VPN sign-in, not by hand.

## shell, completions

| Command | Does |
| --- | --- |
| `shell` | Run the shell in the foreground |
| `completions SHELL` | Print completions for `bash`, `elvish`, `fish`, `powershell` or `zsh` |

## IPC

The CLI talks to the running shell over these interfaces, which scripts can use directly.

**D-Bus** (session bus):

| Name | Object | Interface |
| --- | --- | --- |
| `dev.stubbe.lys.shell` | `/dev/stubbe/lys/shell` | `org.gtk.Actions` with the actions `quit` and `inspector` |
| `dev.stubbe.lys.Shell1` | `/dev/stubbe/lys/Shell` | `dev.stubbe.lys.Shell1`: `BarHide`, `BarShow`, `BarToggle`, `BarReveal`, `BarFocus`, `DashboardToggle` (each takes a monitor name, `""` for all or focused), `Lock`, `VpnSsoCallback` |

**Widget socket**: `$XDG_RUNTIME_DIR/lys-shell/widget.sock`, newline-delimited JSON-RPC 2.0. Methods:

| Method | Params |
| --- | --- |
| `widget.update` | `{"id": "<module id>", "output": "<text or JSON>"}` |
| `toast.show` | `{"label", "icon", "percentage", "duration_ms", "preset", "class"}`, all optional |

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"widget.update","params":{"id":"gpu","output":"72°C"}}' \
  | socat - UNIX-CONNECT:"$XDG_RUNTIME_DIR/lys-shell/widget.sock"
```
