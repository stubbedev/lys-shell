package appicons

import (
	"strings"

	"github.com/stubbedev/lys-shell/internal/glob"
)

// lowered is Default with every pattern lowercased (icons.rs
// compiled_map): Lookup lowercases the name, so the table must match in
// the same case or a mixed-case entry ("Slack", "Tor Browser") never
// does.
var lowered = func() []Entry {
	out := make([]Entry, len(Default))
	for i, e := range Default {
		out[i] = Entry{Pattern: strings.ToLower(e.Pattern), Icon: e.Icon}
	}
	return out
}()

// Lookup is icons.rs lookup_app_icon: the first Default pattern that
// matches the name, both lowercased (an exact match counts too, as
// matches_glob's shortcut does). Callers pass app ids, window classes,
// or player identities alike.
func Lookup(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, entry := range lowered {
		if lower == entry.Pattern || glob.Wildcard(entry.Pattern, lower) {
			return entry.Icon, true
		}
	}
	return "", false
}
