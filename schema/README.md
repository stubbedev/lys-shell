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

The same schema works for both YAML and TOML configs — it gives autocomplete,
inline docs, and validation in editors that speak the Language Server Protocol.

### YAML (`config.yaml`)

Add a `yaml-language-server` modeline at the top of the file:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/stubbedev/lys-shell/master/schema/lys-shell-config.schema.json
```

Or point at a local checkout:

```yaml
# yaml-language-server: $schema=./schema/lys-shell-config.schema.json
```

### TOML (`config.toml`)

lys-shell already writes a `tombi.toml` into the config directory that associates
`config.toml` with the generated `schema.json` (see `lys-shell config schema`).
Editors using Taplo can instead add a directive at the top of the file:

```toml
#:schema https://raw.githubusercontent.com/stubbedev/lys-shell/master/schema/lys-shell-config.schema.json
```
