package portal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/vinput"

	"github.com/stubbedev/lys-shell/shell/portaldialogs"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakeSink records the injected input.
type fakeSink struct {
	events dbustest.Var[[]string]
	closed dbustest.Var[bool]
	keymap []byte
}

func (f *fakeSink) log(s string) { f.events.Update(func(e []string) []string { return append(e, s) }) }

func (f *fakeSink) Motion(dx, dy float64) error { f.log(sprint("motion", dx, dy)); return nil }
func (f *fakeSink) MotionAbsolute(x, y, w, h uint32) error {
	f.log(sprint("abs", x, y, w, h))
	return nil
}
func (f *fakeSink) Button(c uint32, p bool) error { f.log(sprint("button", c, p)); return nil }

// sourceNames name the scroll sources in the log.
var sourceNames = map[vinput.AxisSource]string{vinput.SourceWheel: "wheel", vinput.SourceFinger: "finger"}

func (f *fakeSink) Axis(src vinput.AxisSource, a vinput.ScrollAxis, v float64) error {
	f.log(sprint("axis", sourceNames[src], a, v))
	return nil
}

func (f *fakeSink) AxisDiscrete(src vinput.AxisSource, a vinput.ScrollAxis, v float64, s int32) error {
	f.log(sprint("discrete", sourceNames[src], a, v, s))
	return nil
}

func (f *fakeSink) AxisStop(src vinput.AxisSource, a vinput.ScrollAxis) error {
	f.log(sprint("stop", sourceNames[src], a))
	return nil
}
func (f *fakeSink) Key(c uint32, p bool) error { f.log(sprint("key", c, p)); return nil }
func (f *fakeSink) Keymap() []byte             { return f.keymap }
func (f *fakeSink) Close()                     { f.closed.Store(true) }

func sprint(parts ...any) string { return strings.TrimSuffix(fmt.Sprintln(parts...), "\n") }

// testKeymap is an explicit two-key keymap: <AC01> types a / A (evdev
// 30), <LFSH> is shift.
const testKeymap = `xkb_keymap {
xkb_keycodes "test" { minimum = 8; maximum = 255; <AC01> = 38; <LFSH> = 50; };
xkb_types "test" {
  virtual_modifiers NumLock;
  type "ONE_LEVEL" { modifiers = none; level_name[Level1] = "Any"; };
  type "ALPHABETIC" { modifiers = Shift+Lock; map[Shift] = Level2; map[Lock] = Level2; level_name[Level1] = "Base"; level_name[Level2] = "Caps"; };
};
xkb_compat "test" { };
xkb_symbols "test" {
  key <AC01> { type = "ALPHABETIC", [ a, A ] };
  key <LFSH> { [ Shift_L ] };
  modifier_map Shift { <LFSH> };
};
};
`

// remoteRig has the fake virtual input, the fake streams over a
// 2x-scaled DP-1 left of a 1x DP-2, and a fake clipboard device.
func remoteRig(t *testing.T, dialErr error) (*rig, *fakeSink) {
	r, sink, _ := remoteRigStarts(t, dialErr)
	return r, sink
}

// remoteRigStarts is remoteRig with its fake streams, for a test that
// steers them.
func remoteRigStarts(t *testing.T, dialErr error) (*rig, *fakeSink, *fakeStarts) {
	sink := &fakeSink{keymap: []byte(testKeymap)}
	starts := &fakeStarts{fail: map[string]bool{}}
	r := newRig(t, func(b *Backend) {
		b.screenCast = newScreenCast(b.conn, b.sessions, starts.start, starts.outputs)
		b.remote = newRemoteDesktop(b.conn, b.sessions, b.screenCast, func() (inputSink, error) {
			if dialErr != nil {
				return nil, dialErr
			}
			return sink, nil
		})
		b.clipboard = newClipboardBridge(b.conn, b.sessions, func(func(selectionChange)) (clipDevice, error) {
			return &fakeClip{data: "copied"}, nil
		})
	})
	return r, sink, starts
}

func (r *rig) remoteStart(t *testing.T, options Vardict) (uint32, map[string]dbus.Variant) {
	t.Helper()
	if _, live := r.backend.sessions.get(sessA); !live {
		r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
	}
	r.interactive(t, RemoteDesktopIface+".SelectDevices", handle, sessA, "org.app", options)
	return r.interactive(t, RemoteDesktopIface+".Start", handle, sessA, "org.app", "", Vardict{})
}

func (r *rig) notify(t *testing.T, method string, args ...any) {
	t.Helper()
	if err := r.notifyErr(method, args...); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (r *rig) notifyErr(method string, args ...any) error {
	return r.obj.Call(RemoteDesktopIface+"."+method, 0, append([]any{sessA, Vardict{}}, args...)...).Err
}

// The user is asked first, told which devices; nothing is injected
// before an allowed Start, and the devices granted are the ones asked.
func TestRemoteDesktopAsksFirst(t *testing.T) {
	r, sink := remoteRig(t, nil)
	if code, _ := r.remoteStart(t, Vardict{}); code != ResponseCancelled {
		t.Errorf("no shell = %d", code)
	}
	d := serveDialogs(t, r, false)
	if code, _ := r.remoteStart(t, Vardict{}); code != ResponseCancelled {
		t.Errorf("denied = %d", code)
	}
	if got := d.asked.Load(); len(got) == 0 || !strings.Contains(got[0].(portaldialogs.AccessRequest).Body, "your keyboard and pointer") {
		t.Errorf("consent asked %v, want the devices named", got)
	}
	if err := r.notifyErr("NotifyPointerMotion", 1.0, 2.0); err == nil {
		t.Error("input before an allowed Start was accepted")
	}
	if len(sink.events.Load()) != 0 {
		t.Error("input before an allowed Start")
	}
	d.yes.Store(true)
	code, res := r.remoteStart(t, Vardict{"types": dbus.MakeVariant(devicePointer)})
	if code != ResponseSuccess || res["devices"].Value() != devicePointer || res["clipboard_enabled"].Value() != false {
		t.Errorf("Start = %d %v", code, res)
	}
	if body := d.asked.Load()[0].(portaldialogs.AccessRequest).Body; !strings.Contains(body, "your pointer") || strings.Contains(body, "keyboard") {
		t.Errorf("a pointer-only consent says %q", body)
	}
	if _, has := res["streams"]; has {
		t.Errorf("streams without screen sharing: %v", res)
	}
	if code, _ := r.interactive(t, RemoteDesktopIface+".SelectDevices", handle, sessA, "org.app", Vardict{"types": dbus.MakeVariant(uint32(4))}); code != ResponseOther {
		t.Errorf("touch, not offered = %d", code)
	}
}

// startSharing starts sessA as a remote-desktop session that also
// shares the screen the picker replies.
func (r *rig) startSharing(t *testing.T, reply string, devices Vardict) map[string]dbus.Variant {
	t.Helper()
	serveSharePicker(t, r, reply)
	r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
	r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow)})
	code, res := r.remoteStart(t, devices)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	return res
}

func TestRemoteDesktopReplaysInput(t *testing.T) {
	r, sink := remoteRig(t, nil)
	serveDialogs(t, r, true)
	res := r.startSharing(t, "/screen:DP-2;window:w1", Vardict{})
	if res["devices"].Value() != uint32(3) {
		t.Fatalf("Start = %v", res)
	}
	r.notify(t, "NotifyPointerMotion", 1.5, -2.0)
	// DP-2 is at (1280, 0) in a layout 3200x1080: (100.5, -5) is
	// (1380.5, 0), in 256ths of a pixel.
	r.notify(t, "NotifyPointerMotionAbsolute", uint32(40), 100.5, -5.0)
	if err := r.notifyErr("NotifyPointerMotionAbsolute", uint32(99), 1.0, 1.0); err == nil {
		t.Error("a stream the session does not run took absolute motion")
	}
	if err := r.notifyErr("NotifyPointerMotionAbsolute", uint32(41), 1.0, 1.0); err == nil {
		t.Error("a window stream, whose place is unknown, took absolute motion")
	}
	r.notify(t, "NotifyPointerButton", int32(0x110), uint32(1))
	r.notify(t, "NotifyPointerAxis", 0.0, 3.0)
	r.notify(t, "NotifyPointerAxis", 2.0, 0.0)
	r.notify(t, "NotifyPointerAxisDiscrete", uint32(1), int32(-2))
	r.notify(t, "NotifyPointerAxisDiscrete", uint32(7), int32(1))
	r.notify(t, "NotifyKeyboardKeycode", int32(30), uint32(1))
	r.notify(t, "NotifyKeyboardKeysym", int32(0x41), uint32(1)) // A: shift + a
	r.notify(t, "NotifyKeyboardKeysym", int32(0x41), uint32(0))
	r.notify(t, "NotifyKeyboardKeysym", int32(0x61), uint32(1)) // a
	r.notify(t, "NotifyKeyboardKeysym", int32(0x7a), uint32(1)) // z: not in the keymap
	r.notify(t, "NotifyTouchDown", uint32(40), uint32(0), 1.0, 1.0)
	want := []string{
		"motion 1.5 -2",
		"abs 353408 0 819200 276480",
		"button 272 true",
		"axis finger 0 3",
		"axis finger 1 2",
		"discrete wheel 1 -30 -2",
		"discrete wheel 0 15 1",
		"key 30 true",
		"key 42 true", "key 30 true",
		"key 30 false", "key 42 false",
		"key 30 true",
	}
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("input =\n%q\nwant\n%q", got, want)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if !sink.closed.Load() {
		t.Error("the devices outlived the session")
	}
}

// Start shares the screens a remote-desktop session selected, with
// their places, and says whether the clipboard is enabled; the
// clipboard then works for the session.
func TestRemoteDesktopSharesScreensAndClipboard(t *testing.T) {
	r, _ := remoteRig(t, nil)
	serveDialogs(t, r, true)
	r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
	if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sessA, Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	res := r.startSharing(t, "/screen:DP-1", Vardict{})
	if res["clipboard_enabled"].Value() != true {
		t.Errorf("clipboard_enabled = %v after RequestClipboard", res["clipboard_enabled"])
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || pair(streams[0].Props["position"]) != [2]int32{0, 0} || pair(streams[0].Props["size"]) != [2]int32{1280, 720} {
		t.Errorf("streams = %+v", streams)
	}
	var fd dbus.UnixFD
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "text/plain").Store(&fd); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(os.NewFile(uintptr(fd), "read"))
	if string(got) != "copied" {
		t.Errorf("read %q through the remote-desktop session", got)
	}
}

// Only the device types granted reach the devices, over D-Bus and EIS.
func TestRemoteDesktopEnforcesTheGrantedDevices(t *testing.T) {
	r, sink := remoteRig(t, nil)
	serveDialogs(t, r, true)
	if code, _ := r.remoteStart(t, Vardict{"types": dbus.MakeVariant(devicePointer)}); code != ResponseSuccess {
		t.Fatal("Start failed")
	}
	for _, call := range [][]any{
		{"NotifyKeyboardKeycode", int32(30), uint32(1)},
		{"NotifyKeyboardKeysym", int32(0x61), uint32(1)},
	} {
		if err := r.notifyErr(call[0].(string), call[1:]...); err == nil {
			t.Errorf("%s without the keyboard granted succeeded", call[0])
		}
	}
	in, err := r.backend.remote.input(sessA)
	if err != nil {
		t.Fatal(err)
	}
	e := eisInput{in}
	e.Key(30, true)
	e.Keysym(0x61, true)
	e.Motion(1, 1)
	if got := sink.events.Load(); !slices.Equal(got, []string{"motion 1 1"}) {
		t.Errorf("injected %q, want the pointer only", got)
	}
}

// A restore token of the same devices replays the grant and the shared
// screens without asking; one of other devices asks again.
func TestRemoteDesktopRestoreToken(t *testing.T) {
	r, _ := remoteRig(t, nil)
	d := serveDialogs(t, r, true)
	res := r.startSharing(t, "r/screen:DP-2", Vardict{"persist_mode": dbus.MakeVariant(uint32(2))})
	kept, ok := decodeRemoteRestore(res["restore_data"])
	if !ok || res["persist_mode"].Value() != uint32(2) || kept.devices != 3 || !slices.Equal(payloads(kept.targets), []string{"screen:DP-2"}) {
		t.Fatalf("token keeps %+v, %v (results %v)", kept, ok, res)
	}
	// The frontend hands the token back as the (suv) it got.
	token := dbus.MakeVariant(encodeRemoteRestore(kept))

	r2, sink2 := remoteRig(t, nil)
	d2 := serveDialogs(t, r2, false)
	p2 := serveSharePicker(t, r2, "")
	r2.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
	r2.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{})
	code, res2 := r2.remoteStart(t, Vardict{"restore_data": token, "persist_mode": dbus.MakeVariant(uint32(2))})
	if code != ResponseSuccess || len(d2.asked.Load()) != 0 || len(p2.asked.Load()) != 0 {
		t.Fatalf("replay = %d, asked consent %v, picker %v", code, d2.asked.Load(), p2.asked.Load())
	}
	if streams := castStreams(t, res2); len(streams) != 1 || pair(streams[0].Props["position"]) != [2]int32{1280, 0} {
		t.Errorf("replayed streams %+v", streams)
	}
	if _, ok := res2["restore_data"]; !ok {
		t.Error("a replayed grant was not reissued")
	}
	r2.notify(t, "NotifyPointerMotion", 1.0, 0.0)
	if len(sink2.events.Load()) != 1 {
		t.Error("the replayed session takes no input")
	}

	r3, _ := remoteRig(t, nil)
	d3 := serveDialogs(t, r3, false)
	if code, _ := r3.remoteStart(t, Vardict{"restore_data": token, "types": dbus.MakeVariant(deviceKeyboard)}); code != ResponseCancelled || len(d3.asked.Load()) != 1 {
		t.Errorf("a token of other devices = %d, asked %d times", code, len(d3.asked.Load()))
	}
	if _, ok := decodeRemoteRestore(dbus.MakeVariant(encodeRestore([]sharepicker.Target{{Kind: sharepicker.TargetOutput, Name: "DP-1"}}))); ok {
		t.Error("a ScreenCast token decoded as a RemoteDesktop one")
	}
	if len(d.asked.Load()) != 1 {
		t.Errorf("the first start asked %d times", len(d.asked.Load()))
	}
}

func TestRemoteDesktopWithoutVirtualInput(t *testing.T) {
	r, _ := remoteRig(t, errors.New("no zwlr_virtual_pointer_manager_v1"))
	serveDialogs(t, r, true)
	if code, _ := r.remoteStart(t, Vardict{}); code != ResponseOther {
		t.Errorf("Start = %d, want other", code)
	}
	if got := keysymTable([]byte("not a keymap")); len(got) != 0 {
		t.Errorf("a garbage keymap mapped %v", got)
	}
}

// level3Keymap has AltGr in Mod5 and a four-level key: e, E, €, ¢.
const level3Keymap = `xkb_keymap {
xkb_keycodes "test" { minimum = 8; maximum = 255; <AD03> = 26; <LFSH> = 50; <RALT> = 108; };
xkb_types "test" {
  type "ONE_LEVEL" { modifiers = none; level_name[Level1] = "Any"; };
  type "FOUR_LEVEL" { modifiers = Shift+Mod5; map[Shift] = Level2; map[Mod5] = Level3; map[Shift+Mod5] = Level4;
    level_name[Level1] = "Base"; level_name[Level2] = "Shift"; level_name[Level3] = "AltGr"; level_name[Level4] = "Shift AltGr"; };
};
xkb_compat "test" { };
xkb_symbols "test" {
  key <AD03> { type = "FOUR_LEVEL", [ e, E, EuroSign, cent ] };
  key <LFSH> { [ Shift_L ] };
  key <RALT> { [ ISO_Level3_Shift ] };
  modifier_map Shift { <LFSH> };
  modifier_map Mod5 { <RALT> };
};
};
`

// Level-3 symbols are typed with AltGr held (and shift for level 4),
// but only when the keymap puts AltGr in Mod5.
func TestKeysymsReachLevelThree(t *testing.T) {
	sink := &fakeSink{keymap: []byte(level3Keymap)}
	in := &remoteInput{sink: sink, granted: deviceKeyboard}
	in.keysym(0x20ac, true) // EuroSign
	in.keysym(0x20ac, false)
	in.keysym(0xa2, true) // cent
	want := []string{"key 100 true", "key 18 true", "key 18 false", "key 100 false", "key 100 true", "key 42 true", "key 18 true"}
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("typed %q, want %q", got, want)
	}
	notMod5 := strings.Replace(level3Keymap, "modifier_map Mod5 { <RALT> };", "modifier_map Mod4 { <RALT> };", 1)
	if k, ok := keysymTable([]byte(notMod5))[0x20ac]; ok {
		t.Errorf("AltGr outside Mod5 still types € as %+v", k)
	}
}

// Hi-res wheel steps below a detent scroll smoothly and add up to whole
// steps instead of being dropped.
// A smooth scroll with finish ends the scroll on both axes after its
// deltas, as lifted fingers do, over D-Bus and EIS; without finish no
// scroll ends, and without the pointer granted nothing reaches the
// devices.
func TestRemoteDesktopFinishEndsTheScroll(t *testing.T) {
	r, sink := remoteRig(t, nil)
	serveDialogs(t, r, true)
	if code, _ := r.remoteStart(t, Vardict{"types": dbus.MakeVariant(devicePointer)}); code != ResponseSuccess {
		t.Fatal("Start failed")
	}
	axis := func(options Vardict, dx, dy float64) {
		t.Helper()
		if err := r.obj.Call(RemoteDesktopIface+".NotifyPointerAxis", 0, sessA, options, dx, dy).Err; err != nil {
			t.Fatal(err)
		}
	}
	axis(Vardict{}, 0, 2)
	axis(Vardict{"finish": dbus.MakeVariant(true)}, 1, 3)
	axis(Vardict{"finish": dbus.MakeVariant(false)}, 0, 1)
	in, err := r.backend.remote.input(sessA)
	if err != nil {
		t.Fatal(err)
	}
	eisInput{in}.ScrollStop(true, false)
	want := []string{
		"axis finger 0 2",
		"axis finger 0 3", "axis finger 1 1", "stop finger 0", "stop finger 1",
		"axis finger 0 1",
		"stop finger 1",
	}
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("input =\n%q\nwant\n%q", got, want)
	}

	keyboardOnly := &remoteInput{sink: sink, granted: deviceKeyboard}
	sink.events.Store(nil)
	eisInput{keyboardOnly}.ScrollStop(true, true)
	if got := sink.events.Load(); len(got) != 0 {
		t.Errorf("a scroll stop without the pointer granted reached the devices: %q", got)
	}
}

func TestEISScrollAddsUpHiResSteps(t *testing.T) {
	sink := &fakeSink{}
	e := eisInput{&remoteInput{sink: sink, granted: devicePointer}}
	e.ScrollDiscrete(0, 60)
	e.ScrollDiscrete(0, 60)
	e.ScrollDiscrete(-240, 0)
	e.ScrollDiscrete(0, 0)
	want := []string{"discrete wheel 0 7.5 0", "discrete wheel 0 7.5 1", "discrete wheel 1 -30 -2"}
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("scrolled %q, want %q", got, want)
	}
}

func TestRemoteDesktopConnectToEIS(t *testing.T) {
	r, sink := remoteRig(t, nil)
	serveDialogs(t, r, true)
	r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
	var fd dbus.UnixFD
	err := r.obj.Call(RemoteDesktopIface+".ConnectToEIS", 0, sessA, "org.app", Vardict{}).Store(&fd)
	var de dbus.Error
	if !errors.As(err, &de) || !strings.Contains(de.Body[0].(string), "is not started") {
		t.Errorf("ConnectToEIS before Start = %v", err)
	}
	r.interactive(t, RemoteDesktopIface+".SelectDevices", handle, sessA, "org.app", Vardict{})
	r.interactive(t, RemoteDesktopIface+".Start", handle, sessA, "org.app", "", Vardict{})
	if err := r.obj.Call(RemoteDesktopIface+".ConnectToEIS", 0, sessA, "org.app", Vardict{}).Store(&fd); err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), "eis")
	defer f.Close()
	// An EIS server answers on the other end: it opens with its
	// handshake version, ei_handshake (object 0) opcode 0.
	_ = f.SetReadDeadline(time.Now().Add(2 * time.Second))
	head := make([]byte, 20)
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint64(head) != 0 || binary.LittleEndian.Uint32(head[8:]) != 20 || binary.LittleEndian.Uint32(head[12:]) != 0 || binary.LittleEndian.Uint32(head[16:]) != 1 {
		t.Errorf("first message = %x", head)
	}

	// The handler replays onto the session's devices.
	in, err := r.backend.remote.input(sessA)
	if err != nil {
		t.Fatal(err)
	}
	e := eisInput{in}
	e.ScrollDiscrete(-120, 240)
	e.ScrollDiscrete(0, 0)
	e.Scroll(1, 0)
	e.Keysym(0x41, true)
	want := []string{"discrete wheel 0 30 2", "discrete wheel 1 -15 -1", "axis finger 1 1", "key 42 true", "key 30 true"}
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("replayed %q, want %q", got, want)
	}
}

// Input reaches the devices in the order it was sent even when the
// sender does not wait for replies, as xdg-desktop-portal forwards it:
// a button and a scroll sent after an absolute motion whose layout
// query is slow do not overtake it.
func TestRemoteInputKeepsItsOrder(t *testing.T) {
	r, sink, starts := remoteRigStarts(t, nil)
	serveDialogs(t, r, true)
	r.startSharing(t, "/screen:DP-2", Vardict{})
	starts.slowOutputs.Store(100 * time.Millisecond)
	send := func(method string, args ...any) {
		r.obj.Go(RemoteDesktopIface+"."+method, dbus.FlagNoReplyExpected, nil, append([]any{sessA, Vardict{}}, args...)...)
	}
	send("NotifyPointerMotionAbsolute", uint32(40), 0.0, 0.0)
	send("NotifyPointerButton", int32(0x110), uint32(1))
	send("NotifyPointerAxis", 0.0, 3.0)
	want := []string{"abs 327680 0 819200 276480", "button 272 true", "axis finger 0 3"}
	waitFor(t, func() bool { return len(sink.events.Load()) >= len(want) })
	if got := sink.events.Load(); !slices.Equal(got, want) {
		t.Errorf("input = %q, want %q", got, want)
	}
}

// The input methods run in order, and only they: Start waits on the
// user, so ordering it would hold every session's input behind it.
func TestOnlyInputMethodsAreOrdered(t *testing.T) {
	got := inputMethods()
	for _, m := range []string{"NotifyPointerMotion", "NotifyPointerMotionAbsolute", "NotifyPointerButton", "NotifyPointerAxis", "NotifyPointerAxisDiscrete", "NotifyKeyboardKeycode", "NotifyKeyboardKeysym", "NotifyTouchDown", "NotifyTouchMotion", "NotifyTouchUp"} {
		if !slices.Contains(got, m) {
			t.Errorf("%s is not ordered", m)
		}
	}
	for _, m := range []string{"CreateSession", "SelectDevices", "Start"} {
		if slices.Contains(got, m) {
			t.Errorf("%s is ordered", m)
		}
	}
}
