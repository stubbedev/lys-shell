package portal

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math/bits"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/eis"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-lib/xkb"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// RemoteDesktopIface lets a session drive the pointer, the keyboard and,
// where the compositor serves a virtual touchscreen, touch: after the
// user consents, the Notify* methods and an EIS connection are replayed
// onto virtual devices.
const RemoteDesktopIface = "org.freedesktop.impl.portal.RemoteDesktop"

// Device types. Touchscreen is offered only where the compositor
// serves lys_virtual_touch_v1; allDevices is every type a restore token
// may name.
const (
	deviceKeyboard    uint32 = 1
	devicePointer     uint32 = 2
	deviceTouchscreen uint32 = 4

	allDevices = deviceKeyboard | devicePointer | deviceTouchscreen
)

// keyLeftShift is the evdev code a shifted keysym is typed with.
const keyLeftShift = 42

// inputSink is where a session's input goes (a vinput.Device; a fake
// in tests).
type inputSink interface {
	Motion(dx, dy float64) error
	MotionAbsolute(x, y, width, height uint32) error
	Button(code uint32, pressed bool) error
	Axis(src vinput.AxisSource, axis vinput.ScrollAxis, value float64) error
	AxisDiscrete(src vinput.AxisSource, axis vinput.ScrollAxis, value float64, steps int32) error
	AxisStop(src vinput.AxisSource, axis vinput.ScrollAxis) error
	Key(code uint32, pressed bool) error
	TouchDown(id, x, y, width, height uint32) error
	TouchMotion(id, x, y, width, height uint32) error
	TouchUp(id uint32) error
	TouchCancel() error
	Keymap() []byte
	Close()
}

func dialInput(touch bool) (inputSink, error) {
	return vinput.Dial("", vinput.Options{Keyboard: true, Touch: touch})
}

// touchOffered probes the compositor once for a virtual touchscreen; a
// failed probe offers none, with a warning naming why.
var touchOffered = sync.OnceValue(func() bool {
	ok, err := vinput.TouchAvailable("")
	if err != nil {
		warnf("remotedesktop: cannot tell whether the compositor takes touch, so touch is not offered: %v", err)
	}
	return ok
})

// remoteInput is a started session's devices, the device types the
// user granted, its keysym table, and the scroll left over from hi-res
// wheel steps.
type remoteInput struct {
	sink    inputSink
	granted uint32
	once    sync.Once
	keysyms map[uint32]keysymKey

	scrollMu sync.Mutex
	// scrollRest is each axis' 120ths of a detent not yet a whole step.
	scrollRest [2]int32

	touchMu sync.Mutex
	// touches maps each touch point down, by where it came from, to
	// the virtual touchscreen's id for it, and touchIDs has the bit of
	// each id taken.
	touches  map[touchKey]uint32
	touchIDs uint64
}

// touchKey is a touch point as its sender numbers it: a D-Bus slot or an
// EIS touch id, which may coincide.
type touchKey struct {
	eis bool
	id  uint32
}

// maxTouches bounds the points down at once, below the virtual
// touchscreen's id range.
const maxTouches = 64

// touchDown puts a new point down at (x, y) of extent (w, h); a point
// already down, or one past maxTouches, is dropped.
func (in *remoteInput) touchDown(k touchKey, x, y, w, h uint32) {
	in.touchMu.Lock()
	defer in.touchMu.Unlock()
	if _, down := in.touches[k]; down || len(in.touches) >= maxTouches {
		return
	}
	if in.touches == nil {
		in.touches = map[touchKey]uint32{}
	}
	id := intconv.Must[uint32](bits.TrailingZeros64(^in.touchIDs))
	in.touches[k] = id
	in.touchIDs |= 1 << id
	_ = in.sink.TouchDown(id, x, y, w, h)
}

func (in *remoteInput) touchMotion(k touchKey, x, y, w, h uint32) {
	in.touchMu.Lock()
	defer in.touchMu.Unlock()
	if id, down := in.touches[k]; down {
		_ = in.sink.TouchMotion(id, x, y, w, h)
	}
}

func (in *remoteInput) touchUp(k touchKey) {
	in.touchMu.Lock()
	defer in.touchMu.Unlock()
	if id, down := in.touches[k]; down {
		delete(in.touches, k)
		in.touchIDs &^= 1 << id
		_ = in.sink.TouchUp(id)
	}
}

// touchCancel cancels every point down, as wl_touch.cancel does.
func (in *remoteInput) touchCancel() {
	in.touchMu.Lock()
	defer in.touchMu.Unlock()
	if len(in.touches) > 0 {
		clear(in.touches)
		in.touchIDs = 0
		_ = in.sink.TouchCancel()
	}
}

// keysymKey is how a keysym is typed: its key, and whether shift and
// the level-3 key (AltGr) are held.
type keysymKey struct {
	code          uint32
	shift, level3 bool
	level3Code    uint32
}

// keysym types a keysym by its key in the seat's keymap, with shift
// and AltGr held as its level needs; one the keymap does not carry is
// dropped.
func (in *remoteInput) keysym(sym uint32, pressed bool) {
	in.once.Do(func() { in.keysyms = keysymTable(in.sink.Keymap()) })
	k, ok := in.keysyms[sym]
	if !ok {
		return
	}
	if pressed {
		if k.level3 {
			_ = in.sink.Key(k.level3Code, true)
		}
		if k.shift {
			_ = in.sink.Key(keyLeftShift, true)
		}
		_ = in.sink.Key(k.code, true)
		return
	}
	_ = in.sink.Key(k.code, false)
	if k.shift {
		_ = in.sink.Key(keyLeftShift, false)
	}
	if k.level3 {
		_ = in.sink.Key(k.level3Code, false)
	}
}

// scrollDiscrete scrolls v120 120ths of a detent on axis: the smooth
// amount at once, and a wheel step each time the 120ths add up to one,
// so hi-res wheels below a detent are not dropped.
func (in *remoteInput) scrollDiscrete(axis vinput.ScrollAxis, v120 int32) {
	if v120 == 0 {
		return
	}
	in.scrollMu.Lock()
	total := in.scrollRest[axis] + v120
	steps := total / 120
	in.scrollRest[axis] = total % 120
	in.scrollMu.Unlock()
	_ = in.sink.AxisDiscrete(vinput.SourceWheel, axis, float64(v120)*discreteStep/120, steps)
}

// keysymISOLevel3Shift is the AltGr key's keysym.
const keysymISOLevel3Shift = 0xfe03

// keysymTable maps each keysym of the keymap's first group to the
// evdev key that types it, on the plainest level: unshifted, shifted,
// then with AltGr and with AltGr and shift. Level 3 is reached through
// the key carrying ISO_Level3_Shift, and only when the keymap puts
// that key in Mod5, the modifier the levels are computed with. The xkb
// keycode is the evdev one plus 8.
func keysymTable(keymap []byte) map[uint32]keysymKey {
	table := map[uint32]keysymKey{}
	if len(keymap) == 0 {
		return table
	}
	km, err := xkb.NewContext(os.Getenv).NewKeymapFromString(keymap)
	if err != nil {
		warnf("remotedesktop: the seat keymap does not parse: %v", err)
		return table
	}
	add := func(mods xkb.ModMask, k keysymKey) {
		state := km.NewState()
		state.UpdateMask(mods, 0, 0, 0, 0, 0)
		for kc := km.MinKeycode(); kc <= km.MaxKeycode(); kc++ {
			if kc < 8 {
				continue
			}
			for _, sym := range state.KeyGetSyms(kc) {
				if _, taken := table[uint32(sym)]; !taken && sym != 0 {
					k.code = uint32(kc) - 8
					table[uint32(sym)] = k
				}
			}
		}
	}
	add(0, keysymKey{})
	add(xkb.ModShift, keysymKey{shift: true})
	level3, ok := table[keysymISOLevel3Shift]
	if !ok || km.KeyModMap(xkb.Keycode(level3.code+8))&xkb.ModMod5 == 0 {
		return table
	}
	add(xkb.ModMod5, keysymKey{level3: true, level3Code: level3.code})
	add(xkb.ModMod5|xkb.ModShift, keysymKey{shift: true, level3: true, level3Code: level3.code})
	return table
}

// remoteState is a RemoteDesktop session's options and, once started,
// its devices; it lives on the session, guarded by its mu.
type remoteState struct {
	types, persistMode uint32
	restore            *restoreGrant
	input              *remoteInput
}

// started is whether Start created the session's devices.
func (r *remoteState) started() bool { return r.input != nil }

// remoteDesktop is the interface: its sessions' state lives on the
// session records.
type remoteDesktop struct {
	conn     *dbus.Conn
	sessions *sessions
	// cast shares the session's screens and knows its streams.
	cast *screenCast
	// dial creates a session's devices, the touchscreen when asked
	// (dialInput; a fake in tests).
	dial func(touch bool) (inputSink, error)
	// touch is whether the compositor takes touch (touchOffered; fixed
	// in tests).
	touch func() bool
}

func newRemoteDesktop(conn *dbus.Conn, s *sessions, cast *screenCast, dial func(touch bool) (inputSink, error), touch func() bool) *remoteDesktop {
	return &remoteDesktop{conn: conn, sessions: s, cast: cast, dial: dial, touch: touch}
}

// offered is the device types AvailableDeviceTypes names.
func (r *remoteDesktop) offered() uint32 {
	if r.touch() {
		return allDevices
	}
	return deviceKeyboard | devicePointer
}

func (r *remoteDesktop) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    RemoteDesktopIface,
		Methods: remoteDesktopObject{r},
		Ordered: inputMethods(),
		Properties: dbusx.Getters{
			"AvailableDeviceTypes": func() any { return r.offered() },
			"version":              func() any { return uint32(2) },
		},
	}
}

// inputMethods are the interface's Notify* methods: xdg-desktop-portal
// forwards input without waiting for each reply, so they run in
// arrival order, or a click could land before the motion that placed
// it.
func inputMethods() []string {
	var out []string
	for m := range reflect.TypeFor[remoteDesktopObject]().Methods() {
		if strings.HasPrefix(m.Name, "Notify") {
			out = append(out, m.Name)
		}
	}
	return out
}

// session is the live RemoteDesktop session at path.
func (r *remoteDesktop) session(path dbus.ObjectPath) (*session, error) {
	s, ok := r.sessions.get(path)
	if !ok || s.kind != kindRemoteDesktop {
		return nil, fmt.Errorf("no remote-desktop session at %s", path)
	}
	return s, nil
}

// input is the started session's devices; an unknown or unstarted
// session is an error.
func (r *remoteDesktop) input(path dbus.ObjectPath) (*remoteInput, error) {
	s, err := r.session(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote.input == nil {
		return nil, fmt.Errorf("remote-desktop session %s is not started", path)
	}
	return s.remote.input, nil
}

// remoteDesktopObject carries the interface's D-Bus methods.
type remoteDesktopObject struct{ r *remoteDesktop }

// CreateSession opens a session.
func (o remoteDesktopObject) CreateSession(_, session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.r.sessions.create(session, kindRemoteDesktop, appID)
	if err != nil {
		warnf("remotedesktop: cannot mount session: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	s.remote = &remoteState{}
	s.mu.Unlock()
	s.onClose(func() {
		s.mu.Lock()
		in := s.remote.input
		s.remote.input = nil
		s.mu.Unlock()
		if in != nil {
			in.sink.Close()
		}
	})
	return ResponseSuccess, Vardict{}, nil
}

// SelectDevices records the device types (both when none are named),
// the persist mode, and a replayed restore token.
func (o remoteDesktopObject) SelectDevices(_, session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.r.sessions.lookup(session, appID, kindRemoteDesktop)
	if err != nil {
		warnf("remotedesktop: SelectDevices: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	offered := o.r.offered()
	types, ok := optU32(options, "types")
	if !ok {
		types = offered
	}
	if types == 0 || types&^offered != 0 {
		warnf("remotedesktop: SelectDevices: device types %#x, of %#x offered", types, offered)
		return ResponseOther, Vardict{}, nil
	}
	persist, _ := optU32(options, "persist_mode")
	var restore *restoreGrant
	if v, ok := options["restore_data"]; ok {
		if r, ok := decodeRestore(v); ok && r.devices != 0 {
			restore = &r
		}
	}
	s.mu.Lock()
	s.remote.types, s.remote.persistMode, s.remote.restore = types, persist, restore
	s.mu.Unlock()
	return ResponseSuccess, Vardict{}, nil
}

// remoteConsentBody says what a session asks for: control of its device
// types, the screen when it shares one, and the clipboard when it asked.
func remoteConsentBody(types uint32, sharing, clipboard bool) string {
	var asks []string
	if types != 0 {
		asks = append(asks, "to control "+phraseList(devicesNames(types)))
	}
	if sharing {
		asks = append(asks, "to see your screen")
	}
	if clipboard {
		asks = append(asks, "to use your clipboard")
	}
	if len(asks) == 0 {
		return "It asks for a remote desktop session with no devices."
	}
	return "It asks " + phraseList(asks) + "."
}

// devicesNames names device types, each with "your".
func devicesNames(types uint32) []string {
	var names []string
	for _, d := range []struct {
		bit  uint32
		name string
	}{{deviceKeyboard, "keyboard"}, {devicePointer, "pointer"}, {deviceTouchscreen, "touchscreen"}} {
		if types&d.bit != 0 {
			names = append(names, "your "+d.name)
		}
	}
	return names
}

// phraseList joins phrases as a sentence lists them: "a", "a and b",
// "a, b and c".
func phraseList(phrases []string) string {
	if len(phrases) == 1 {
		return phrases[0]
	}
	return strings.Join(phrases[:len(phrases)-1], ", ") + " and " + phrases[len(phrases)-1]
}

// Start asks the user to allow control of the selected devices (and
// picks the screens when the session shares them), unless a restore
// token of the same devices replays an earlier grant; then it creates
// the devices and replies them, the streams, whether the clipboard is
// enabled, and a restore token when persisting.
func (o remoteDesktopObject) Start(handle, session dbus.ObjectPath, appID, parent string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.r.sessions.lookup(session, appID, kindRemoteDesktop)
	if err != nil {
		warnf("remotedesktop: Start: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	dialogs := portaldialogs.NewClient(o.r.conn)
	c, ok := beginOnSession(o.r.conn, handle, s, parent, func(ctx context.Context, h string) error {
		return errors.Join(dialogs.Dismiss(ctx, h), sharepicker.NewClient(o.r.conn).Dismiss(ctx, h))
	})
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	s.mu.Lock()
	state, clipboard := *s.remote, s.clipboard
	s.mu.Unlock()
	sharing := sharesScreens(s)
	restoring := state.restore != nil && state.restore.devices == state.types
	if sharing {
		// The session's persist mode is RemoteDesktop's: it governs the
		// picker's token box, and a token replays the screens it kept.
		s.mu.Lock()
		s.cast.persistMode = state.persistMode
		if restoring && (len(state.restore.targets) > 0 || state.restore.virtual != nil) && state.restore.fits(s.cast.types) {
			s.cast.restore, s.cast.restoreVirtual, s.cast.restoreIssued = state.restore.targets, state.restore.virtual, state.restore.issued
		}
		s.mu.Unlock()
	}
	if !restoring {
		body := remoteConsentBody(state.types, sharing, clipboard)
		allowed, _, err := dialogs.Access(c.on(portaldialogs.ServiceName), c.caller, portaldialogs.AccessRequest{
			Title: "Allow remote control?", Body: body,
			GrantLabel: "Allow", DenyLabel: "Deny", Icon: remoteDesktopIcon,
		})
		if code := c.response("remotedesktop", allowed, err); code != ResponseSuccess {
			return ResponseCancelled, Vardict{}, nil
		}
	}
	results := Vardict{}
	var shared sharedScreens
	if sharing {
		var code uint32
		if shared, code = o.r.cast.share(c, s); code != ResponseSuccess {
			return code, Vardict{}, nil
		}
		results["streams"] = dbus.MakeVariant(shared.streams)
	}
	sink, err := o.r.dial(state.types&deviceTouchscreen != 0)
	if err != nil {
		warnf("remotedesktop: cannot start virtual input: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	if s.cleanups == nil {
		s.mu.Unlock()
		sink.Close()
		return ResponseCancelled, Vardict{}, nil
	}
	old := s.remote.input
	s.remote.input = &remoteInput{sink: sink, granted: state.types}
	s.mu.Unlock()
	if old != nil {
		old.sink.Close()
	}
	results["devices"] = dbus.MakeVariant(state.types)
	results["clipboard_enabled"] = dbus.MakeVariant(clipboard)
	if state.persistMode > 0 && (!sharing || shared.allowToken) {
		results["restore_data"] = dbus.MakeVariant(encodeRestore(o.r.cast.grant(state.types, shared)))
		results["persist_mode"] = dbus.MakeVariant(state.persistMode)
	}
	return ResponseSuccess, results, nil
}

// with runs fn on the started session's devices when the user granted
// the device type it drives; an unknown or unstarted session, or a
// device type not granted, is an error.
func (o remoteDesktopObject) with(session dbus.ObjectPath, device uint32, fn func(*remoteInput)) *dbus.Error {
	in, err := o.r.input(session)
	if err != nil {
		return dbusx.Failed(err.Error())
	}
	if in.granted&device == 0 {
		return dbusx.Failed(fmt.Sprintf("remote-desktop session %s was not granted device type %d", session, device))
	}
	fn(in)
	return nil
}

// NotifyPointerMotion moves the pointer relatively.
func (o remoteDesktopObject) NotifyPointerMotion(session dbus.ObjectPath, _ Vardict, dx, dy float64) *dbus.Error {
	return o.with(session, devicePointer, func(in *remoteInput) { _ = in.sink.Motion(dx, dy) })
}

// absoluteScale keeps a fraction of a logical pixel in the virtual
// devices' whole-number absolute positions.
const absoluteScale = 256

// absolutePoint is a point of the layout as the virtual devices' absolute
// positions take it: offset from the layout's corner and scaled by
// absoluteScale, within the layout's scaled extent.
type absolutePoint struct{ x, y, width, height uint32 }

// layoutPoint is (x, y), offset from the layout box's corner in logical
// pixels and clamped into the box, as an absolutePoint.
func layoutPoint(box image.Rectangle, x, y float64) absolutePoint {
	x = min(max(x, 0), float64(box.Dx()))
	y = min(max(y, 0), float64(box.Dy()))
	return absolutePoint{
		uint32(x * absoluteScale), uint32(y * absoluteScale),
		intconv.Clamp[uint32](box.Dx() * absoluteScale), intconv.Clamp[uint32](box.Dy() * absoluteScale),
	}
}

// streamPoint places (x, y) of one of the session's monitor streams, in
// its logical coordinates and clamped into it, by the stream's position
// in the compositor's layout.
func (o remoteDesktopObject) streamPoint(session dbus.ObjectPath, stream uint32, x, y float64) (absolutePoint, *dbus.Error) {
	s, err := o.r.session(session)
	if err != nil {
		return absolutePoint{}, dbusx.Failed(err.Error())
	}
	st, ok := o.r.cast.stream(s, stream)
	if !ok {
		return absolutePoint{}, dbusx.Failed(fmt.Sprintf("session %s runs no stream %d", session, stream))
	}
	outputs, err := o.r.cast.outputs()
	if err != nil {
		return absolutePoint{}, dbusx.Failed(fmt.Sprintf("no output layout for absolute input: %v", err))
	}
	sx, sy, sw, sh, ok := targetGeometry(st.target, outputs)
	if !ok {
		return absolutePoint{}, dbusx.Failed(fmt.Sprintf("stream %d has no place in the layout", stream))
	}
	box := layoutBox(outputs)
	gx := float64(sx) + min(max(x, 0), float64(sw))
	gy := float64(sy) + min(max(y, 0), float64(sh))
	return layoutPoint(box, gx-float64(box.Min.X), gy-float64(box.Min.Y)), nil
}

// NotifyPointerMotionAbsolute moves the pointer to (x, y) of one of the
// session's monitor streams, in its logical coordinates.
func (o remoteDesktopObject) NotifyPointerMotionAbsolute(session dbus.ObjectPath, _ Vardict, stream uint32, x, y float64) *dbus.Error {
	p, derr := o.streamPoint(session, stream, x, y)
	if derr != nil {
		return derr
	}
	return o.with(session, devicePointer, func(in *remoteInput) { _ = in.sink.MotionAbsolute(p.x, p.y, p.width, p.height) })
}

// eisRegions are the EIS regions of the session's monitor streams, cut
// from the layout box: one per output or region stream, offset from the
// box's corner, at its output's scale, with the stream's mapping_id. A
// session sharing no screen, or only windows, has none.
func (r *remoteDesktop) eisRegions(s *session) (image.Rectangle, []eis.Region, error) {
	streams := r.cast.streams(s)
	if !slices.ContainsFunc(streams, func(st startedStream) bool { return sourceTypeOf(st.target) == sourceMonitor }) {
		return image.Rectangle{}, nil, nil
	}
	outputs, err := r.cast.outputs()
	if err != nil {
		return image.Rectangle{}, nil, fmt.Errorf("no output layout for the EIS regions: %w", err)
	}
	box := layoutBox(outputs)
	var regions []eis.Region
	for _, st := range streams {
		x, y, w, h, ok := targetGeometry(st.target, outputs)
		if !ok {
			continue
		}
		i := slices.IndexFunc(outputs, func(o outputGeometry) bool { return o.name == st.target.Name })
		area := image.Rect(int(x), int(y), int(x)+int(w), int(y)+int(h)).Sub(box.Min)
		region, err := eisRegion(area, outputs[i].scale, mappingID(st))
		if err != nil {
			return image.Rectangle{}, nil, fmt.Errorf("stream %d: %w", st.node, err)
		}
		regions = append(regions, region)
	}
	return box, regions, nil
}

func eisRegion(area image.Rectangle, scale float32, mapping string) (eis.Region, error) {
	var r eis.Region
	var errs [4]error
	r.X, errs[0] = intconv.Exact[uint32](area.Min.X)
	r.Y, errs[1] = intconv.Exact[uint32](area.Min.Y)
	r.Width, errs[2] = intconv.Exact[uint32](area.Dx())
	r.Height, errs[3] = intconv.Exact[uint32](area.Dy())
	if err := errors.Join(errs[:]...); err != nil {
		return eis.Region{}, fmt.Errorf("region %v: %w", area, err)
	}
	r.Scale, r.MappingID = scale, mapping
	return r, nil
}

// layoutBox is the outputs' bounding box in the compositor's logical
// space.
func layoutBox(outputs []outputGeometry) image.Rectangle {
	var box image.Rectangle
	for _, o := range outputs {
		box = box.Union(image.Rect(int(o.x), int(o.y), int(o.x+o.width), int(o.y+o.height)))
	}
	return box
}

// NotifyPointerButton presses or releases an evdev button.
func (o remoteDesktopObject) NotifyPointerButton(session dbus.ObjectPath, _ Vardict, button int32, state uint32) *dbus.Error {
	code, derr := inputCode("button", button)
	if derr != nil {
		return derr
	}
	return o.with(session, devicePointer, func(in *remoteInput) { _ = in.sink.Button(code, state != 0) })
}

func inputCode(what string, v int32) (uint32, *dbus.Error) {
	code, err := intconv.Exact[uint32](v)
	if err != nil {
		return 0, dbusx.InvalidArgs(fmt.Sprintf("%s: %v", what, err))
	}
	return code, nil
}

// NotifyPointerAxis scrolls smoothly, vertical then horizontal, as a
// finger scroll does; finish ends the scroll on both axes (the fingers
// lifted), which lets the app run its kinetic scrolling.
func (o remoteDesktopObject) NotifyPointerAxis(session dbus.ObjectPath, options Vardict, dx, dy float64) *dbus.Error {
	finish, _ := optBool(options, "finish")
	return o.with(session, devicePointer, func(in *remoteInput) {
		smoothScroll(in.sink, dx, dy)
		if finish {
			stopScroll(in.sink, true, true)
		}
	})
}

func smoothScroll(sink inputSink, dx, dy float64) {
	if dy != 0 {
		_ = sink.Axis(vinput.SourceFinger, vinput.AxisVertical, dy)
	}
	if dx != 0 {
		_ = sink.Axis(vinput.SourceFinger, vinput.AxisHorizontal, dx)
	}
}

// stopScroll ends a finger scroll on the axes set, vertical first.
func stopScroll(sink inputSink, horizontal, vertical bool) {
	if vertical {
		_ = sink.AxisStop(vinput.SourceFinger, vinput.AxisVertical)
	}
	if horizontal {
		_ = sink.AxisStop(vinput.SourceFinger, vinput.AxisHorizontal)
	}
}

// axisOf is a wl_pointer axis: 1 horizontal, anything else vertical.
func axisOf(axis uint32) vinput.ScrollAxis {
	if axis == 1 {
		return vinput.AxisHorizontal
	}
	return vinput.AxisVertical
}

// discreteStep is one wheel click in axis units.
const discreteStep = 15

// NotifyPointerAxisDiscrete scrolls whole wheel steps.
func (o remoteDesktopObject) NotifyPointerAxisDiscrete(session dbus.ObjectPath, _ Vardict, axis uint32, steps int32) *dbus.Error {
	return o.with(session, devicePointer, func(in *remoteInput) {
		_ = in.sink.AxisDiscrete(vinput.SourceWheel, axisOf(axis), float64(steps)*discreteStep, steps)
	})
}

// NotifyKeyboardKeycode presses or releases an evdev key.
func (o remoteDesktopObject) NotifyKeyboardKeycode(session dbus.ObjectPath, _ Vardict, keycode int32, state uint32) *dbus.Error {
	code, derr := inputCode("keycode", keycode)
	if derr != nil {
		return derr
	}
	return o.with(session, deviceKeyboard, func(in *remoteInput) { _ = in.sink.Key(code, state != 0) })
}

// NotifyKeyboardKeysym types a keysym through the seat's keymap.
func (o remoteDesktopObject) NotifyKeyboardKeysym(session dbus.ObjectPath, _ Vardict, keysym int32, state uint32) *dbus.Error {
	sym, derr := inputCode("keysym", keysym)
	if derr != nil {
		return derr
	}
	return o.with(session, deviceKeyboard, func(in *remoteInput) { in.keysym(sym, state != 0) })
}

// NotifyTouchDown puts touch point slot down at (x, y) of one of the
// session's monitor streams, as NotifyPointerMotionAbsolute places the
// pointer.
func (o remoteDesktopObject) NotifyTouchDown(session dbus.ObjectPath, _ Vardict, stream, slot uint32, x, y float64) *dbus.Error {
	p, derr := o.streamPoint(session, stream, x, y)
	if derr != nil {
		return derr
	}
	return o.with(session, deviceTouchscreen, func(in *remoteInput) { in.touchDown(touchKey{id: slot}, p.x, p.y, p.width, p.height) })
}

// NotifyTouchMotion moves touch point slot to (x, y) of a stream.
func (o remoteDesktopObject) NotifyTouchMotion(session dbus.ObjectPath, _ Vardict, stream, slot uint32, x, y float64) *dbus.Error {
	p, derr := o.streamPoint(session, stream, x, y)
	if derr != nil {
		return derr
	}
	return o.with(session, deviceTouchscreen, func(in *remoteInput) { in.touchMotion(touchKey{id: slot}, p.x, p.y, p.width, p.height) })
}

// NotifyTouchUp lifts touch point slot.
func (o remoteDesktopObject) NotifyTouchUp(session dbus.ObjectPath, _ Vardict, slot uint32) *dbus.Error {
	return o.with(session, deviceTouchscreen, func(in *remoteInput) { in.touchUp(touchKey{id: slot}) })
}

// eisInput replays an EIS client's emulation onto a session's devices,
// the device types the user granted only. box is the layout the EIS
// regions were cut from: an absolute position is offset from its corner.
type eisInput struct {
	in  *remoteInput
	box image.Rectangle
}

func (e eisInput) pointer() bool  { return e.in.granted&devicePointer != 0 }
func (e eisInput) keyboard() bool { return e.in.granted&deviceKeyboard != 0 }
func (e eisInput) touch() bool    { return e.in.granted&deviceTouchscreen != 0 }

func (e eisInput) MotionAbsolute(x, y float64) {
	if e.pointer() {
		p := layoutPoint(e.box, x, y)
		_ = e.in.sink.MotionAbsolute(p.x, p.y, p.width, p.height)
	}
}

func (e eisInput) TouchDown(id uint32, x, y float64) {
	if e.touch() {
		p := layoutPoint(e.box, x, y)
		e.in.touchDown(touchKey{eis: true, id: id}, p.x, p.y, p.width, p.height)
	}
}

func (e eisInput) TouchMotion(id uint32, x, y float64) {
	if e.touch() {
		p := layoutPoint(e.box, x, y)
		e.in.touchMotion(touchKey{eis: true, id: id}, p.x, p.y, p.width, p.height)
	}
}

func (e eisInput) TouchUp(id uint32) {
	if e.touch() {
		e.in.touchUp(touchKey{eis: true, id: id})
	}
}

func (e eisInput) TouchCancel(uint32) {
	if e.touch() {
		e.in.touchCancel()
	}
}

func (e eisInput) Motion(dx, dy float64) {
	if e.pointer() {
		_ = e.in.sink.Motion(dx, dy)
	}
}

func (e eisInput) Button(code uint32, pressed bool) {
	if e.pointer() {
		_ = e.in.sink.Button(code, pressed)
	}
}

func (e eisInput) Scroll(dx, dy float64) {
	if e.pointer() {
		smoothScroll(e.in.sink, dx, dy)
	}
}

func (e eisInput) Key(code uint32, pressed bool) {
	if e.keyboard() {
		_ = e.in.sink.Key(code, pressed)
	}
}

func (e eisInput) Keysym(sym uint32, pressed bool) {
	if e.keyboard() {
		e.in.keysym(sym, pressed)
	}
}

// ScrollDiscrete scrolls 120ths of a detent, vertical then horizontal.
func (e eisInput) ScrollDiscrete(dx, dy int32) {
	if e.pointer() {
		e.in.scrollDiscrete(vinput.AxisVertical, dy)
		e.in.scrollDiscrete(vinput.AxisHorizontal, dx)
	}
}

// ScrollStop ends the client's finger scroll on the axes it names.
func (e eisInput) ScrollStop(x, y bool) {
	if e.pointer() {
		stopScroll(e.in.sink, x, y)
	}
}

// ConnectToEIS hands the app an EIS socket for the started session,
// which libei clients prefer to the Notify* calls, with an absolute
// region per monitor stream carrying the stream's mapping_id; godbus
// closes this end's copy once the reply is out.
func (o remoteDesktopObject) ConnectToEIS(session dbus.ObjectPath, _ string, _ Vardict) (*os.File, *dbus.Error) {
	in, err := o.r.input(session)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	s, err := o.r.session(session)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	box, regions, err := o.r.eisRegions(s)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	f, err := eis.Pair(eisInput{in: in, box: box}, shellName, regions)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	return f, nil
}
