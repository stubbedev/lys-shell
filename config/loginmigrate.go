package config

import (
	"log"
	"strings"
)

// loginMoves is where each key of the former lock and greeter sections
// lives in login. Lock comes first: where both set a key the screens now
// share, the lock screen's value is kept.
var loginMoves = []struct {
	section string
	keys    map[string]string
}{
	{"lock", map[string]string{
		"enabled": "lock.enabled", "lock-on-start": "lock.lock-on-start",
		"background-mode": "background-mode", "background-image": "background-image", "background-color": "background-color",
		"blur": "blur", "show-clock": "show-clock", "clock-format": "clock-format", "date-format": "date-format",
		"grace-period-ms": "lock.grace-period-ms", "max-attempts": "max-attempts",
		"show-failed-attempts": "show-failed-attempts", "blank-timeout-ms": "lock.blank-timeout-ms",
		"pam-service": "lock.pam-service",
	}},
	{"greeter", map[string]string{
		"background-mode": "background-mode", "background-image": "background-image", "background-color": "background-color",
		"show-clock": "show-clock", "clock-format": "clock-format", "date-format": "date-format",
		"show-user-list": "greeter.show-user-list", "show-power-buttons": "greeter.show-power-buttons",
		"cursor-theme": "greeter.cursor-theme", "cursor-size": "greeter.cursor-size",
	}},
}

// migrateLogin moves a config tree's former lock and greeter sections
// into login, warning once per moved key. A key login already sets keeps
// login's value, and a key the old sections never had is dropped; both
// warn too. A tree without the old sections is returned as it is; one
// with them is copied, never changed in place.
func migrateLogin(tree any) any {
	keys, get, ok := tableEntries(tree)
	if !ok {
		return tree
	}
	if _, lock := lookup(tree, "lock"); !lock {
		if _, greeter := lookup(tree, "greeter"); !greeter {
			return tree
		}
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if k != "lock" && k != "greeter" {
			out[k] = get(k)
		}
	}
	login := plainCopy(out["login"])
	for _, mv := range loginMoves {
		old, ok := lookup(tree, mv.section)
		if !ok {
			continue
		}
		oldKeys, oldGet, ok := tableEntries(old)
		if !ok {
			log.Printf("config: %s is not a table and is no longer read; its settings are in login", mv.section)
			continue
		}
		for _, k := range oldKeys {
			dest, known := mv.keys[k]
			switch {
			case !known:
				log.Printf("config: %s.%s is not a login screen setting; it is dropped", mv.section, k)
			case hasPath(login, dest):
				log.Printf("config: %s.%s is ignored: login.%s is set", mv.section, k, dest)
			default:
				setPlain(login, dest, oldGet(k))
				log.Printf("config: %s.%s moved to login.%s; rename it in the config", mv.section, k, dest)
			}
		}
	}
	out["login"] = login
	return out
}

// plainCopy is a table-like value as a fresh map, so setPlain never
// writes into a tree it was handed; anything else starts an empty one.
func plainCopy(v any) map[string]any {
	keys, get, ok := tableEntries(v)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = get(k)
	}
	return out
}

// setPlain sets the dot path in m, copying each nested table it descends
// into.
func setPlain(m map[string]any, path string, v any) {
	head, rest, nested := strings.Cut(path, ".")
	if !nested {
		m[head] = v
		return
	}
	child := plainCopy(m[head])
	m[head] = child
	setPlain(child, rest, v)
}
