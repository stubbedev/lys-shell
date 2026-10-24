# lys-shell xdg-desktop-portal backend

lys-shell ships its own `xdg-desktop-portal` backend (`lys-shell portal`), so screen
sharing, screenshots, remote control, global shortcuts, appearance settings and
more work on **any** Wayland compositor — niri, mango, Hyprland, sway, river —
not just the one whose portal happens to be installed.

Unlike `lys-shell portal share-picker` (an xdg-desktop-portal-hyprland *plugin* that only
runs under Hyprland), this backend plugs into the compositor-independent
`xdg-desktop-portal` **frontend**, which routes every app's portal request to
whichever backend `portals.conf` selects.

## Interfaces

Implemented natively by `lys-shell portal`:

| Interface | Backed by |
|---|---|
| `ScreenCast` | native PipeWire producer + wlr-screencopy / ext-image-copy capture |
| `RemoteDesktop` | `zwlr_virtual_pointer_v1` + `zwp_virtual_keyboard_v1`: relative+absolute pointer, scroll, evdev + keysym keys (libxkbcommon), and an `ConnectToEIS` libei socket (`reis`) |
| `Screenshot` (+ PickColor) | the shell's `dev.stubbe.lys.Screenshot1` overlay |
| `GlobalShortcuts` | `hyprland-global-shortcuts-v1` (where the compositor implements it) |
| `Settings` | `lys-shell-config` (`org.freedesktop.appearance`: color-scheme, accent, contrast) |
| `Notification` | the shell's notification daemon (incl. action buttons + ActionInvoked) |
| `Wallpaper` | `dev.stubbe.lys.Wallpaper1` |
| `Inhibit` | a systemd-logind inhibitor lock |
| `Clipboard` | `zwlr_data_control` (read/own the Wayland selection for RemoteDesktop sessions) |
| `Background` | autostart `.desktop` entries + permission |
| `Usb` | device-access permission grant (enumeration is frontend-side) |
| `InputCapture` | output zones from `wl_output`; capture itself unsupported (see below) |
| `FileChooser` | the shell's native `gtk::FileDialog` via `dev.stubbe.lys.FileChooser1` |
| `Email` | builds a `mailto:` and launches the default mail handler |
| `Access` | native GTK `AlertDialog` (grant/deny) via `dev.stubbe.lys.PortalDialogs1` |
| `Account` | consent dialog + local passwd/avatar lookup |
| `AppChooser` | native app-list dialog via `dev.stubbe.lys.PortalDialogs1` |
| `DynamicLauncher` | install-confirm dialog; frontend writes the `.desktop` |
| `Print` | native `GtkPrintUnixDialog` + `GtkPrintJob` via `dev.stubbe.lys.Print1` |
| `Lockdown` | static policy (nothing locked down) |

**Nothing is delegated** — lys-shell implements every `org.freedesktop.impl.portal.*`
interface itself, using GTK's own widgets (`FileDialog`, `PrintUnixDialog`,
`AlertDialog`) inside the shell, never the `xdg-desktop-portal-gtk` package. The
backend has no other portal as a dependency or fallback.

`InputCapture` is declared and answers `GetZones` from the live output layout,
but reports **zero capabilities** and performs no grab: real input capture
needs the compositor to redirect local input to the portal, which wlroots
compositors and niri do not expose (only KWin/Mutter back it). Clients detect
the zero capabilities and fall back cleanly.

## Previewing dialogs

`lys-shell portal show <dialog>` pops a single dialog UI directly, without an
application making a portal request — handy for developing and eyeballing the
dialogs. It talks to the running shell over the same `dev.stubbe.lys.*` services the
real backend delegates to, so `lys-shell shell` must be running.

```sh
lys-shell portal show file-chooser [--save] [--multiple] [--directory]
lys-shell portal show screenshot [--mode region|output|screen|window] [--target <output>]
lys-shell portal show color
lys-shell portal show print
lys-shell portal show screen-cast [--allow-token] [--multiple]
lys-shell portal show access
lys-shell portal show account
lys-shell portal show app-chooser
lys-shell portal show dynamic-launcher
```

The chosen result (or `cancelled`) is printed to stdout. The dialogs use
placeholder arguments, so they exercise the UI but do not act on a real request.

## Installing (non-Nix)

```sh
# 1. The backend binary is just `lys-shell portal`; make sure `lys-shell` is on PATH.
# 2. Declare the backend + its interfaces to the frontend:
install -Dm644 resources/lys-shell.portal \
  /usr/share/xdg-desktop-portal/portals/lys-shell.portal
# 3. Let the frontend D-Bus-activate it (edit Exec= if lys-shell is elsewhere):
install -Dm644 resources/org.freedesktop.impl.portal.desktop.lys.service \
  /usr/share/dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service
# 4. Route interfaces to lys-shell (and the dialog tier to gtk):
install -Dm644 resources/lys-shell-portals.conf \
  /usr/share/xdg-desktop-portal/lys-shell-portals.conf
```

Then start a session with `XDG_CURRENT_DESKTOP=lys-shell` (so the frontend reads
`lys-shell-portals.conf`), restart `xdg-desktop-portal`, and confirm:

```sh
busctl --user introspect org.freedesktop.impl.portal.desktop.lys \
  /org/freedesktop/portal/desktop
```

The shell (`lys-shell shell`) must be running for the interfaces that delegate to it
(Screenshot, Wallpaper, Notification, and the ScreenCast picker).

## Installing (Nix)

`nix/lys-shell.nix` installs `lys-shell.portal`, the D-Bus activation file (with the
store path substituted in), and reference copies of the systemd unit and
`lys-shell-portals.conf` under `$out/share/lys-shell`. Enable the backend through the
NixOS / home-manager module alongside the shell.

## Compositor notes

- **ScreenCast / Screenshot / RemoteDesktop / Settings / Notification /
  Wallpaper / Inhibit**: work on any compositor implementing the standard
  capture (`ext-image-copy-capture` or `wlr-screencopy`) and virtual-input
  (`wlr-virtual-pointer`, `virtual-keyboard`) protocols.
- **GlobalShortcuts**: requires `hyprland-global-shortcuts-v1`. Compositors that
  do not implement it (currently niri, mango) accept binds but never deliver
  activations — there is no portal-agnostic global-shortcut mechanism.

## Platform limitations (not stubs — no Wayland mechanism exists)

- **RemoteDesktop touch injection**: there is no virtual-touchscreen protocol on
  wlroots, so touch events are accepted but dropped. Pointer + keyboard (incl.
  keysym + EIS) are fully injected.
- **InputCapture grab**: real input capture needs the compositor to redirect
  local input to the portal, which wlroots/niri do not expose (only KWin/Mutter
  do). The interface reports zero capabilities and answers `GetZones`.
- **ScreenCast dmabuf**: frames are copied through SHM. dmabuf zero-copy is a
  performance optimization (needs GPU/gbm), not a missing feature — capture
  works on every GPU today.
