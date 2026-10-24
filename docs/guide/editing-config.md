---
title: Editing config
---

# Editing config

lys-shell reads configuration from `~/.config/lys-shell/config.yaml` (`config.yml` is accepted too). All fields have defaults, so the config file may be empty; declare only the fields that should override a default.

The `lys-shell config` CLI and the settings GUI write to `~/.config/lys-shell/runtime.yaml` rather than `config.yaml`. For each field, lys-shell uses the first value defined among these sources:

1. `runtime.yaml` - overrides written by `lys-shell config` or the settings GUI.
2. `config.yaml` - values declared by hand.
3. The built-in default.

A minimal override:

```yaml
bar:
  scale: 1.25
modules:
  clock:
    format: "%H:%M"
```

Every supported key is documented in the [config reference](/config/).

## Migrating from TOML

Earlier releases read `config.toml`. It is no longer read: when a `config.toml` exists without a `config.yaml`, lys-shell reports an error asking for it to be converted. The keys are unchanged, so converting is a matter of syntax: `[section]` tables become nested mappings, `[[bar.layout]]` arrays of tables become a list under `bar: layout:`, and `key = value` becomes `key: value`. Quote strings YAML would otherwise misread, such as `"*"`, hex colors like `"#1e1e2e"` and anything starting with `%` or `{`. Imported files and `themes/*.toml` need the same conversion.

## Pixel sizes

Any size field — bar insets and padding, the module gap, button icon/label sizes and paddings, and per-module icon, label, gap, and padding sizes — accepts either a scale multiplier (a bare number, multiplied by the bar scale) or an absolute pixel length written as a string:

```yaml
bar:
  button-icon-size: "20px"   # absolute pixels, ignores bar scale
  button-gap: 1.5            # scale multiplier (default form)
```

Pixel values are logical pixels: GTK scales them for HiDPI displays automatically, so `"20px"` renders at the correct physical size on a 2× monitor while ignoring the configurable `bar.scale` multiplier.

## Imports

`config.yaml` may declare a top-level `imports` list to load additional YAML files. Files referenced through `imports` may themselves declare `imports`, forming a chain:

```yaml
imports: [themes/nord.yaml, modules/clock.yaml]
bar:
  scale: 1.25
```

Paths are resolved relative to the importing file's directory; the extension may be omitted, in which case `.yaml` is tried, then `.yml`. Imports are merged in declaration order, then the importing file is overlaid on top. Mappings merge key by key; scalars and lists in the overlay replace the corresponding value in the base. The merged result becomes the `config.yaml` layer described above.

`runtime.yaml` does not resolve imports. Circular chains are rejected at load; the previous valid configuration remains active and the error is recorded in the log.

## Editor setup

On startup, lys-shell writes a JSON Schema for the configuration to `~/.config/lys-shell/schema.json`. The schema is generated from the installed binary and matches the version of lys-shell on disk. Any YAML language server with JSON Schema support can use it for completion, hover documentation, and validation.

The [YAML extension by Red Hat](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) provides this in VS Code; Neovim, Helix, and Zed use [`yaml-language-server`](https://github.com/redhat-developer/yaml-language-server). The stub `config.yaml` lys-shell creates starts with a modeline pointing the server at the schema; add the same line to the top of your own file:

```yaml
# yaml-language-server: $schema=./schema.json
```

The same schema is published in the repository at [`schema/lys-shell-config.schema.json`](https://github.com/stubbedev/lys-shell/blob/master/schema/lys-shell-config.schema.json) for files outside the config directory. You can regenerate the schema for the exact version you run with `lys-shell config schema --stdout`.

## Live reload

lys-shell watches the configuration directory. Changes to `config.yaml` trigger an in-process reload; a shell restart is not required. Invalid configuration is rejected, the previous valid state is retained, and parse or validation errors are recorded in the log.

## Editing from the CLI

The `lys-shell config` subcommand reads and writes individual fields by dotted path:

```bash
lys-shell config get bar.scale
lys-shell config set modules.clock.format "%H:%M"
lys-shell config reset modules.clock.format
```

`set` writes to `~/.config/lys-shell/runtime.yaml`; `config.yaml` is never modified by the CLI or GUI Settings dialog. `reset` removes the runtime override for the given path, reverting the field to the value declared in `config.yaml` or to the built-in default.

`VALUE` is parsed as a YAML value, so `true`, `5`, `[a, b]` and `{module: clock}` set a boolean, a number, a list and a mapping; anything that does not parse as YAML is stored as a plain string.

## Printing the default configuration

`lys-shell config default --stdout` prints every key with its default value to standard output. Without `--stdout`, the command writes `config.yaml.example` to the configuration directory; `config.yaml` is not modified. The output is YAML. `lys-shell config schema --stdout` prints the JSON Schema in the same manner.
