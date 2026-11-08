---
title: Themes
---

# Themes

A theme is a palette of ten colors. The shell's colors come from one of:

- a built-in theme or a theme file, picked in lys-settings, which writes it to [`styling.palette`](/config/styling);
- `styling.palette` set by hand;
- a wallpaper-driven provider set by [`styling.theme-provider`](/config/styling): `wallpaper` (built in), `matugen`, `pywal` or `wallust` (each needs its tool installed).

`styling.appearance: light` or `dark` switches a built-in palette to its light or dark variant where one exists, and sets the mode of the providers.

## Built-in themes

`lys-shell` (default), `catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte`, `dracula`, `everforest-dark`, `everforest-dark-hard`, `everforest-dark-soft`, `everforest-light`, `everforest-light-hard`, `everforest-light-soft`, `gruvbox-dark`, `gruvbox-dark-hard`, `gruvbox-dark-soft`, `gruvbox-light`, `gruvbox-light-hard`, `gruvbox-light-soft`, `kanagawa-wave`, `kanagawa-dragon`, `kanagawa-lotus`, `monokai`, `monokai-pro-classic`, `monokai-pro-octagon`, `monokai-pro-machine`, `monokai-pro-ristretto`, `monokai-pro-spectrum`, `nightfox-carbonfox`, `nightfox-nightfox`, `nightfox-duskfox`, `nightfox-nordfox`, `nightfox-terafox`, `nightfox-dayfox`, `nord`, `one-dark`, `one-light`, `rose-pine-main`, `rose-pine-moon`, `rose-pine-dawn`, `solarized-dark`, `solarized-light`, `tokyo-night-night`, `tokyo-night-storm`, `tokyo-night-moon`, `tokyo-night-day`.

## Theme files

Each `*.yaml` or `*.yml` file in `~/.config/lys-shell/themes/` is a theme named after the file. All ten keys are required, as hex colors:

```yaml
# ~/.config/lys-shell/themes/my-theme.yaml
bg: "#1a1b26"        # darkest background
surface: "#24283b"   # cards and sidebars
elevated: "#2f334d"  # raised elements
fg: "#c0caf5"        # primary text
fg_muted: "#9aa5ce"  # secondary text
primary: "#7aa2f7"   # accent
red: "#f7768e"
yellow: "#e0af68"
green: "#9ece6a"
blue: "#7aa2f7"
```

A file that does not parse, misses a key, or is named like an existing theme is skipped and logged. Changes to the directory are picked up live.

Config fields of type [`ColorValue`](/config/types#color-value) refer to palette colors by token.
