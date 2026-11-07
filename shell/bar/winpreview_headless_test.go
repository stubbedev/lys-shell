package bar

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/swaytest"
)

// On a real compositor a workspace's hover preview leaves the bar its
// clicks: with the preview open, the first click on the clock opens its
// calendar, and the preview gives way to it. Sway is the compositor the
// workspaces module detects, whatever session the test runs inside.
func TestHeadlessAClickBesideTheWorkspacePreviewReachesTheBar(t *testing.T) {
	for _, name := range []string{lyswm.SocketEnv, "HYPRLAND_INSTANCE_SIGNATURE", niri.SocketEnv, mango.SocketEnv} {
		t.Setenv(name, "")
	}
	sway := swaytest.Start(t)
	t.Setenv("SWAYSOCK", filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "sway-ipc."+strconv.Itoa(os.Getuid())+"."+strconv.Itoa(sway.Pid)+".sock"))
	startProbeProcess(t, "org.example.Probe", "probe title")
	layout := config.BarLayout{Monitor: "*", Left: []config.BarItem{{Module: "sway-workspaces"}}, Right: []config.BarItem{{Module: "clock"}}}
	h := openHeadlessBar(t, config.Defaults(), layout)
	clock := h.buttons(1)[0]

	var segs widget.Boundser
	h.wait("the workspace with the probe on it", func() error {
		var err error
		h.onLoop(func() {
			found := treetest.WithClass(h.bar.bar, "workspaces")
			if len(found) != 1 {
				err = fmt.Errorf("%d workspace indicators", len(found))
				return
			}
			segs = found[0].(widget.Boundser)
			if segs.Bounds().Empty() {
				err = errors.New("the workspace indicator is not laid out")
			}
		})
		return err
	})

	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pointer.Close()
	at := func(w widget.Boundser) {
		t.Helper()
		var b render.Rect
		h.onLoop(func() { b = w.Bounds() })
		ow, oh := pointer.OutputSize()
		x, y := b.X+b.W/2, b.Y+b.H/2
		if err := pointer.MotionAbsolute(intconv.Clamp[uint32](x), intconv.Clamp[uint32](y), intconv.Clamp[uint32](ow), intconv.Clamp[uint32](oh)); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	holds := func(want int, open string) func() error {
		return func() error {
			var got int
			var name string
			h.onLoop(func() { got, name = h.bar.hide.holds, h.rt.ctx.Dropdowns.openName[h.output.Name] })
			if got != want || name != open {
				return fmt.Errorf("%d reveal holds and dropdown %q open, want %d and %q", got, name, want, open)
			}
			return nil
		}
	}

	at(segs)
	h.wait("the workspace preview to open on a rest", holds(1, ""))

	at(clock)
	for _, pressed := range []bool{true, false} {
		if err := pointer.Button(widget.BTNLeft, pressed); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	h.wait("the first click on the clock to open its calendar in place of the preview", holds(1, "calendar"))
}
