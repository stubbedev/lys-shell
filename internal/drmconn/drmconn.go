// Package drmconn names DRM connectors from their sysfs entries, the
// names compositors give their outputs.
package drmconn

import "strings"

// Dir is the sysfs class directory of DRM cards and connectors.
const Dir = "/sys/class/drm"

// Name is the connector a sysfs entry such as card1-DP-3 is, DP-3; an
// entry that is no connector (a card, a render node) is not ok.
func Name(entry string) (string, bool) {
	rest, ok := strings.CutPrefix(entry, "card")
	if !ok {
		return "", false
	}
	card, name, ok := strings.Cut(rest, "-")
	if !ok || card == "" || name == "" || strings.TrimLeft(card, "0123456789") != "" {
		return "", false
	}
	return name, true
}
