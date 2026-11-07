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
| Calculator | `6*7`, `sqrt(16)`, conversions (`5 km to mi`, `100 f in c`, `1 GiB to MB`), currencies, times and dates, see below |
| System | Lock, sleep, log out, restart, shut down, through the [`power`](/config/modules/power) commands. Those that end the session ask for a second Enter |
| Settings | Each page of [`lys-settings`](https://github.com/stubbedev/lys-settings), when it is installed |
| Clipboard | The history, in the list from three typed letters; `Clipboard History` lists it all, pinned entries first. See [Clipboard history](#clipboard-history) |
| Emoji | In the list from two typed letters, in your skin tone; `Emoji Picker` lists them all, see below |
| Unicode characters | Any character Unicode names, from three typed letters: `right arrow` finds →, `degree` finds °. `Unicode Characters` lists them all |
| Bitwarden | `Bitwarden Vault` opens the vault, asking for the master password if it is locked |
| Quick links, snippets | From `launcher.search`, see below |
| Browser bookmarks, history | Bookmarks from two typed letters, history from three, each with the site's icon. `Browser Bookmarks` and `Most Visited Sites` list them all. Firefox and Chromium families |
| Quick toggles | Do not disturb, Wi-Fi, Bluetooth, switching the audio output, and Choose Wallpaper, which opens the wallpaper picker |
| Media | Play, pause and skip on the active player |
| Capture | Screenshots, screen recording and the color picker, which copies the color in the format the picker showed (see [`colorpicker`](/config/colorpicker); Tab in the picker cycles it). They start once the panel has closed |
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

## Currencies, times and dates

The calculator row also answers:

| Query | Answer |
| --- | --- |
| `100 usd in eur`, `$100 to €`, `12.5 dollars in euros` | The amount at the European Central Bank's reference rate, with the rate's date |
| `15:00 PST in Copenhagen`, `3pm london to tokyo`, `9:30 am in Europe/Paris` | That time in the other zone, and the day if it changes |
| `time in Tokyo`, `now in new york` | The time there now |
| `today + 90 days`, `2026-12-24 + 2 weeks`, `3 weeks from tomorrow`, `10 days ago` | The date |
| `days until 2027-01-01`, `days since 2026-01-01`, `2026-12-24 - today` | The number of days |

Currencies are the 30 or so the ECB publishes, by code, symbol (`$`, `€`, `£`, `¥`)
or name (`dollars`, `euros`, `yen`); `kr` names the krone or krona of your language
and nothing elsewhere. The rates are fetched at most once a day, never while you
type, and kept in `$XDG_CACHE_HOME/lys-shell/fx.xml`. Offline, the cached rates are
used and the row says how old they are; with no rates at all there is no currency
row. `launcher.search.currency-rates: false` stops the fetch.

Time zones are abbreviations, which are fixed offsets (`PST` is UTC−8 even in July,
`PDT` is UTC−7), the generic `PT`, `MT`, `CT` and `ET`, which follow daylight saving,
major cities, and IANA names such as `America/Los_Angeles`. A time with no zone is
local. Month and year steps keep to the end of the month: January 31 plus one month
is the last day of February.

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
| `Ctrl+Backspace` | On a clipboard row, forget it |

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

Text shows as `bat` shows it: line numbers in a gutter and the code highlighted in
the theme's palette, so it reads in a light theme as in a dark one. The language
comes from the file name, else the `#!` line, else what the first line gives away
(`package main`, `<?xml`, `diff --git`), else a vim or Emacs modeline. C, C++, C#,
CSS, diffs, Dockerfiles, Go, HTML, INI and systemd units, Java, JavaScript, JSON,
Lua, make, Markdown, Nix, Python, Ruby, Rust, shell, SQL, TOML, TypeScript, XML and
YAML are known; anything else shows plain. Only the lines that fit are read and
highlighted, so a large file previews as fast as a small one.

<img src="/launcher-preview-window-mode.png" alt="The window mode previewing a window: its picture, title, app and workspace">
<img src="/launcher-preview-clipboard-mode.png" alt="The clipboard mode previewing a copied text in full">
<img src="/launcher-preview-clipboard-mode-other.png" alt="The clipboard mode previewing an entry by its type and size">
<img src="/launcher-preview-search-mode.png" alt="The search panel previewing a file">

## Clipboard history

The shell remembers what you copy, in memory, up to
[`clipboard.history-mib`](/config/clipboard). A selection a password manager marks
secret is never remembered, and so can never be pinned or written to disk.

The actions panel (`Ctrl+K`) on a clipboard row pins it. A pinned entry is
listed first, marked Pinned, and kept in
`$XDG_DATA_HOME/lys-shell/clipboard-pinned/` (files only you can read), so it
survives restarts and logging out with its bytes and type exactly as copied; the
budget never forgets it to make room. Pins have their own budget,
`clipboard.pinned-mib`, and a pin past it is refused with the reason in the
footer. Unpin returns the entry to the rest of the history. `Ctrl+Backspace`
forgets a row, pinned or not, and `Clear Unpinned` forgets everything else.

With `clipboard.persist: true` the whole history is kept, in
`$XDG_DATA_HOME/lys-shell/clipboard-history/`; turning it off removes that
folder's entries. Stored entries are read back only when the launcher first
lists them, so they cost nothing at login.

In `lys-shell launcher -show clipboard`, `kb-custom-1` forgets the selected entry,
`kb-custom-2` every unpinned one, and `kb-custom-3` pins or unpins it.

## Emoji and characters

Person and hand emoji show in `launcher.emoji.skin-tone` (`none`, the default
yellow, or `light`, `medium-light`, `medium`, `medium-dark`, `dark`). The actions
panel (`Ctrl+K`) on one of them lists all five tones; typing one makes it the new
default, saved to the config.

`lys-shell launcher -show emoji` opens every emoji as a grid: type to filter by name
or keyword, arrows move in both directions, the footer names the selected one,
`Enter` types it into the window you came from and `Ctrl+Enter` copies it.

`lys-shell launcher -show unicode` opens the search on every named character:
arrows, maths, currency signs, box drawing, IPA, combining marks (shown on a dotted
circle) and invisible ones such as `zero width joiner` or `nbsp`. A query matches
the starts of the words of a character's name or its aliases, `U+2192` finds that
code point and a character typed on its own finds its name. `Enter` types the
character; the actions copy it, its code point (`U+2192`) or its HTML entity
(`&#x2192;`). The names come from the Unicode Character Database, built into the
binary and read the first time you search them.

## The wallpaper picker

`lys-shell launcher -show wallpaper`, or the Choose Wallpaper row of the search
panel, opens a grid of the images in `wallpaper.picker-directories` (by default
the cycling directory and the `Wallpapers` folder of your pictures directory),
filtered by name as you type. Arrow keys move through the grid; where the
selection rests, the monitor the picker opened on (`launcher.monitor`, else the
focused one) shows it, and with `styling.theme-provider: wallpaper` the shell
re-themes from it too. Enter keeps it for that monitor, Ctrl+Enter for every
monitor, and Esc puts back what was there. The choice is saved to the config.

Thumbnails are the freedesktop large thumbnails in
`$XDG_CACHE_HOME/thumbnails/large`, made for the cells in view the first time and
read back from there afterwards, so a file manager shares them.

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
did, on their own surface. `-show search` is the panel by name; `-show emoji` is the
emoji grid above rather than a list.
