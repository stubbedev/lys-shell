package jinja

import (
	"maps"
	"slices"
	"strings"
)

// ReplaceVars substitutes the exact "{{ name }}" spelling of each of vars'
// names in one pass, without the template language: the Rust modules'
// plain replacements.
func ReplaceVars(format string, vars map[string]string) string {
	pairs := make([]string, 0, 2*len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		pairs = append(pairs, "{{ "+name+" }}", vars[name])
	}
	return strings.NewReplacer(pairs...).Replace(format)
}
