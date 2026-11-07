---
title: Launcher
---

# Launcher

`lys-shell launcher` with no arguments opens the search panel on the focused
output: one field, and one ranked list over everything below. Nothing needs
configuring. Running the command again closes it.

Bind it to a key in your compositor. lys-wm's built-in config uses `SUPER + Space`;
if you keep your own config, add it there:

```yaml
binds:
  - {chord: SUPER + Space, action: exec lys-shell launcher}
```

```ini
# sway
bindsym $mod+space exec lys-shell launcher
# Hyprland
bind = SUPER, SPACE, exec, lys-shell launcher
```

niri and mango take the same command in their own bind syntax. The shell has to
be running, as it hosts the panel.

## What it searches

| Source | Notes |
| --- | --- |
| Applications | Desktop entries; their desktop actions are in the actions panel |
| Open windows | lys-wm, Hyprland, sway, niri and mango |
| Files | Recent files and an index, see below; a query starting with `/` or `~` lists that folder |
| Calculator | `6*7`, `sqrt(16)`, and conversions: `5 km to mi`, `100 f in c`, `1 GiB to MB`. No currencies |
| System | Lock, sleep, log out, restart, shut down, through the [`power`](/config/modules/power) commands. Those that end the session ask for a second Enter |
| Settings | Each page of [`lys-settings`](https://github.com/stubbedev/lys-settings), when it is installed |
| Clipboard | The history, in the list from three typed letters; `Clipboard History` lists it all |
| Emoji | In the list from two typed letters; `Emoji Picker` lists them all |
| Bitwarden | `Bitwarden Vault` opens the vault, asking for the master password if it is locked |
| Quick links, snippets | From `launcher.search`, see below |
| Browser bookmarks, history | Bookmarks from two typed letters, history from three, each with the site's icon. `Browser Bookmarks` and `Most Visited Sites` list them all. Firefox and Chromium families |
| Quick toggles | Do not disturb, Wi-Fi, Bluetooth, and switching the audio output |
| Media | Play, pause and skip on the active player |
| Capture | Screenshots, screen recording and the color picker, which copies the hex value. They start once the panel has closed |
| Processes | `Kill Process` lists your processes, largest first; ending one asks for a second Enter. The compositor, the shell and what started them are not listed |
| Script commands | From a folder, see below |
| Shell | `>command` runs a command; the actions panel can run it in a terminal |
| Web | The last row, always: search the web for what you typed |

It opens on the focused output, or the one [`launcher.monitor`](/config/launcher) names; a
name no output has is logged and the focused output is used, so a typo cannot lock
you out of the launcher.

An empty field suggests what you use most. Picking a row teaches the ranking,
overall and for what you typed to find it, so `f` comes to mean Firefox if you
keep choosing it. Uses count half as much every two weeks. What it learned is in
`$XDG_STATE_HOME/lys-shell/launcher-frecency.json`; `launcher.history.enable: false`
turns learning off.

## Keys

| Key | Does |
| --- | --- |
| `Up`, `Down`, `Ctrl+P`, `Ctrl+N`, `Ctrl+J` | Move the selection |
| `Page Up`, `Page Down` | Move by a page |
| `Enter` | The row's first action |
| `Ctrl+Enter` | The row's second action |
| `Ctrl+K` | The actions panel: everything the row can do, searchable |
| `Tab` | Complete a path or link keyword; otherwise the actions panel |
| `Ctrl+1` … `Ctrl+9` | Take that row in view |
| `Esc` | Go back a level, then close |
| `Backspace` | In an empty field, go back a level |

The footer always shows what `Enter` and `Ctrl+Enter` will do. A right click on a
row opens its actions; a click outside the panel closes it.

## The preview pane

Beside the list, the pane shows the selected row once the selection rests, so
arrowing through a list reads no files and captures no windows:

| Row | Pane |
| --- | --- |
| File | An image at fit size, the text of a text file, or a thumbnail |
| Snippet | Its text |
| Clipboard entry | Text in full, an image at fit size with its type and size, a copied file as that file, anything else its type and size |
| Window | Its picture, captured from the compositor, with its title, app and workspace |

`lys-shell launcher -show window` and `-show clipboard` show the same pane.
`launcher.search.previews: false` turns it off everywhere.

<img src="/launcher-preview-window-mode.png" alt="The window mode previewing a window: its picture, title, app and workspace">
<img src="/launcher-preview-clipboard-mode.png" alt="The clipboard mode previewing a copied text in full">
<img src="/launcher-preview-clipboard-mode-other.png" alt="The clipboard mode previewing an entry by its type and size">
<img src="/launcher-preview-search-mode.png" alt="The search panel previewing a file">

## The file index

There is no daemon. While the panel is open, and only if the index is older than
10 minutes, a background walk refreshes it: breadth first from the home directory,
at most 30,000 paths, 8 levels and 3 seconds, staying on one filesystem so a
network mount cannot hang it, and skipping hidden names and `node_modules`,
`vendor`, `target`, `build`, `dist`, `venv`, `__pycache__` and `result`. The result
is cached in `$XDG_CACHE_HOME/lys-shell/launcher-files.idx`. Recent files come from
`recently-used.xbel` and rank above indexed ones. A very large home directory
therefore loses its deepest files first; list what matters in
`launcher.search.file-roots`.

## Configuration

Every key has a default. [`launcher.search`](/config/launcher) holds them:

```yaml
launcher:
  search:
    web-engine: Kagi
    web-url: https://kagi.com/search?q={query}
    file-roots: [~/Documents, ~/src]
    file-exclude: [archive]
    quicklinks:
      - {name: GitHub, url: "https://github.com/search?q={query}", keyword: gh}
      - {name: Calendar, url: "https://calendar.example.com"}
    snippets:
      - {name: Signature, text: "Regards,\nAlex", keyword: sig}
```

With a `keyword`, `gh rust` opens the link with that query. Panel motion follows
[`animations`](/config/animations) (`animations.launcher` overrides it for this
surface) and reduced motion. The panel frosts where the compositor offers
background blur and rests opaque where it does not.

## Script commands

Any executable in `~/.config/lys-shell/launcher/commands` with a `@lys.title`
comment is a command. Directives sit in the comment lines at the top of the file:

```sh
#!/bin/sh
# @lys.title Shorten URL
# @lys.subtitle Through my shortener
# @lys.keywords link tiny
# @lys.icon ld-link-symbolic
# @lys.output copy
# @lys.argument url
# @lys.argument slug optional
exec shorten "$1" "$2"
```

| Directive | Meaning |
| --- | --- |
| `@lys.title` | Required: the name shown and searched |
| `@lys.subtitle`, `@lys.keywords`, `@lys.icon` | Optional; the icon is a name or an absolute path |
| `@lys.output` | `silent` (the default) runs it detached, `terminal` runs it in a terminal, `copy` waits up to 30 seconds and copies its stdout |
| `@lys.argument NAME [optional]` | Asked for in turn in the field, then passed as the next argument |

Arguments are passed as an argv, never through a shell. A directive it cannot read
is logged with its file and line, and that command stays out of the list.

## rofi compatibility

Everything else about `lys-shell launcher` is unchanged. `-dmenu`, `-show drun` and
the other rofi flags, `-e`, hooks, exit codes and a `rofi` symlink all behave as they
did, on their own surface. `-show search` is the panel by name.
