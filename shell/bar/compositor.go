package bar

import (
	"fmt"
	"os"

	"github.com/stubbedev/wayle/service/mango"
	"github.com/stubbedev/wayle/service/niri"
	"github.com/stubbedev/wayle/service/sway"
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
	}
	return "unknown"
}

// detectCompositor is Compositor::detect: the first advertised socket
// in Rust's order (Hyprland, niri, mango, sway), so a session that
// exports more than one is still a single compositor. An empty
// variable advertises nothing.
func detectCompositor() compositor {
	switch {
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
