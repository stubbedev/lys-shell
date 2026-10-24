package portal

import (
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// Vardict is a portal a{sv}: every interactive method's options and
// results.
type Vardict = map[string]dbus.Variant

// Response codes of an interactive method's (u, a{sv}) reply.
const (
	ResponseSuccess   uint32 = 0
	ResponseCancelled uint32 = 1
	ResponseOther     uint32 = 2
)

// optU32 reads a u32 option, also accepting a u64 that fits.
func optU32(options Vardict, key string) (uint32, bool) {
	switch v := options[key].Value().(type) {
	case uint32:
		return v, true
	case uint64:
		if v <= 1<<32-1 {
			return uint32(v), true
		}
	}
	return 0, false
}

// optBool reads a bool option.
func optBool(options Vardict, key string) (value, ok bool) {
	value, ok = options[key].Value().(bool)
	return value, ok
}

// optString reads a string option.
func optString(options Vardict, key string) (string, bool) {
	s, ok := options[key].Value().(string)
	return s, ok
}

// stringOr is optString with a fallback for a missing or mistyped key.
func stringOr(options Vardict, key, fallback string) string {
	if s, ok := optString(options, key); ok {
		return s
	}
	return fallback
}

// serializedIcon splits a g_icon_serialize() icon, (sv), into its kind
// and value; false for any other shape.
func serializedIcon(v dbus.Variant) (kind string, value dbus.Variant, ok bool) {
	parts, ok := v.Value().([]any)
	if !ok || len(parts) != 2 {
		return "", dbus.Variant{}, false
	}
	kind, kindOK := parts[0].(string)
	value, valueOK := parts[1].(dbus.Variant)
	if !kindOK || !valueOK {
		return "", dbus.Variant{}, false
	}
	return kind, value, true
}

// optChoices reads a choices option, a(ssa(ss)s); a malformed one, or
// a list choice without options' ids and labels, is an error.
func optChoices(options Vardict) ([]portalreq.Choice, error) {
	v, ok := options["choices"]
	if !ok {
		return nil, nil
	}
	var choices []portalreq.Choice
	if err := dbus.Store([]any{v.Value()}, &choices); err != nil {
		return nil, fmt.Errorf("malformed choices: %w", err)
	}
	for _, c := range choices {
		if c.ID == "" || c.Label == "" {
			return nil, fmt.Errorf("a choice without an id or label: %+v", c)
		}
		for _, o := range c.Options {
			if o.ID == "" || o.Label == "" {
				return nil, fmt.Errorf("choice %s has an option without an id or label", c.ID)
			}
		}
	}
	return choices, nil
}
