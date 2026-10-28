package portal

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/eis"
	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/unxed/xkb-go"

	"github.com/stubbedev/lys-shell/shell/portaldialogs"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// RemoteDesktopIface lets a session drive the pointer and keyboard:
// after the user consents, the Notify* methods
// and an EIS connection are replayed onto a virtual pointer and
// keyboard. Touch is not supported.
const RemoteDesktopIface = "org.freedesktop.impl.portal.RemoteDesktop"

// Device types; touch (4) is not offered.
const (
	deviceKeyboard uint32 = 1
	devicePointer  uint32 = 2

	availableDevices = deviceKeyboard | devicePointer
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
	Keymap() []byte
	Close()
}

func dialInput() (inputSink, error) { return vinput.Dial("", vinput.Options{Keyboard: true}) }

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
	km, err := xkb.NewContext(context.Background(), xkb.ContextNoFlags).NewKeymapFromString(keymap, xkb.KeymapFormatTextV1)
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
	if !ok || !inModifierMap(keymap, km.KeyGetName(xkb.Keycode(level3.code+8)), "Mod5") {
		return table
	}
	add(xkb.ModMod5, keysymKey{level3: true, level3Code: level3.code})
	add(xkb.ModMod5|xkb.ModShift, keysymKey{shift: true, level3: true, level3Code: level3.code})
	return table
}

// inModifierMap reports whether the keymap text's modifier_map puts
// the key named name in the real modifier mod: libxkbcommon writes one
// `modifier_map <mod> { <KEY>, ... };` line per modifier, and the
// parser does not expose the map.
func inModifierMap(keymap []byte, name, mod string) bool {
	if name == "" {
		return false
	}
	for line := range strings.SplitSeq(string(keymap), "\n") {
		keys, ok := strings.CutPrefix(strings.TrimSpace(line), "modifier_map "+mod+" {")
		if ok && strings.Contains(keys, "<"+name+">") {
			return true
		}
	}
	return false
}

// remoteState is a RemoteDesktop session's options and, once started,
// its devices; it lives on the session, guarded by its mu.
type remoteState struct {
	types, persistMode uint32
	restore            *remoteRestore
	input              *remoteInput
}

// remoteRestore is what a RemoteDesktop restore token keeps: the device
// types the user granted and the screens shared with them.
type remoteRestore struct {
	devices uint32
	targets []sharepicker.Target
}

// remoteTokenPrefix marks a RemoteDesktop token's data, which a
// ScreenCast token's never starts with.
const remoteTokenPrefix = "remote:"

func encodeRemoteRestore(r remoteRestore) restoreToken {
	data := remoteTokenPrefix + strconv.FormatUint(uint64(r.devices), 10) + "/" + sharepicker.JoinTargets(r.targets)
	return restoreToken{restoreVendor, restoreVersion, dbus.MakeVariant(data)}
}

// decodeRemoteRestore reads a RemoteDesktop token this portal issued;
// any other is none.
func decodeRemoteRestore(v dbus.Variant) (remoteRestore, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 3 || fields[0] != restoreVendor {
		return remoteRestore{}, false
	}
	data, _ := fields[2].(dbus.Variant)
	payload, _ := data.Value().(string)
	rest, ok := strings.CutPrefix(payload, remoteTokenPrefix)
	devices, targets, found := strings.Cut(rest, "/")
	if !ok || !found {
		return remoteRestore{}, false
	}
	n, err := strconv.ParseUint(devices, 10, 32)
	if err != nil || n == 0 || uint32(n)&^availableDevices != 0 {
		return remoteRestore{}, false
	}
	r := remoteRestore{devices: uint32(n)}
	if targets != "" {
		if r.targets, ok = sharepicker.ParseTargets(targets); !ok {
			return remoteRestore{}, false
		}
	}
	return r, true
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
	// dial creates a session's devices (dialInput; a fake in tests).
	dial func() (inputSink, error)
}

func newRemoteDesktop(conn *dbus.Conn, s *sessions, cast *screenCast, dial func() (inputSink, error)) *remoteDesktop {
	return &remoteDesktop{conn: conn, sessions: s, cast: cast, dial: dial}
}

func (r *remoteDesktop) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    RemoteDesktopIface,
		Methods: remoteDesktopObject{r},
		Ordered: inputMethods(),
		Properties: dbusx.Getters{
			"AvailableDeviceTypes": func() any { return availableDevices },
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
	types, ok := optU32(options, "types")
	if !ok {
		types = availableDevices
	}
	if types == 0 || types&^availableDevices != 0 {
		warnf("remotedesktop: SelectDevices: device types %#x, of %#x offered", types, availableDevices)
		return ResponseOther, Vardict{}, nil
	}
	persist, _ := optU32(options, "persist_mode")
	var restore *remoteRestore
	if v, ok := options["restore_data"]; ok {
		if r, ok := decodeRemoteRestore(v); ok {
			restore = &r
		}
	}
	s.mu.Lock()
	s.remote.types, s.remote.persistMode, s.remote.restore = types, persist, restore
	s.mu.Unlock()
	return ResponseSuccess, Vardict{}, nil
}

// devicesPhrase names device types for the consent dialog.
func devicesPhrase(types uint32) string {
	switch types {
	case deviceKeyboard:
		return "your keyboard"
	case devicePointer:
		return "your pointer"
	}
	return "your keyboard and pointer"
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
	c, ok := beginOr(o.r.conn, handle, appID, parent, func(ctx context.Context, h string) error {
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
		if restoring && len(state.restore.targets) > 0 && allOfTypes(state.restore.targets, s.cast.types) {
			s.cast.restore = state.restore.targets
		}
		s.mu.Unlock()
	}
	if !restoring {
		body := "It asks to control " + devicesPhrase(state.types) + "."
		if sharing {
			body = "It asks to control " + devicesPhrase(state.types) + " and to see your screen."
		}
		allowed, _, err := dialogs.Access(c.ctx, c.caller, portaldialogs.AccessRequest{
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
	sink, err := o.r.dial()
	if err != nil {
		warnf("remotedesktop: cannot start virtual input: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	old := s.remote.input
	s.remote.input = &remoteInput{sink: sink, granted: state.types}
	s.mu.Unlock()
	if old != nil {
		old.sink.Close()
	}
	results["devices"] = dbus.MakeVariant(state.types)
	results["clipboard_enabled"] = dbus.MakeVariant(clipboard)
	if state.persistMode > 0 && (!sharing || shared.allowToken) {
		results["restore_data"] = dbus.MakeVariant(encodeRemoteRestore(remoteRestore{devices: state.types, targets: shared.targets}))
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
// pointer's whole-number absolute motion.
const absoluteScale = 256

// NotifyPointerMotionAbsolute moves the pointer to (x, y) of one of the
// session's monitor streams, in its logical coordinates: placed by the
// stream's position in the compositor's layout, whose extent the
// virtual pointer's absolute motion spans.
func (o remoteDesktopObject) NotifyPointerMotionAbsolute(session dbus.ObjectPath, _ Vardict, stream uint32, x, y float64) *dbus.Error {
	s, err := o.r.session(session)
	if err != nil {
		return dbusx.Failed(err.Error())
	}
	st, ok := o.r.cast.stream(s, stream)
	if !ok {
		return dbusx.Failed(fmt.Sprintf("session %s runs no stream %d", session, stream))
	}
	outputs, err := o.r.cast.outputs()
	if err != nil {
		return dbusx.Failed(fmt.Sprintf("no output layout for absolute motion: %v", err))
	}
	sx, sy, sw, sh, ok := targetGeometry(st.target, outputs)
	if !ok {
		return dbusx.Failed(fmt.Sprintf("stream %d has no place in the layout", stream))
	}
	box := layoutBox(outputs)
	gx := float64(sx) + min(max(x, 0), float64(sw))
	gy := float64(sy) + min(max(y, 0), float64(sh))
	return o.with(session, devicePointer, func(in *remoteInput) {
		_ = in.sink.MotionAbsolute(
			uint32((gx-float64(box.Min.X))*absoluteScale), uint32((gy-float64(box.Min.Y))*absoluteScale),
			intconv.Clamp[uint32](box.Dx()*absoluteScale), intconv.Clamp[uint32](box.Dy()*absoluteScale))
	})
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

// NotifyTouchDown is unsupported.
func (remoteDesktopObject) NotifyTouchDown(_ dbus.ObjectPath, _ Vardict, _, _ uint32, _, _ float64) *dbus.Error {
	warnf("remotedesktop: touch input not supported")
	return nil
}

// NotifyTouchMotion is unsupported.
func (remoteDesktopObject) NotifyTouchMotion(_ dbus.ObjectPath, _ Vardict, _, _ uint32, _, _ float64) *dbus.Error {
	return nil
}

// NotifyTouchUp is unsupported.
func (remoteDesktopObject) NotifyTouchUp(_ dbus.ObjectPath, _ Vardict, _ uint32) *dbus.Error {
	return nil
}

// eisInput replays an EIS client's emulation onto a session's devices,
// the device types the user granted only.
type eisInput struct{ in *remoteInput }

func (e eisInput) pointer() bool  { return e.in.granted&devicePointer != 0 }
func (e eisInput) keyboard() bool { return e.in.granted&deviceKeyboard != 0 }

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
// which libei clients prefer to the Notify* calls; godbus closes this
// end's copy once the reply is out.
func (o remoteDesktopObject) ConnectToEIS(session dbus.ObjectPath, _ string, _ Vardict) (*os.File, *dbus.Error) {
	in, err := o.r.input(session)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	f, err := eis.Pair(eisInput{in}, shellName)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	return f, nil
}
