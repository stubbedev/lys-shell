package bar

import (
	"fmt"
	"os"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sway"
)

// compositor is compositor.rs's Compositor: the session's compositor
// as its environment advertises it.
type compositor int

const (
	compositorUnknown compositor = iota
	compositorHyprland
	compositorNiri
	compositorMango
	compositorSway
	compositorLysWM
)

// String names the compositor for diagnostics.
func (c compositor) String() string {
	switch c {
	case compositorHyprland:
		return "Hyprland"
	case compositorNiri:
		return "niri"
	case compositorMango:
		return "mango"
	case compositorSway:
		return "sway"
	case compositorLysWM:
		return "lys-wm"
	}
	return "unknown"
}

// detectCompositor is Compositor::detect: the first advertised socket
// in the order lys-wm, then Rust's (Hyprland, niri, mango, sway), so a
// session that exports more than one is still a single compositor. An
// empty variable advertises nothing.
func detectCompositor() compositor {
	switch {
	case lyswm.IsRunning():
		return compositorLysWM
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return compositorHyprland
	case niri.IsRunning():
		return compositorNiri
	case mango.IsRunning():
		return compositorMango
	case sway.IsRunning():
		return compositorSway
	}
	return compositorUnknown
}

// requireCompositor is module_registry.rs's require_hyprland and
// siblings: module builds only under want.
func requireCompositor(module string, want compositor) error {
	if got := detectCompositor(); got != want {
		return fmt.Errorf("%s: requires %s, the compositor is %s", module, want, got)
	}
	return nil
}
