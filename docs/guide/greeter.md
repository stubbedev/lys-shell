# Greeter (login screen)

`lys-login greet` is a [greetd](https://sr.ht/~kennylevinsen/greetd/) greeter — a
display-manager login screen that can replace sddm/gdm for Wayland sessions. It
is the same login screen as the lock screen, in its greeter mode: both read the
`login` config section, so they share the theme, background, blur, clock and
the failed-attempt handling; `login.greeter` holds what only the greeter has
(user list, power buttons, cursor). It is editable under **Login & Lock Screen**
and **Greeter** in `lys-settings`.

Features:

- **Session picker** — discovers Wayland sessions from
  `wayland-sessions/*.desktop` and X11 sessions from `xsessions/*.desktop` (the
  same files sddm/gdm read) and offers them in a dropdown. X11 entries are
  labelled `(X11)` and launched through `startx`. The last-used session is
  remembered and pre-selected.
- **User list** — real login accounts (from `/etc/passwd`) shown as clickable
  avatars; avatars come from AccountsService
  (`/var/lib/AccountsService/icons/<user>`) or `~/.face`, with an
  initial-letter fallback. Hidden when there are more than 8 users (the
  username field always works).
- **Remembered username** — the last successful username is pre-filled, with
  focus on the password field.
- **Caps Lock warning** — shown under the password entry while Caps Lock is on.
- **Power controls** — shutdown and reboot buttons.
- **Localized** — greeter labels follow the system locale (same Fluent-based
  i18n as the shell).

## Configuration

The greeter reads the `login` section of its config file (see below for where
that file lives per setup); the full list is in the
[config reference](/config/login):

| Key | Default | What it does |
| --- | --- | --- |
| `background-mode` | `"color"` | `color`, `image`, or `wallpaper` (reuses `wallpaper.wallpaper`). |
| `background-image` | `""` | Image path for `background-mode: image`. |
| `background-color` | `"#000000"` | Fill color for `background-mode: color`. |
| `blur` | `0` | Blur radius for image and wallpaper backgrounds. |
| `show-clock` | `true` | Show the clock above the login form. |
| `clock-format` / `date-format` | `"%H:%M"` / `"%A, %B %-d"` | strftime formats. |
| `greeter.show-user-list` | `true` | Clickable avatars for login users. |
| `greeter.show-power-buttons` | `true` | Shutdown/reboot buttons at the bottom. |
| `greeter.cursor-theme` | `""` (auto) | Xcursor theme. Empty = auto-detect (see below). |
| `greeter.cursor-size` | `24` (auto) | Logical cursor size; scaled per display, so HiDPI outputs get a sharp cursor (hotplug included). Unset = auto-detect. |

The former `greeter` and `lock` sections still load: each key is moved to its
place in `login` with a warning naming the new key.

When `greeter.cursor-theme` / `greeter.cursor-size` are not set, the greeter auto-detects the
cursor the last logged-in user actually sees. The primary source is a small file
each running lys-shell shell writes at startup —
`~/.local/state/lys-shell/greeter-cursor`, recording the session's live
`XCURSOR_THEME` / `XCURSOR_SIZE` (size falling back to `HYPRCURSOR_SIZE`). That
is exactly what the compositor exported, so the login screen keeps the last
session's cursor even when it was set via env or `hyprcursor` rather than a
config file. If no record exists, the greeter falls back to reading dotfiles:
the Hyprland config (`env = XCURSOR_*` lines, `hyprctl setcursor`, following
`source =` includes), niri's `cursor` block, sway's `seat … xcursor_theme`, GTK
`settings.ini`, and `~/.icons/default/index.theme` — preferring the compositor
of the remembered session. Both are best-effort: they only work when that user's
home is readable by the greetd user (e.g. `chmod o+x` on `$HOME`); otherwise the
`XCURSOR_THEME`/`XCURSOR_SIZE` environment and finally the defaults apply.

> **Cursor theme must be reachable by the greeter.** The greeter runs as the
> unprivileged `greeter` user (home `/var/empty`), so a theme installed only via
> home-manager (in your `0700` home) is invisible to it — and with *no* theme
> resolvable it shows the large built-in fallback cursor, which ignores the size.
> On NixOS the module inherits the system cursor automatically: set it the usual
> way (`environment.sessionVariables.XCURSOR_THEME` / `XCURSOR_SIZE`) and install
> the theme system-wide (`environment.systemPackages`, so it lands in
> `/run/current-system/sw/share/icons`, which is on the greeter's `XCURSOR_PATH`).
> An explicit `greeter.settings.login.greeter.cursor-theme` still overrides. On other
> setups install the theme system-wide and pass `XCURSOR_THEME` / `XCURSOR_PATH`
> in the greetd command.

These options live under **Login & Lock Screen** and **Greeter** in `lys-settings`. The
greeter reads the *system* config (`/etc/lys-shell/config.yaml` plus a
`runtime.yaml` overlay beside it), not your user config. The **Apply to login
screen** button on that page pushes the current greeter values to the system via
`pkexec lys-login apply-config` (bundled polkit action
`dev.stubbe.lys.greeter.apply-config`, admin auth), writing only the login keys
the greeter reads to `/etc/lys-shell/runtime.yaml` — so any user can set the login-screen background
without hand-editing root-owned files, and a hand-written `config.yaml` is never
clobbered.

## How it runs

greetd starts a kiosk compositor ([cage](https://github.com/cage-kiosk/cage))
whose single client is the greeter. On successful login greetd replaces the
greeter with your chosen session.

```
greetd → cage -s → lys-login greet → (login) → your compositor
```

## NixOS

Enable it through the system module:

```nix
programs.lys-shell.greeter = {
  enable = true;
  # Theme it like your desktop (full lys-shell config schema; the greeter honours
  # styling, login.* and wallpaper).
  settings = {
    styling.appearance = "dark";
    login.background-mode = "color";
    login.background-color = "#1e1e2e";
    login.greeter.cursor-size = 24;
  };
};
```

Sessions are discovered from the aggregate NixOS sessions directory, which
contains the session files of every installed Wayland compositor. Options:

| Option | Default | What it does |
| --- | --- | --- |
| `greeter.session.dirs` | NixOS sessions dir | Directories scanned for Wayland `*.desktop` session files. |
| `greeter.session.x11Dirs` | NixOS xsessions dir | Directories scanned for X11 session files (launched via `startx`; `[]` disables). |
| `greeter.session.command` | `""` | Optional explicit fallback session, offered as a "Custom" entry. |
| `greeter.session.environment` | `[]` | Extra `KEY=value` entries for the started session. |
| `greeter.renderer` | `"auto"` | `auto` tries GPU and falls back to software; `software` forces the driver-free pixman + cairo path. |
| `greeter.graphicsWrapper` | `[]` | Command prefix (e.g. a nixGL wrapper) around the whole launch — for non-NixOS-style driver setups. |

The last session/username are stored in `/var/lib/lys-greeter/`, owned by the
greetd user.

## Other distros (manual greetd)

Point greetd's `default_session` at the greeter, hosted by cage:

```toml
# /etc/greetd/config.toml
[default_session]
command = "cage -s -- lys-login greet --config /etc/lys-shell/config.yaml --state /var/lib/lys-greeter/last-session"
user = "greeter"
```

- The greeter runs pre-login as the greetd user (no `$HOME`), so its theme
  config lives at a system path — `/etc/lys-shell/config.yaml` by default.
- Create the state dir and make it writable by the greetd user:
  `install -d -o greeter -g greeter -m 0700 /var/lib/lys-greeter`.
- Sessions are discovered from `/usr/local/share/wayland-sessions` and
  `/usr/share/wayland-sessions`; add `--sessions DIR` to override.
- An explicit fallback session can be appended: `-- sway` (offered as
  "Custom", and the only entry when no `.desktop` sessions exist).

With home-manager, `programs.lys-shell.greeter.enable` installs a
`lys-greeter-session` wrapper (cage + greeter in one command) you can point
greetd at directly.

### Software rendering / nixGL

The login screen needs no GPU: with `renderer = "software"` (or manually
`WLR_RENDERER=pixman WLR_DRM_NO_MODIFIERS=1 GSK_RENDERER=cairo GDK_DISABLE=gl`)
the whole stack renders without any GPU driver — useful on VMs or hosts where GL
init is unreliable.

On non-NixOS hosts running the nix-built package, GPU acceleration needs
[nixGL](https://github.com/nix-community/nixGL) wrapping the whole launch:

```
nixGLIntel lys-greeter-session --config /etc/lys-shell/config.yaml --state /var/lib/lys-greeter/last-session
```

## CLI reference

```
lys-login greet [--config PATH] [--sessions DIR]... [--xsessions DIR]... [--state PATH] [--env KEY=VAL]... [-- <session argv...>]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--config PATH` | `/etc/lys-shell/config.yaml` | lys-shell config used for theming. |
| `--sessions DIR` | `/usr/{local/,}share/wayland-sessions` | Wayland session `.desktop` dir (repeatable; overrides defaults). |
| `--xsessions DIR` | `/usr/{local/,}share/xsessions` | X11 session `.desktop` dir (repeatable; launched via `startx`). |
| `--state PATH` | `$XDG_STATE_HOME/lys-greeter/last-session` | File the last session id is remembered in (username in a `last-user` sibling). |
| `--env KEY=VAL` | — | Extra environment for the started session (repeatable). |
| `-- <argv...>` | — | Optional explicit fallback session ("Custom"). |
