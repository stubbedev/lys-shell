# Custom styles

`~/.config/lys-shell/styles/index.scss` is an escape hatch for CSS tweaks that aren't exposed through `config.toml` or the settings GUI. The file is created with a starter comment the first time lys-shell runs. Anything written into it is layered on top of the built-in stylesheet, and saved changes apply to the running shell automatically.

For things the config already covers (palette, fonts, global scale, rounding, bar layout, module visibility), use the normal config or the settings GUI. This file is for the rest: per-instance module styling, animation behavior, padding and spacing tweaks, hover and focus state changes, custom keyframe animations.

## Per-instance module styling

Two clock modules on the same bar share every config-level styling option. To style them differently, give one of them a CSS class in the layout entry by writing it as a table instead of a bare string:

```toml
# config.toml
[[bar.layout]]
monitor = "*"
center = [
  { module = "clock", class = "primary-clock" },
  "clock",
]
```

The class is added to that module's root widget. The selector to target it from `index.scss` is `menubutton.bar-button.primary-clock` (every module is a `menubutton.bar-button`; the custom class is appended).

The robust way to change appearance on that one instance is to override the CSS variables it consumes. The built-in rules read from variables like `--bar-btn-bg`, `--bar-btn-label-color`, `--bar-btn-icon-size`, and `--bar-btn-label-weight`. Redefining one of those inside the per-instance selector replaces it for that widget only:

```scss
// ~/.config/lys-shell/styles/index.scss
menubutton.bar-button.primary-clock {
  --bar-btn-bg: var(--palette-primary);
  --bar-btn-label-color: var(--palette-bg);
  --bar-btn-label-weight: 700;
}
```

Other modules continue to use the defaults.

The same `class = "..."` field can be applied to any item in `left`, `center`, or `right`, and to items nested inside groups.

## Finding selectors and variables

The built-in stylesheet is the reference for selectors and variables: its sources live in [`styling/scss`](https://github.com/stubbedev/lys-shell/tree/master/styling/scss), one partial per bar module and dropdown. Every rule there reads its colors, sizes and spacing from CSS variables, and overriding a variable inside a narrower selector is the robust way to change one thing.

To see which widget sits where, open the inspector on the running shell:

```sh
lys-shell panel inspect
```

It draws the widget tree over every bar and dropdown. While it is open, <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>I</kbd> toggles the overlay and <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>D</kbd> prints the focused surface's tree, with each widget's bounds, role and hover, focus and press state, to the shell's stdout (`journalctl --user -u lys-shell` when it runs as a service). Starting the shell with `LYS_UI_INSPECT=1` arms the inspector from the start.

### Inspecting dropdowns

Dropdowns close as soon as focus leaves the bar. To keep one open while inspecting it, disable autohide first. Either toggle **Bar → Dropdown → Dropdown Autohide** in the settings GUI, or set this in `config.toml`:

```toml
[bar]
dropdown-autohide = false
```

Open the dropdown, then run `lys-shell panel inspect`. Re-enable autohide afterwards.

lys-shell renders with [lys-ui](https://github.com/stubbedev/lys-ui), which implements GTK4's CSS dialect: a subset of web CSS. See [docs.gtk.org/gtk4/css-overview.html](https://docs.gtk.org/gtk4/css-overview.html) for the reference.

## Sass features

The styles directory is treated as a Sass project. `@use`, `@forward`, `@import`, mixins, variables, math, and color functions all work. Partials (`_name.scss`) can be imported from `index.scss` by basename:

```scss
// ~/.config/lys-shell/styles/index.scss
@import "modules";
```

```scss
// ~/.config/lys-shell/styles/_modules.scss
menubutton.bar-button.primary-clock {
  --bar-btn-bg: var(--palette-primary);
}
```

Any `.scss` or `.css` change anywhere in the directory triggers a reload.

## Disable user styles

Empty the contents of `~/.config/lys-shell/styles/index.scss` to revert to defaults.
