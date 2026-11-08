package portal

import (
	"image"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-desktop/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-lib/eis/eistest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-lib/wmipc"
)

func TestInputCaptureOnLysWM(t *testing.T) {
	r := &captureRig{}
	r.rig = newRig(t, func(b *Backend) {
		lyswmtest.Headless(t)
		b.input = offeredInputCapture(b.conn, b.sessions, &waylandLoop{})
		if b.input == nil {
			t.Fatal("lys-wm offers no input capture")
		}
	})
	r.create(t)
	serveDialogs(t, r.rig, true)
	if code, _ := r.start(t, captureCapabilities); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	var fd dbus.UnixFD
	if err := r.obj.Call(InputCaptureIface+".ConnectToEIS", 0, sessA, "org.kvm", Vardict{}).Store(&fd); err != nil {
		t.Fatal(err)
	}
	c := eistest.Receive(t, int(fd), "deskflow", eistest.CapKeyboard, eistest.CapPointer, eistest.CapButton, eistest.CapScroll)
	c.Until("the keyboard and pointer", c.Saw("resumed keyboard", "resumed pointer"))
	if km := c.Keymaps["keyboard"]; !strings.HasPrefix(km, "xkb_keymap") {
		t.Errorf("the EIS keyboard's keymap starts %.40q, want lys-wm's xkb keymap", km)
	}

	zones, set := r.zones(t)
	if len(zones) != 1 {
		t.Fatalf("zones %v, want the headless output", zones)
	}
	if _, err := lyswm.CreateOutput(wmipc.Mode{Width: 800, Height: 600, RefreshMHz: 60000}, 1); err != nil {
		t.Fatal(err)
	}
	if got := signalOptions(r.signal(t, "ZonesChanged"))["zone_set"].Value(); got != set {
		t.Errorf("ZonesChanged named zone set %v, want the invalidated %d", got, set)
	}
	zones, next := r.zones(t)
	if len(zones) != 2 || next == set {
		t.Fatalf("after the hotplug zone set %d %v, want a new set of two", next, zones)
	}
	right := slices.MaxFunc(zones, func(a, b zoneWire) int { return int(a.X) + int(a.Width) - int(b.X) - int(b.Width) })
	edge := right.X + int32(right.Width)
	if code, failed := r.barriers(t, next, barrier(9, edge, right.Y, edge, right.Y+int32(right.Height)-1), barrier(10, right.X+1, right.Y, right.X+1, right.Y+10)); code != ResponseSuccess || !slices.Equal(failed, []uint32{10}) {
		t.Fatalf("SetPointerBarriers = %d, failed %v; want the inner barrier failed", code, failed)
	}
	if code := r.call(t, "Enable", Vardict{}); code != ResponseSuccess {
		t.Fatalf("Enable = %d", code)
	}

	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer pointer.Close()
	var box image.Rectangle
	for _, z := range zones {
		box = box.Union(zoneRect(outputGeometry{x: z.X, y: z.Y, width: int32(z.Width), height: int32(z.Height)}))
	}
	if err := pointer.MotionAbsolute(uint32(int(edge)-1-box.Min.X), uint32(int(right.Y)+10-box.Min.Y), uint32(box.Dx()), uint32(box.Dy())); err != nil {
		t.Fatal(err)
	}
	if err := pointer.Motion(25, 0); err != nil {
		t.Fatal(err)
	}
	activated := signalOptions(r.signal(t, "Activated"))
	id, _ := activated["activation_id"].Value().(uint32)
	if activated["barrier_id"].Value() != uint32(9) || id == 0 {
		t.Fatalf("Activated %v, want barrier 9", activated)
	}
	if err := pointer.Motion(-4, 3); err != nil {
		t.Fatal(err)
	}
	c.Until("the captured motion", c.Saw("motion -4 3", "frame pointer"))
	if got := c.Since("start keyboard " + strconv.FormatUint(uint64(id), 10)); len(got) == 0 {
		t.Errorf("the EIS receiver never started activation %d: %q", id, c.Log)
	}

	if code := r.call(t, "Release", Vardict{"activation_id": dbus.MakeVariant(id), "cursor_position": dbus.MakeVariant(struct{ X, Y float64 }{100, 100})}); code != ResponseSuccess {
		t.Fatalf("Release = %d", code)
	}
	if got := signalOptions(r.signal(t, "Deactivated"))["cursor_position"].Value(); !slices.Equal(got.([]any), []any{100.0, 100.0}) {
		t.Errorf("Deactivated at %v, want the warp point", got)
	}
	c.Until("the emulation to stop", c.Saw("stop pointer"))
}
