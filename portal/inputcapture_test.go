package portal

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/eis/eistest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

const captureKeymap = "xkb_keymap { xkb_keycodes { include \"evdev\" }; };"

type fakeOutputs struct {
	outputs dbustest.Var[[]outputGeometry]
	ticks   chan struct{}
	watched dbustest.Var[int]
}

func (f *fakeOutputs) read() ([]outputGeometry, error) { return slices.Clone(f.outputs.Load()), nil }

func (f *fakeOutputs) watch() (<-chan struct{}, func(), error) {
	f.watched.Update(func(n int) int { return n + 1 })
	return f.ticks, func() { f.watched.Update(func(n int) int { return n - 1 }) }, nil
}

func (f *fakeOutputs) hotplug(outputs ...outputGeometry) {
	f.outputs.Store(outputs)
	f.ticks <- struct{}{}
}

var (
	leftZone  = outputGeometry{name: "DP-1", x: 0, y: 0, width: 1920, height: 1080}
	rightZone = outputGeometry{name: "DP-2", x: 1920, y: 0, width: 1280, height: 1024}
)

type captureRig struct {
	*rig
	wm      *lyswmtest.WM
	outputs *fakeOutputs
}

func newCaptureRig(t *testing.T) *captureRig {
	t.Helper()
	outputs := &fakeOutputs{ticks: make(chan struct{}, 4)}
	outputs.outputs.Store([]outputGeometry{rightZone, leftZone})
	var wm *lyswmtest.WM
	r := newRig(t, func(b *Backend) {
		wm = lyswmtest.Start(t)
		wm.ServeCapture(captureKeymap)
		b.input = newInputCapture(b.conn, b.sessions, lyswm.OpenCapture, outputs.read, outputs.watch)
	})
	return &captureRig{rig: r, wm: wm, outputs: outputs}
}

func (r *captureRig) create(t *testing.T) {
	t.Helper()
	var results map[string]dbus.Variant
	if err := r.obj.Call(InputCaptureIface+".CreateSession2", 0, sessA, "org.kvm", Vardict{}).Store(&results); err != nil {
		t.Fatal(err)
	}
}

func (r *captureRig) start(t *testing.T, caps uint32) (uint32, map[string]dbus.Variant) {
	t.Helper()
	return r.interactive(t, InputCaptureIface+".Start", handle, sessA, "org.kvm", "", Vardict{"capabilities": dbus.MakeVariant(caps)})
}

func (r *captureRig) started(t *testing.T) {
	t.Helper()
	r.create(t)
	serveDialogs(t, r.rig, true)
	if code, res := r.start(t, captureCapabilities); code != ResponseSuccess || res["capabilities"].Value() != captureCapabilities {
		t.Fatalf("Start = %d %v", code, res)
	}
	r.wm.AwaitCapture(t)
}

func (r *captureRig) call(t *testing.T, method string, options Vardict) uint32 {
	t.Helper()
	var code uint32
	var results map[string]dbus.Variant
	if err := r.obj.Call(InputCaptureIface+"."+method, 0, sessA, "org.kvm", options).Store(&code, &results); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return code
}

func (r *captureRig) zones(t *testing.T) ([]zoneWire, uint32) {
	t.Helper()
	code, res := r.interactive(t, InputCaptureIface+".GetZones", handle, sessA, "org.kvm", Vardict{})
	if code != ResponseSuccess {
		t.Fatalf("GetZones = %d", code)
	}
	var zones []zoneWire
	if err := dbus.Store([]any{res["zones"].Value()}, &zones); err != nil {
		t.Fatal(err)
	}
	return zones, res["zone_set"].Value().(uint32)
}

type barrierPos struct{ X1, Y1, X2, Y2 int32 }

func barrier(id uint32, x1, y1, x2, y2 int32) Vardict {
	return Vardict{"barrier_id": dbus.MakeVariant(id), "position": dbus.MakeVariant(barrierPos{x1, y1, x2, y2})}
}

func (r *captureRig) barriers(t *testing.T, zoneSet uint32, barriers ...Vardict) (uint32, []uint32) {
	t.Helper()
	code, res := r.interactive(t, InputCaptureIface+".SetPointerBarriers", handle, sessA, "org.kvm", Vardict{}, barriers, zoneSet)
	if code != ResponseSuccess {
		return code, nil
	}
	return code, res["failed_barriers"].Value().([]uint32)
}

func (r *captureRig) signal(t *testing.T, member string) *dbus.Signal {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name == InputCaptureIface+"."+member {
				if s.Body[0] != sessA {
					t.Fatalf("%s for %v, want %s", member, s.Body[0], sessA)
				}
				return s
			}
		case <-deadline:
			t.Fatalf("no %s", member)
			return nil
		}
	}
}

func signalOptions(s *dbus.Signal) map[string]dbus.Variant {
	return s.Body[1].(map[string]dbus.Variant)
}

func TestInputCaptureStartAsksFirst(t *testing.T) {
	r := newCaptureRig(t)
	r.create(t)
	if code, _ := r.start(t, captureCapabilities); code != ResponseCancelled {
		t.Errorf("Start without a shell = %d", code)
	}
	d := serveDialogs(t, r.rig, false)
	if code, _ := r.start(t, captureCapabilities); code != ResponseCancelled {
		t.Errorf("denied Start = %d", code)
	}
	if got := d.asked.Load(); len(got) == 0 || !strings.Contains(got[0].(portaldialogs.AccessRequest).Body, "your keyboard and your pointer") {
		t.Errorf("consent asked %v, want the devices named", got)
	}
	for _, caps := range []uint32{0, 4} {
		if code, _ := r.start(t, caps); code != ResponseOther {
			t.Errorf("Start with capabilities %d = %d, want other", caps, code)
		}
	}
	if code := r.call(t, "Enable", Vardict{}); code != ResponseOther {
		t.Errorf("Enable before Start = %d", code)
	}
	d.yes.Store(true)
	if code, res := r.start(t, capturePointer|4); code != ResponseSuccess || res["capabilities"].Value() != capturePointer {
		t.Errorf("allowed Start = %d %v, want the pointer alone", code, res)
	}
	r.wm.AwaitCapture(t)
	if code, _ := r.start(t, captureCapabilities); code != ResponseOther {
		t.Errorf("a second Start = %d", code)
	}
}

func TestInputCaptureZonesFollowTheOutputs(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	zones, set := r.zones(t)
	if want := []zoneWire{{1920, 1080, 0, 0}, {1280, 1024, 1920, 0}}; !slices.Equal(zones, want) {
		t.Errorf("zones %v, want %v", zones, want)
	}
	if again, same := r.zones(t); !slices.Equal(again, zones) || same != set {
		t.Errorf("unchanged outputs gave zone set %d %v after %d %v", same, again, set, zones)
	}

	r.outputs.hotplug(leftZone, rightZone, outputGeometry{name: "HDMI-A-1", x: 3200, y: 0, width: 800, height: 600})
	changed := signalOptions(r.signal(t, "ZonesChanged"))
	if changed["zone_set"].Value() != set {
		t.Errorf("ZonesChanged named zone set %v, want the invalidated %d", changed["zone_set"], set)
	}
	zones, next := r.zones(t)
	if next == set || len(zones) != 3 {
		t.Errorf("after the hotplug zone set %d %v, want a new set of three", next, zones)
	}
	if code, _ := r.barriers(t, set, barrier(1, 4000, 0, 4000, 599)); code != ResponseOther {
		t.Errorf("barriers on the stale zone set %d = %d, want other", set, code)
	}
	if code, failed := r.barriers(t, next, barrier(1, 4000, 0, 4000, 599)); code != ResponseSuccess || len(failed) != 0 {
		t.Errorf("barriers on the current zone set = %d, failed %v", code, failed)
	}
	r.wm.ExpectCaptureCommand(t, `{"command":"barriers","barriers":[{"id":1,"x1":4000,"y1":0,"x2":4000,"y2":599}]}`)
}

func TestSetPointerBarriersFailsTheOnesOffTheOutsideEdges(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	_, set := r.zones(t)
	code, failed := r.barriers(t, set,
		barrier(1, 3200, 0, 3200, 1023),
		barrier(2, 0, 1079, 0, 0),
		barrier(3, 1920, 1024, 3199, 1024),
		barrier(4, 0, 0, 1919, 0),
		barrier(5, 1920, 0, 1920, 1023),
		barrier(6, 100, 0, 100, 1079),
		barrier(7, 3200, 0, 3200, 1079),
		barrier(8, 0, 0, 30, 40),
		barrier(0, 0, 0, 0, 1079),
		barrier(9, 1920, 1080, 1919, 1080),
		barrier(10, 1919, 1080, 1919, 1080),
		barrier(1, 0, 0, 0, 10),
	)
	if code != ResponseSuccess {
		t.Fatalf("SetPointerBarriers = %d", code)
	}
	if want := []uint32{5, 6, 7, 8, 0, 9, 1}; !slices.Equal(failed, want) {
		t.Errorf("failed barriers %v, want %v", failed, want)
	}
	r.wm.ExpectCaptureCommand(t, `{"command":"barriers","barriers":[`+
		`{"id":1,"x1":3200,"y1":0,"x2":3200,"y2":1023},{"id":2,"x1":0,"y1":1079,"x2":0,"y2":0},`+
		`{"id":3,"x1":1920,"y1":1024,"x2":3199,"y2":1024},{"id":4,"x1":0,"y1":0,"x2":1919,"y2":0},`+
		`{"id":10,"x1":1919,"y1":1080,"x2":1919,"y2":1080}]}`)
}

func (r *captureRig) connectEIS(t *testing.T) *eistest.Client {
	t.Helper()
	var fd dbus.UnixFD
	if err := r.obj.Call(InputCaptureIface+".ConnectToEIS", 0, sessA, "org.kvm", Vardict{}).Store(&fd); err != nil {
		t.Fatal(err)
	}
	c := eistest.Receive(t, int(fd), "deskflow", eistest.CapKeyboard, eistest.CapPointer, eistest.CapButton, eistest.CapScroll)
	c.Until("the keyboard and pointer", c.Saw("connect", "seat "+shellName, "resumed keyboard", "resumed pointer"))
	if got := c.Keymaps["keyboard"]; got != captureKeymap+"\x00" {
		t.Errorf("the EIS keyboard's keymap %q, want lys-wm's", got)
	}
	return c
}

func TestCapturedInputReachesTheEISReceiver(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	if code := r.call(t, "Enable", Vardict{}); code != ResponseOther {
		t.Errorf("Enable before ConnectToEIS = %d", code)
	}
	c := r.connectEIS(t)
	if err := r.obj.Call(InputCaptureIface+".ConnectToEIS", 0, sessA, "org.kvm", Vardict{}).Err; err == nil {
		t.Error("a second ConnectToEIS was served")
	}
	_, set := r.zones(t)
	r.barriers(t, set, barrier(7, 3200, 0, 3200, 1023))
	r.wm.ExpectCaptureCommand(t, `{"command":"barriers","barriers":[{"id":7,"x1":3200,"y1":0,"x2":3200,"y2":1023}]}`)
	if code := r.call(t, "Enable", Vardict{}); code != ResponseSuccess {
		t.Fatalf("Enable = %d", code)
	}
	r.wm.ExpectCaptureCommand(t, `{"command":"enable"}`)

	r.wm.SendCapture(t, `{"event":"activated","activation":5,"at":{"x":3204,"y":500},"barrier":7}`,
		`{"event":"modifiers","depressed":0,"latched":0,"locked":2,"group":0}`,
		`{"event":"key","key":30,"pressed":true}`, `{"event":"key","key":30,"pressed":false}`,
		`{"event":"motion","dx":-3,"dy":2}`, `{"event":"button","button":272,"pressed":true}`, `{"event":"frame"}`,
		`{"event":"scroll_discrete","dx":0,"dy":120}`, `{"event":"frame"}`)
	activated := signalOptions(r.signal(t, "Activated"))
	if activated["activation_id"].Value() != uint32(5) || activated["barrier_id"].Value() != uint32(7) ||
		!slices.Equal(activated["cursor_position"].Value().([]any), []any{3204.0, 500.0}) {
		t.Errorf("Activated %v", activated)
	}
	c.Until("the captured input", c.Saw("discrete 0 120"))
	want := []string{
		"start keyboard 5", "start pointer 5", "modifiers 0 0 2 0",
		"key 30 true", "frame keyboard", "key 30 false", "frame keyboard",
		"motion -3 2", "button 272 true", "frame pointer", "discrete 0 120",
	}
	if got := c.Since("start keyboard 5"); !slices.Equal(got[:min(len(got), len(want))], want) {
		t.Errorf("the EIS receiver got %q, want %q", got, want)
	}

	if code := r.call(t, "Release", Vardict{"activation_id": dbus.MakeVariant(uint32(4)), "cursor_position": dbus.MakeVariant(struct{ X, Y float64 }{10, 20})}); code != ResponseSuccess {
		t.Errorf("Release of an old activation = %d", code)
	}
	if code := r.call(t, "Release", Vardict{"activation_id": dbus.MakeVariant(uint32(5)), "cursor_position": dbus.MakeVariant(struct{ X, Y float64 }{100, 200.5})}); code != ResponseSuccess {
		t.Errorf("Release = %d", code)
	}
	r.wm.ExpectCaptureCommand(t, `{"command":"release","at":{"x":100,"y":200.5}}`)
	r.wm.SendCapture(t, `{"event":"deactivated","activation":5,"at":{"x":100,"y":200.5}}`)
	deactivated := signalOptions(r.signal(t, "Deactivated"))
	if deactivated["activation_id"].Value() != uint32(5) {
		t.Errorf("Deactivated %v", deactivated)
	}
	c.Until("the emulation to stop", c.Saw("stop keyboard", "stop pointer"))
}

func TestALockOnLysWMDisablesTheSession(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	c := r.connectEIS(t)
	if code := r.call(t, "Enable", Vardict{}); code != ResponseSuccess {
		t.Fatalf("Enable = %d", code)
	}
	r.wm.SendCapture(t, `{"event":"activated","activation":1,"at":{"x":3204,"y":500},"barrier":7}`)
	r.signal(t, "Activated")
	r.wm.SendCapture(t, `{"event":"deactivated","activation":1,"at":{"x":3199,"y":500}}`, `{"event":"disabled"}`)
	r.signal(t, "Deactivated")
	r.signal(t, "Disabled")
	c.Until("the emulation to stop", c.Saw("start keyboard 1", "stop keyboard"))
}

func TestClosingTheSessionClosesTheCapture(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	if r.outputs.watched.Load() != 1 {
		t.Errorf("%d output watches for one session", r.outputs.watched.Load())
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	r.wm.AwaitCaptureClosed(t)
	if r.outputs.watched.Load() != 0 {
		t.Error("the output watch outlived the last session")
	}
}

func TestTheCaptureEndingClosesTheSession(t *testing.T) {
	r := newCaptureRig(t)
	r.started(t)
	if err := r.client.AddMatchSignal(dbus.WithMatchObjectPath(sessA)); err != nil {
		t.Fatal(err)
	}
	r.wm.SendCapture(t, `{"event":"error","error":"lys-wm is going away"}`)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name == SessionIface+".Closed" && s.Path == sessA {
				return
			}
		case <-deadline:
			t.Fatal("the session outlived its capture")
		}
	}
}

func mountsInputCapture(t *testing.T) bool {
	t.Helper()
	return slices.ContainsFunc(New(nil, config.Load(t.TempDir(), config.DiscardDiagnostics)).interfaces(), func(i dbusx.Interface) bool {
		return i.Name == InputCaptureIface
	})
}

func TestInputCaptureIsNotMountedWithoutLysWM(t *testing.T) {
	t.Setenv(lyswm.SocketEnv, "")
	if mountsInputCapture(t) {
		t.Error("InputCapture mounted without lys-wm")
	}
}

func TestInputCaptureIsNotMountedOnALysWMWithoutTheCaptureIPC(t *testing.T) {
	lyswmtest.Start(t)
	if mountsInputCapture(t) {
		t.Error("InputCapture mounted on a lys-wm without the capture IPC")
	}
}

func TestInputCaptureIsMountedOnALysWMThatCaptures(t *testing.T) {
	wm := lyswmtest.Start(t)
	wm.ServeCapture(captureKeymap)
	if !mountsInputCapture(t) {
		t.Error("InputCapture not mounted on a lys-wm that captures")
	}
	wm.AwaitCapture(t)
	wm.AwaitCaptureClosed(t)
}
