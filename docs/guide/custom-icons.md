# Custom icons

Icon fields such as `icon-name: tb-home-symbolic` resolve against lys-shell's icon theme in `~/.local/share/lys-shell/icons/` (`$XDG_DATA_HOME`), then `lys-shell/icons` under each `$XDG_DATA_DIRS` entry and `/usr/share/lys-shell/icons`. Icons installed while the shell runs resolve without a restart.

The bundled icons are [Lucide](https://lucide.dev/icons) (`ld-`) and [Simple Icons](https://simpleicons.org) (`si-`). Everything else is installed with `lys-shell icons`.

## Sources

`lys-shell icons sources` lists them. Slugs are the names on the upstream site.

| Source | Prefix | Contents | Browse |
|---|---|---|---|
| `tabler` | `tb-` | Outline UI icons | [tabler.io/icons](https://tabler.io/icons) |
| `tabler-filled` | `tbf-` | Filled UI icons | [tabler.io/icons](https://tabler.io/icons) |
| `simple-icons` | `si-` | Brand logos | [simpleicons.org](https://simpleicons.org) |
| `material` | `md-` | Material Symbols (outlined) | [fonts.google.com/icons](https://fonts.google.com/icons) |
| `lucide` | `ld-` | UI icons | [lucide.dev/icons](https://lucide.dev/icons) |

An installed icon is named `<prefix>-<slug>-symbolic`:

```sh
lys-shell icons install tabler home settings bell   # tb-home-symbolic, ...
lys-shell icons install simple-icons firefox        # si-firefox-symbolic
```

## Local SVGs

```sh
lys-shell icons import ~/Downloads/my-logo.svg my-logo   # cm-my-logo-symbolic
lys-shell icons import ~/exported-icons/                  # every SVG in the folder
```

A single file needs a name and gets the `cm-` prefix. In a folder, files already named with a known prefix (`tb-`, `tbf-`, `si-`, `md-`, `ld-`, `cm-`) keep their name; the rest get `cm-`. Imported SVGs are converted to symbolic icons so they take the theme's colors.

## Managing

| Command | Does |
| --- | --- |
| `icons list [--source tb] [--interactive]` | List installed icons, by prefix; `--interactive` searches with `fzf` |
| `icons remove tb-home-symbolic ...` | Remove icons by full name |
| `icons sync [--dry-run]` | Install every CDN icon the config names that is missing |
| `icons export <dir>` | Copy all installed icons to a folder |
| `icons open` | Open the icons folder in the file manager |
| `icons setup` | Install the bundled icons (needed for source builds) |

## Using icons

Give the full name, prefix and suffix included:

```yaml
modules:
  clock:
    icon-name: ld-clock-symbolic
```

At startup and on every reload, the shell logs each icon the config names that is not installed, with the command that gets it: `icons sync` for a CDN icon, `icons import` for a `cm-` icon.
