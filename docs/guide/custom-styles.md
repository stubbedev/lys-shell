# Custom styles

`~/.config/lys-shell/styles/index.css` is layered over the built-in stylesheet and applies on save. It is created with a starter comment on first run; emptying it restores the defaults. Use it for what the config does not cover: per-instance styling, spacing, hover and focus states, animations.

The file is plain CSS in lys-ui's dialect, which follows [GTK4 CSS](https://docs.gtk.org/gtk4/css-overview.html). Nesting (`&`) works; `@import` does not. `styles/index.scss` is not read; while one exists, the log reports it.

## Styling one module instance

Give the instance a class in the layout:

```yaml
bar:
  layout:
    - monitor: "*"
      center:
        - {module: clock, class: primary-clock}
        - clock
```

The class goes on the module's box; its button is `button.bar-button` inside it. Built-in rules read CSS variables such as `--bar-btn-label-color`, `--bar-btn-icon-color`, `--bar-btn-icon-size` and `--bar-btn-label-weight`. The config sets the color variables on the button, so override them on the button's content box:

```css
.primary-clock button.bar-button {
  background-color: var(--palette-primary);
}
.primary-clock button.bar-button > box {
  --bar-btn-label-color: var(--palette-bg);
  --bar-btn-label-weight: 700;
}
```

Classes work on any slot, including modules inside groups. A group is addressed by its name as an ID (`#status`).

## Finding selectors

The built-in rules are in [`styling/css`](https://github.com/stubbedev/lys-shell/tree/master/styling/css), grouped into modules, components and primitives. They read colors, sizes and spacing from variables; overriding a variable under a narrower selector is the most robust change.

`lys-shell panel inspect` draws the widget tree over every bar and dropdown. While it is armed:

| Key | Does |
| --- | --- |
| <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>I</kbd> | Toggle the overlay |
| <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>D</kbd> | Print the focused surface's tree (bounds, role, hover, focus and press state) to the shell's stdout |

Under systemd, stdout is in `journalctl --user -u lys-shell`. `LYS_UI_INSPECT=1` arms the inspector at startup.

A dropdown closes on a click outside it. Set `bar.dropdown-autohide: false` to keep one open while inspecting it.
