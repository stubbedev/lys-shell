package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/app"
)

// focusOn reports name as the focused output, none for "".
func focusOn(name string) func() (string, bool) {
	return func() (string, bool) { return name, name != "" }
}

// A portal dialog maps on the focused output; without one known (or
// with it gone), on the first; with no output, nowhere in particular.
func TestDialogOutputFollowsTheFocus(t *testing.T) {
	dp1, dp2 := &app.Output{Name: "DP-1"}, &app.Output{Name: "DP-2"}
	outs := []*app.Output{dp1, dp2}
	if o := dialogOutput(outs, focusOn("DP-2")); o != dp2 {
		t.Errorf("focused DP-2 = %v", o)
	}
	for _, name := range []string{"", "HDMI-9"} {
		if o := dialogOutput(outs, focusOn(name)); o != dp1 {
			t.Errorf("focus %q = %v, want the first output", name, o)
		}
	}
	if o := dialogOutput(nil, focusOn("DP-1")); o != nil {
		t.Errorf("no outputs placed a dialog on %v", o)
	}
}
