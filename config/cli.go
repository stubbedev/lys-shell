package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/atomicfile"
	"go.yaml.in/yaml/v3"
)

// Helpers behind `lys-shell config`, kept here so the output forms live
// next to the tree they format.

// FormatGetValue renders a `lys-shell config get` result: scalars bare
// (strings unquoted, floats in their shortest form), tables as a YAML
// document, arrays inline.
func FormatGetValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float64:
		return rustFloat(t, 64)
	case float32:
		return rustFloat(float64(t), 32)
	case bool:
		return strconv.FormatBool(t)
	}
	if s, err := yamlDocument(v); err == nil {
		return strings.TrimSuffix(s, "\n")
	}
	return YAMLInline(v)
}

// ParseCLIValue reads a `lys-shell config set` value as a YAML value
// ("true", "5", "[1, 2]", "{module: clock}", "'quoted'"). Anything that
// is not valid YAML, is empty or null, or reads as a mapping without
// being written as a flow mapping ("Now: playing") is taken as a plain
// string.
func ParseCLIValue(raw string) any {
	v, err := parseYAMLValue([]byte(raw))
	if err != nil || v == nil {
		return raw
	}
	if _, ok := v.(map[string]any); ok && !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return raw
	}
	return v
}

// parseYAMLValue parses one YAML value of any kind; an empty document
// is nil.
func parseYAMLValue(data []byte) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return yamlValue(doc.Content[0])
}

// DefaultYAML renders the default configuration as a YAML document
// (the `lys-shell config default` output).
func DefaultYAML() (string, error) {
	out, err := yamlDocument(encode(Defaults()))
	if err != nil {
		return "", fmt.Errorf("render the default config: %w", err)
	}
	return out, nil
}

// ExamplePath is config.yaml.example in dir.
func ExamplePath(dir string) string { return filepath.Join(dir, "config.yaml.example") }

// SchemaPath is schema.json in dir, which the config stub's
// yaml-language-server modeline points editors at.
func SchemaPath(dir string) string { return filepath.Join(dir, "schema.json") }

// EnsureSchemaCurrent writes schema.json unless it already holds what
// this binary generates.
func EnsureSchemaCurrent(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the user's config dir
		return fmt.Errorf("create the config dir %s: %w", dir, err)
	}
	schemaPath := SchemaPath(dir)
	current := []byte(GenerateSchema())
	existing, err := os.ReadFile(schemaPath) //nolint:gosec // schema.json in the config dir
	if err == nil && bytes.Equal(existing, current) {
		return nil
	}
	if err := atomicfile.WriteFile(schemaPath, current, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", schemaPath, err)
	}
	return nil
}
