package bar

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
)

// advertise sets exactly the named compositor variables, hiding the
// rest the host session exports.
func advertise(t *testing.T, set ...string) {
	t.Helper()
	clearCompositorEnv(t)
	for _, name := range set {
		t.Setenv(name, "/run/test-"+name)
	}
}

func TestDetectCompositorTakesTheFirstAdvertised(t *testing.T) {
	for _, tc := range []struct {
		set  []string
		want compositor
	}{
		{nil, compositorUnknown},
		{[]string{"SWAYSOCK"}, compositorSway},
		{[]string{mango.SocketEnv}, compositorMango},
		{[]string{niri.SocketEnv}, compositorNiri},
		{[]string{"HYPRLAND_INSTANCE_SIGNATURE"}, compositorHyprland},
		{[]string{"HYPRLAND_INSTANCE_SIGNATURE", "SWAYSOCK"}, compositorHyprland},
		{[]string{niri.SocketEnv, mango.SocketEnv, "SWAYSOCK"}, compositorNiri},
		{[]string{mango.SocketEnv, "SWAYSOCK"}, compositorMango},
		{[]string{lyswm.SocketEnv}, compositorLysWM},
		{[]string{lyswm.SocketEnv, "HYPRLAND_INSTANCE_SIGNATURE", niri.SocketEnv}, compositorLysWM},
	} {
		advertise(t, tc.set...)
		if got := detectCompositor(); got != tc.want {
			t.Errorf("%v: detected %v, want %v", tc.set, got, tc.want)
		}
	}
}

func TestRequireCompositorNamesBothSides(t *testing.T) {
	advertise(t, "SWAYSOCK")
	if err := requireCompositor("sway-workspaces", compositorSway); err != nil {
		t.Errorf("sway under sway: %v", err)
	}
	err := requireCompositor("niri-workspaces", compositorNiri)
	if err == nil {
		t.Fatal("niri-workspaces under sway: want an error")
	}
	if msg := err.Error(); !strings.Contains(msg, "niri-workspaces") || !strings.Contains(msg, "requires niri") || !strings.Contains(msg, "sway") {
		t.Errorf("error %q, want the module, the wanted and the running compositor", msg)
	}
}

// A Hyprland session that also exports SWAYSOCK is Hyprland: the sway
// module is skipped, as Compositor::detect decides it.
func TestSwayWorkspacesSkippedUnderHyprland(t *testing.T) {
	fakeI3(t, "[]", `{"id":1,"type":"root","nodes":[],"floating_nodes":[]}`)
	ctx := newTestContext(t, config.Defaults())
	if _, err := Create("sway-workspaces", ctx); err != nil {
		t.Fatalf("sway-workspaces under sway: %v", err)
	}
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "/run/test-hyprland")
	if _, err := Create("sway-workspaces", ctx); err == nil {
		t.Error("sway-workspaces under Hyprland: want an error, got a module")
	}
}
