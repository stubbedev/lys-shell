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
	dp1 := &app.Output{Name: "DP-1", LogicalW: 1920, LogicalH: 1080}
	dp2 := &app.Output{Name: "DP-2", LogicalW: 2560, LogicalH: 1440}
	outs := []*app.Output{dp1, dp2}
	if o, w, h, ok := dialogOutput(outs, focusOn("DP-2")); o != dp2 || w != 2560 || h != 1440 || !ok {
		t.Errorf("focused DP-2 = %v %dx%d %v", o, w, h, ok)
	}
	for _, name := range []string{"", "HDMI-9"} {
		if o, _, _, ok := dialogOutput(outs, focusOn(name)); o != dp1 || !ok {
			t.Errorf("focus %q = %v %v, want the first output", name, o, ok)
		}
	}
	if _, _, _, ok := dialogOutput(nil, focusOn("DP-1")); ok {
		t.Error("no outputs placed a dialog")
	}
}
