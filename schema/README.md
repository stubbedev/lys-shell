# lys-shell config JSON Schema

`lys-shell-config.schema.json` is the JSON Schema for the complete lys-shell
configuration. It is generated from the config types in `config/` and kept current by
`just check` (its `schema-check` step fails if this file drifts from the
generated output).

Regenerate it after changing any config type:

```sh
just schema
```

## Editor hints

The schema gives autocomplete, inline docs, and validation for `config.yaml` in
editors running the YAML language server (the Red Hat "YAML" extension in VS
Code, `yaml-language-server` in Neovim, Helix, or Zed).

lys-shell writes the schema for the installed version to `schema.json` in the
config directory (see `lys-shell config schema`), and the stub `config.yaml` it
creates starts with a modeline pointing at it. Add the same line to the top of
your own file:

```yaml
# yaml-language-server: $schema=./schema.json
```

Or point at the published copy:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/stubbedev/lys-shell/master/schema/lys-shell-config.schema.json
```
