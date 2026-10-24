package config

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// YAML output for `lys-shell config default`, `get`, `set`, runtime.yaml,
// and diagnostics: tables in their encoded field order, two-space
// block indentation, and f32 values in their own width (0.35, not its
// widened f64).

// errNotADocument is the error for rendering a non-table as a
// document.
var errNotADocument = errors.New("a config document must be a table")

// yamlDocument renders a table as a block-style YAML document; a
// non-table root is an error.
func yamlDocument(v any) (string, error) {
	if !isTable(v) {
		return "", errNotADocument
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(yamlNode(v, false)); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}
	if isEmptyTable(v) {
		return "", nil
	}
	return out.String(), nil
}

func isEmptyTable(v any) bool {
	keys, _, _ := tableEntries(v)
	return len(keys) == 0
}

// YAMLInline renders a value on one line in YAML flow style (the form
// `lys-shell config set` echoes).
func YAMLInline(v any) string {
	out, err := yaml.Marshal(yamlNode(v, true))
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// YAMLDocument renders a table (map[string]any, nested tables as
// mappings) as a YAML document, the form runtime.yaml takes.
func YAMLDocument(table map[string]any) (string, error) { return yamlDocument(table) }

// yamlNode builds the node for one tree value; flow renders every
// collection inline.
func yamlNode(v any, flow bool) *yaml.Node {
	switch t := v.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(t, 10)}
	case uint64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatUint(t, 10)}
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: yamlFloat(t, 64)}
	case float32:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: yamlFloat(float64(t), 32)}
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if flow {
			n.Style = yaml.FlowStyle
		}
		for _, elem := range t {
			n.Content = append(n.Content, yamlNode(elem, flow))
		}
		return n
	}
	keys, get, ok := tableEntries(v)
	if !ok {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if flow {
		n.Style = yaml.FlowStyle
	}
	for _, k := range keys {
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			yamlNode(get(k), flow))
	}
	return n
}

// yamlFloat is the shortest round-tripping decimal for the float's
// width, with ".0" on integral values so it reads back as a float, and
// YAML's .nan/.inf spellings.
func yamlFloat(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return ".nan"
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	}
	s := rustFloat(f, bits)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}
