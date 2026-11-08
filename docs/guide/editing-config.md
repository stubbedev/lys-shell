---
title: Editing config
---

# Editing config

## Files and layers

All files live in `$XDG_CONFIG_HOME/lys-shell/` (default `~/.config/lys-shell/`).

| File | Written by | Role |
| --- | --- | --- |
| `config.yaml` (or `config.yml`) | you | Your settings. Created with only the schema modeline when missing. |
| `runtime.yaml` | `lys-shell config`, lys-settings | Overrides on top of `config.yaml`. |
| `schema.json` | the shell, at startup | JSON Schema for editor completion. |
| `.env`, `.*.env` | you | Environment variables for `$NAME` secrets. |
| `themes/*.yaml` | you | Custom [themes](/guide/themes). |
| `styles/index.css` | you | [Custom CSS](/guide/custom-styles). |

Each field takes the first value found in `runtime.yaml`, then `config.yaml`, then the built-in default. Every field has a default, so declare only what you change:

```yaml
bar:
  scale: 1.25
modules:
  clock:
    format: "%H:%M"
```

Every key is in the [config reference](/config/). lys-shell does not read TOML: a `config.toml` without a `config.yaml` (or a `runtime.toml` without a `runtime.yaml`) is reported as an error until converted.

## Live reload

The config directory is watched; saving a file applies it to the running shell. A file that fails to load (bad YAML, a missing or circular import) keeps the running config, or the defaults at startup. A field with an invalid value keeps its default while the rest of the file applies. Both are reported in the log.

## Sizes

Every size field takes either a scale multiplier (a number; on the bar it is multiplied by `bar.scale`) or logical pixels as a string:

```yaml
bar:
  button-icon-size: "20px"   # pixels, ignores bar.scale
  button-gap: 1.5            # multiplier
```

Logical pixels are scaled for HiDPI outputs.

## Imports

A top-level `imports` list loads other YAML files first:

```yaml
imports: [themes/nord.yaml, modules/clock.yaml]
bar:
  scale: 1.25
```

- Paths are relative to the importing file. Without an extension, `.yaml` is used, or `.yml` when only that exists.
- Imports merge in order, then the importing file on top. Mappings merge key by key; scalars and lists replace.
- Imported files may import others. A cycle is an error.
- `runtime.yaml` does not resolve imports.

## Secrets

A field of type [`Secret`](/config/types#secret) (API keys, for example) takes `$NAME` to read the environment variable `NAME`. `.env` and `.*.env` files in the config directory are loaded into the environment at startup and on change; a variable already set in the environment is kept.

## Editor setup

Point a YAML language server at `schema.json` with this first line (the created `config.yaml` has it):

```yaml
# yaml-language-server: $schema=./schema.json
```

VS Code needs the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml); Neovim, Helix and Zed use [`yaml-language-server`](https://github.com/redhat-developer/yaml-language-server). The repository carries the same schema at [`schema/lys-shell-config.schema.json`](https://github.com/stubbedev/lys-shell/blob/master/schema/lys-shell-config.schema.json).

## CLI

```sh
lys-shell config get bar.scale
lys-shell config set modules.clock.format "%H:%M"
lys-shell config reset modules.clock.format
lys-shell config default --stdout   # every key with its default
lys-shell config schema --stdout    # the JSON Schema
```

- `set` writes `runtime.yaml`. The value is parsed as YAML (`true`, `5`, `[a, b]`, `{module: clock}`); anything that does not parse is a string.
- `reset` removes the override from `runtime.yaml`.
- `default` and `schema` without `--stdout` write `config.yaml.example` and `schema.json` into the config directory.
- `config.yaml` is never written by the CLI or lys-settings.
