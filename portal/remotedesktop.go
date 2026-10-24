package portal

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/vinput"
	"github.com/unxed/xkb-go"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/eis"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// RemoteDesktopIface lets a session drive the pointer and keyboard
// (remotedesktop/mod.rs): after the user consents, the Notify* methods
// and an EIS connection are replayed onto a virtual pointer and
// keyboard. Touch is not supported.
const RemoteDesktopIface = "org.freedesktop.impl.portal.RemoteDesktop"

// Device types.
const (
	deviceKeyboard uint32 = 1
	devicePointer  uint32 = 2
)

// keyLeftShift is the evdev code a shifted keysym is typed with.
const keyLeftShift = 42

// inputSink is where a session's input goes (a vinput.Device; a fake
// in tests).
type inputSink interface {
	Motion(dx, dy float64) error
	MotionAbsolute(x, y, width, height uint32) error
	Button(code uint32, pressed bool) error
	Axis(axis uint32, value float64) error
	AxisDiscrete(axis uint32, value float64, steps int32) error
	Key(code uint32, pressed bool) error
	Keymap() []byte
	Close()
}

func dialInput() (inputSink, error) { return vinput.Dial("", vinput.Options{Keyboard: true}) }

// remoteInput is a started session's devices and keysym table.
type remoteInput struct {
	sink    inputSink
	once    sync.Once
	keysyms map[uint32]keysymKey
}

// keysymKey is how a keysym is typed: its key and whether shift is held.
type keysymKey struct {
	code  uint32
	shift bool
}

// keysym types a keysym by its key in the seat's keymap, shift around
// it when the symbol is on the shifted level; one the keymap does not
// carry is dropped.
func (in *remoteInput) keysym(sym uint32, pressed bool) {
	in.once.Do(func() { in.keysyms = keysymTable(in.sink.Keymap()) })
	k, ok := in.keysyms[sym]
	if !ok {
		return
	}
	if pressed {
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
}

// keysymTable maps each keysym of the keymap's first group to the
// evdev key that types it, unshifted first. The xkb keycode is the
// evdev one plus 8.
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
	for _, shift := range []bool{false, true} {
		state := km.NewState()
		if shift {
			state.UpdateMask(xkb.ModShift, 0, 0, 0, 0, 0)
		}
		for kc := km.MinKeycode(); kc <= km.MaxKeycode(); kc++ {
			if kc < 8 {
				continue
			}
			for _, sym := range state.KeyGetSyms(kc) {
				if _, taken := table[uint32(sym)]; !taken && sym != 0 {
					table[uint32(sym)] = keysymKey{uint32(kc) - 8, shift}
				}
			}
		}
	}
	return table
}

// remoteState is a RemoteDesktop session's device types and, once
// started, its devices; it lives on the session, guarded by its mu.
type remoteState struct {
	types uint32
	input *remoteInput
}

// remoteDesktop is the interface: its sessions' state lives on the
// session records.
type remoteDesktop struct {
	conn     *dbus.Conn
	sessions *sessions
	sizes    *streamSizes
	// dial creates a session's devices (dialInput; a fake in tests).
	dial func() (inputSink, error)
}

func newRemoteDesktop(conn *dbus.Conn, s *sessions, sizes *streamSizes, dial func() (inputSink, error)) *remoteDesktop {
	return &remoteDesktop{conn: conn, sessions: s, sizes: sizes, dial: dial}
}

func (r *remoteDesktop) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    RemoteDesktopIface,
		Methods: remoteDesktopObject{r},
		Properties: dbusx.Getters{
			"AvailableDeviceTypes": func() any { return deviceKeyboard | devicePointer },
			"version":              func() any { return uint32(2) },
		},
	}
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

// SelectDevices records the device types (both when none are named).
func (o remoteDesktopObject) SelectDevices(_, session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.r.sessions.lookup(session, appID, kindRemoteDesktop)
	if err != nil {
		warnf("remotedesktop: SelectDevices: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	types, ok := optU32(options, "types")
	if !ok {
		types = deviceKeyboard | devicePointer
	}
	s.mu.Lock()
	s.remote.types = types
	s.mu.Unlock()
	return ResponseSuccess, Vardict{}, nil
}

// Start asks the user to allow control, then creates the devices and
// replies the granted types.
func (o remoteDesktopObject) Start(_, session dbus.ObjectPath, appID, _ string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.r.sessions.lookup(session, appID, kindRemoteDesktop)
	if err != nil {
		warnf("remotedesktop: Start: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	types := s.remote.types
	s.mu.Unlock()
	granted := deviceKeyboard | devicePointer
	if types != 0 {
		granted = types & (deviceKeyboard | devicePointer)
	}
	allowed, err := portaldialogs.NewClient(o.r.conn).Access(context.Background(), portaldialogs.AccessRequest{
		Title: "Remote control", Body: "An application is requesting control of your pointer and keyboard.",
		GrantLabel: "Allow", DenyLabel: "Deny", Icon: "input-keyboard-symbolic",
	})
	if err != nil || !allowed {
		if err != nil {
			warnf("remotedesktop: consent dialog unavailable; denying: %v", err)
		}
		return ResponseCancelled, Vardict{}, nil
	}
	sink, err := o.r.dial()
	if err != nil {
		warnf("remotedesktop: cannot start virtual input: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	s.mu.Lock()
	old := s.remote.input
	s.remote.input = &remoteInput{sink: sink}
	s.mu.Unlock()
	if old != nil {
		old.sink.Close()
	}
	return ResponseSuccess, Vardict{"devices": dbus.MakeVariant(granted)}, nil
}

// with runs fn on the started session's devices; an unknown or
// unstarted session is an error.
func (o remoteDesktopObject) with(session dbus.ObjectPath, fn func(*remoteInput)) *dbus.Error {
	in, err := o.r.input(session)
	if err != nil {
		return dbusx.Failed(err.Error())
	}
	fn(in)
	return nil
}

// NotifyPointerMotion moves the pointer relatively.
func (o remoteDesktopObject) NotifyPointerMotion(session dbus.ObjectPath, _ Vardict, dx, dy float64) *dbus.Error {
	return o.with(session, func(in *remoteInput) { _ = in.sink.Motion(dx, dy) })
}

// NotifyPointerMotionAbsolute moves the pointer within a ScreenCast
// stream's extent; an unknown stream is dropped.
func (o remoteDesktopObject) NotifyPointerMotionAbsolute(session dbus.ObjectPath, _ Vardict, stream uint32, x, y float64) *dbus.Error {
	size, ok := o.r.sizes.get(stream)
	if !ok {
		return nil
	}
	return o.with(session, func(in *remoteInput) {
		_ = in.sink.MotionAbsolute(uint32(max(x, 0)), uint32(max(y, 0)), uint32(max(size[0], 1)), uint32(max(size[1], 1)))
	})
}

// NotifyPointerButton presses or releases an evdev button.
func (o remoteDesktopObject) NotifyPointerButton(session dbus.ObjectPath, _ Vardict, button int32, state uint32) *dbus.Error {
	return o.with(session, func(in *remoteInput) { _ = in.sink.Button(uint32(button), state != 0) })
}

// NotifyPointerAxis scrolls smoothly, vertical then horizontal.
func (o remoteDesktopObject) NotifyPointerAxis(session dbus.ObjectPath, _ Vardict, dx, dy float64) *dbus.Error {
	return o.with(session, func(in *remoteInput) { smoothScroll(in.sink, dx, dy) })
}

func smoothScroll(sink inputSink, dx, dy float64) {
	if dy != 0 {
		_ = sink.Axis(0, dy)
	}
	if dx != 0 {
		_ = sink.Axis(1, dx)
	}
}

// axisOf is a wl_pointer axis: 1 horizontal, anything else vertical.
func axisOf(axis uint32) uint32 {
	if axis == 1 {
		return 1
	}
	return 0
}

// discreteStep is one wheel click in axis units.
const discreteStep = 15

// NotifyPointerAxisDiscrete scrolls whole wheel steps.
func (o remoteDesktopObject) NotifyPointerAxisDiscrete(session dbus.ObjectPath, _ Vardict, axis uint32, steps int32) *dbus.Error {
	return o.with(session, func(in *remoteInput) {
		_ = in.sink.AxisDiscrete(axisOf(axis), float64(steps)*discreteStep, steps)
	})
}

// NotifyKeyboardKeycode presses or releases an evdev key.
func (o remoteDesktopObject) NotifyKeyboardKeycode(session dbus.ObjectPath, _ Vardict, keycode int32, state uint32) *dbus.Error {
	return o.with(session, func(in *remoteInput) { _ = in.sink.Key(uint32(keycode), state != 0) })
}

// NotifyKeyboardKeysym types a keysym through the seat's keymap.
func (o remoteDesktopObject) NotifyKeyboardKeysym(session dbus.ObjectPath, _ Vardict, keysym int32, state uint32) *dbus.Error {
	return o.with(session, func(in *remoteInput) { in.keysym(uint32(keysym), state != 0) })
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

// eisInput replays an EIS client's emulation onto a session's devices.
type eisInput struct{ in *remoteInput }

func (e eisInput) Motion(dx, dy float64)            { _ = e.in.sink.Motion(dx, dy) }
func (e eisInput) Button(code uint32, pressed bool) { _ = e.in.sink.Button(code, pressed) }
func (e eisInput) Scroll(dx, dy float64)            { smoothScroll(e.in.sink, dx, dy) }
func (e eisInput) Key(code uint32, pressed bool)    { _ = e.in.sink.Key(code, pressed) }
func (e eisInput) Keysym(sym uint32, pressed bool)  { e.in.keysym(sym, pressed) }

// ScrollDiscrete turns 120ths of a detent into whole wheel steps,
// vertical then horizontal.
func (e eisInput) ScrollDiscrete(dx, dy int32) {
	if dy != 0 {
		_ = e.in.sink.AxisDiscrete(0, float64(dy/120)*discreteStep, dy/120)
	}
	if dx != 0 {
		_ = e.in.sink.AxisDiscrete(1, float64(dx/120)*discreteStep, dx/120)
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
	f, err := eis.Pair(eisInput{in})
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	return f, nil
}
