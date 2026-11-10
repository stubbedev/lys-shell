---
title: Launcher
---

# Launcher

`lys-shell launcher` toggles the search panel: one field and one ranked list over every source below. The shell must be running; it hosts the panel. The panel opens on the focused output, or on [`launcher.monitor`](/config/launcher) (an unknown name is logged and the focused output used).

Bind it in your compositor. lys-wm's default config binds `SUPER + Space`.

```ini
# sway
bindsym $mod+space exec lys-shell launcher
# Hyprland
bind = SUPER, SPACE, exec, lys-shell launcher
```

## Sources

[`launcher.search.providers`](/config/types#launcher-search-config) selects them.

| Source | Notes |
| --- | --- |
| Applications | Desktop entries; desktop actions are in the actions panel |
| Windows | lys-wm, Hyprland, Sway, Niri and Mango |
| Files | Recent files and the [file index](#file-index); a query starting with `/` or `~` lists that folder |
| Calculator | Arithmetic (`6*7`, `sqrt(16)`), units (`5 km to mi`, `100 f in c`, `1 GiB to MB`), [currencies, times and dates](#currencies-times-and-dates) |
| System | Lock, sleep, log out, restart, shut down, through the [`power`](/config/modules/power) commands. Ending the session asks for a second Enter |
| Settings | Pages of [lys-settings](https://github.com/stubbedev/lys-settings), when installed |
| Clipboard | From three typed letters; `Clipboard History` lists all. See [Clipboard history](#clipboard-history) |
| Emoji | From two typed letters; `Emoji Picker` lists all |
| Unicode characters | From three typed letters (`right arrow` finds →); `Unicode Characters` lists all |
| Bitwarden | `Bitwarden Vault` opens the vault, asking for the master password when locked |
| Quick links, snippets | From `launcher.search` |
| Browser bookmarks, history | Firefox and Chromium families. Bookmarks from two letters, history from three; `Browser Bookmarks` and `Most Visited Sites` list all |
| Quick toggles | Do not disturb, Wi-Fi, Bluetooth, audio output, Choose Wallpaper |
| Media | Play, pause and skip on the active player |
| Capture | Screenshots, recording, the color picker; they start once the panel has closed |
| Processes | `Kill Process` lists your processes, largest first; ending one asks for a second Enter. The compositor and the shell are not listed |
| Script commands | See [Script commands](#script-commands) |
| Shell | `>command` runs a command; the actions panel can run it in a terminal |
| Web | Always the last row: search the web for the query |

With an empty field the list suggests what you use most. Picks are remembered overall and per query, so `f` comes to mean Firefox if you keep choosing it; a use counts half as much after two weeks. The data is in `$XDG_STATE_HOME/lys-shell/launcher-frecency.json`; `launcher.history.enable: false` turns it off.

What runs in a terminal (a `Terminal=true` app, a file or link whose default app is one, a script command with `@lys.output terminal`, the run-in-terminal and open-terminal-here actions, `run` and `ssh`) uses `launcher.terminal`, else `$TERMINAL`, else the first of foot, kitty, alacritty, wezterm, ghostty, gnome-terminal, konsole and xterm on `$PATH`. With none of them nothing is run: the panel stays open and says that there is no terminal emulator.

## Keys

| Key | Does |
| --- | --- |
| `Up`, `Down`, `Ctrl+P`, `Ctrl+N`, `Ctrl+J` | Move the selection |
| `Page Up`, `Page Down` | Move by a page |
| `Enter` | The row's first action |
| `Ctrl+Enter` | The row's second action |
| `Ctrl+K` | Actions panel: everything the row can do, searchable |
| `Tab` | Complete a path or link keyword; otherwise the actions panel |
| `Ctrl+1` … `Ctrl+9` | Take that visible row |
| `Esc` | Back one level, then close |
| `Backspace` | In an empty field, back one level |
| `Ctrl+Backspace` | Forget a clipboard row |

The footer shows what `Enter` and `Ctrl+Enter` do. Right-click a row for its actions; click outside to close.

## Currencies, times and dates

| Query | Answer |
| --- | --- |
| `100 usd in eur`, `$100 to €`, `12.5 dollars in euros` | Converted at the European Central Bank reference rate, with its date |
| `15:00 PST in Copenhagen`, `3pm london to tokyo`, `9:30 am in Europe/Paris` | The time in the other zone, and the day if it differs |
| `time in Tokyo`, `now in new york` | The current time there |
| `today + 90 days`, `2026-12-24 + 2 weeks`, `3 weeks from tomorrow`, `10 days ago` | A date |
| `days until 2027-01-01`, `days since 2026-01-01`, `2026-12-24 - today` | A number of days |

- Currencies are those the ECB publishes, by code, symbol (`$`, `€`, `£`, `¥`) or name. `kr` means the krone or krona of your language.
- Rates are fetched at most once a day, never while typing, into `$XDG_CACHE_HOME/lys-shell/fx.xml`. Offline, the cached rates are used and their age shown. `launcher.search.currency-rates: false` stops fetching.
- Zone abbreviations are fixed offsets (`PST` is always UTC−8); `PT`, `MT`, `CT` and `ET` follow daylight saving. Cities and IANA names work too. A time without a zone is local.
- Adding months keeps to the month's end: January 31 plus one month is the last day of February.

## Preview pane

Once the selection rests, a pane beside the list previews it:

| Row | Preview |
| --- | --- |
| File | An image, the text of a text file, or a thumbnail |
| Snippet | Its text |
| Clipboard entry | The text, an image with its type and size, a copied file, or the type and size |
| Window | A capture of the window with its title, app and workspace |

Text is shown with line numbers and syntax highlighting in the theme's colors. The language comes from the file name, the `#!` line, the first line, or a vim or Emacs modeline. Known: C, C++, C#, CSS, diff, Dockerfile, Go, HTML, INI and systemd units, Java, JavaScript, JSON, Lua, make, Markdown, Nix, Python, Ruby, Rust, shell, SQL, TOML, TypeScript, XML, YAML. Only the visible lines are read.

The window and clipboard modes (`-show window`, `-show clipboard`) show the same pane. `launcher.search.previews: false` turns it off.

<img src="/launcher-preview-window-mode.png" alt="The window mode previewing a window: its picture, title, app and workspace">
<img src="/launcher-preview-clipboard-mode.png" alt="The clipboard mode previewing a copied text in full">
<img src="/launcher-preview-clipboard-mode-other.png" alt="The clipboard mode previewing an entry by its type and size">
<img src="/launcher-preview-search-mode.png" alt="The search panel previewing a file">

## Clipboard history

The shell keeps what you copy in memory, up to [`clipboard.history-mib`](/config/clipboard). A selection marked secret by a password manager is never kept.

- **Pin** an entry from the actions panel (`Ctrl+K`). Pinned entries are listed first and stored, byte for byte, in `$XDG_DATA_HOME/lys-shell/clipboard-pinned/` (readable only by you), within `clipboard.pinned-mib`. A pin over that budget is refused.
- **Forget** a row with `Ctrl+Backspace`; `Clear Unpinned` forgets the rest.
- `clipboard.persist: true` stores the whole history in `$XDG_DATA_HOME/lys-shell/clipboard-history/`; turning it off empties that folder. Stored entries load when first listed.

In `lys-shell launcher -show clipboard`, `kb-custom-1` forgets the entry, `kb-custom-2` every unpinned one, `kb-custom-3` pins or unpins it.

## Emoji and characters

Person and hand emoji use `launcher.emoji.skin-tone`. The actions panel on such an emoji lists every tone; picking one makes it the default and saves it to the config.

`-show emoji` opens a grid of every emoji, filtered by name or keyword. `Enter` types the selected one into the previous window; `Ctrl+Enter` copies it.

`-show unicode` searches every named character, including combining and invisible ones (`zero width joiner`, `nbsp`). A query matches the start of words in a name or alias; `U+2192` finds a code point; a typed character finds its name. `Enter` types it; the actions copy the character, its code point or its HTML entity.

## Wallpaper picker

`-show wallpaper`, or the Choose Wallpaper row, opens a grid of the images in `wallpaper.picker-directories` (default: the cycling directory and the `Wallpapers` folder in your pictures directory), filtered as you type. The selected image is previewed on the picker's monitor, and with `styling.theme-provider: wallpaper` re-themes the shell. `Enter` keeps it for that monitor, `Ctrl+Enter` for every monitor, `Esc` restores the previous one. The choice is saved to the config. Thumbnails are shared freedesktop thumbnails in `$XDG_CACHE_HOME/thumbnails/large`.

## File index

While the panel is open, an index older than 10 minutes is refreshed in the background: breadth first from each root (default: home), at most 30,000 paths, 8 levels and 3 seconds, on one filesystem, skipping hidden names and `node_modules`, `vendor`, `target`, `build`, `dist`, `venv`, `__pycache__` and `result`. It is cached in `$XDG_CACHE_HOME/lys-shell/launcher-files.idx`. Recent files from `recently-used.xbel` rank higher. Set `launcher.search.file-roots` when the home directory is too large to index fully.

## Configuration

Search options are in [`launcher.search`](/config/types#launcher-search-config):

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

With a `keyword`, `gh rust` opens the link with `rust` as the query. Panel animation follows [`animations`](/config/animations), with `animations.launcher` as an override.

## Script commands

Each executable in `~/.config/lys-shell/launcher/commands` (or `launcher.search.commands-dir`) with a `@lys.title` line is a command. Directives are comment lines at the top of the file:

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
| `@lys.title` | Required. Name shown and searched |
| `@lys.subtitle`, `@lys.keywords`, `@lys.icon` | Optional. The icon is a name or an absolute path |
| `@lys.output` | `silent` (default) runs detached, `terminal` in a terminal, `copy` waits up to 30 s and copies stdout |
| `@lys.argument NAME [optional]` | Prompted for in turn, then passed as the next argument |

Arguments are passed as argv, never through a shell. A file with an invalid directive is left out and logged with its line.

## rofi mode

With rofi flags, `lys-shell launcher` is a rofi-compatible launcher and dmenu: `-show drun`, `-dmenu`, `-e`, custom keybindings and exit codes, script modes and the options in [`launcher`](/config/launcher). Run through a symlink named `rofi`, the binary takes the same flags. `-show search` is the search panel; `-show emoji`, `-show unicode`, `-show wallpaper`, `-show clipboard`, `-show window` and `-show vault` are the modes above. `lys-shell launcher --help` lists every flag.
