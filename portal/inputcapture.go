package portal

import (
	"cmp"
	"errors"
	"fmt"
	"image"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/eis"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/wmipc"
	"github.com/stubbedev/lys-ui/app"
)

// InputCaptureIface hands an app the local keyboard and pointer once
// the pointer pushes through one of its barriers on a screen edge, for
// software KVMs; lys-wm's capture IPC does the capturing.
const InputCaptureIface = "org.freedesktop.impl.portal.InputCapture"

const (
	captureKeyboard     = deviceKeyboard
	capturePointer      = devicePointer
	captureCapabilities = captureKeyboard | capturePointer
	captureKeymapBound  = 5 * time.Second
)

type zoneSet struct {
	id    uint32
	zones []outputGeometry
}

type captureSession struct {
	s           *session
	caps        uint32
	link        *lyswm.Capture
	keymap      string
	keymapReady chan struct{}
	recv        *eis.Receiver
	active      bool
	activation  uint32
}

type inputCapture struct {
	conn         *dbus.Conn
	sessions     *sessions
	dial         func() (*lyswm.Capture, error)
	outputs      func() ([]outputGeometry, error)
	watchOutputs func() (ticks <-chan struct{}, stop func(), err error)

	mu        sync.Mutex
	set       zoneSet
	live      map[dbus.ObjectPath]*captureSession
	stopWatch func()
}

func newInputCapture(conn *dbus.Conn, s *sessions, dial func() (*lyswm.Capture, error), outputs func() ([]outputGeometry, error), watchOutputs func() (<-chan struct{}, func(), error)) *inputCapture {
	return &inputCapture{conn: conn, sessions: s, dial: dial, outputs: outputs, watchOutputs: watchOutputs, live: map[dbus.ObjectPath]*captureSession{}}
}

func watchLysWMOutputs() (<-chan struct{}, func(), error) {
	w, err := lyswm.Watch(wmipc.EventOutput)
	if err != nil {
		return nil, nil, err
	}
	return w.Ticks(), w.Close, nil
}

func (w *waylandLoop) syncedOutputs() ([]outputGeometry, error) {
	err := w.do(func(*app.Application) error {
		w.mu.Lock()
		sess := w.sess
		w.mu.Unlock()
		rt, ok := sess.(interface{ Roundtrip() error })
		if !ok {
			return fmt.Errorf("inputcapture: the wayland session %T cannot roundtrip", sess)
		}
		return rt.Roundtrip()
	})
	if err != nil {
		return nil, fmt.Errorf("inputcapture: sync with the compositor: %w", err)
	}
	return w.outputs()
}

func (ic *inputCapture) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    InputCaptureIface,
		Methods: inputCaptureObject{ic},
		Properties: dbusx.Getters{
			"SupportedCapabilities": func() any { return captureCapabilities },
			"version":               func() any { return uint32(2) },
		},
	}
}

func (ic *inputCapture) refreshZones() (zoneSet, error) {
	outs, err := ic.outputs()
	if err != nil {
		return zoneSet{}, err
	}
	slices.SortFunc(outs, func(a, b outputGeometry) int {
		return cmp.Or(cmp.Compare(a.x, b.x), cmp.Compare(a.y, b.y), cmp.Compare(a.name, b.name))
	})
	ic.mu.Lock()
	old := ic.set
	if old.id != 0 && slices.Equal(outs, old.zones) {
		ic.mu.Unlock()
		return old, nil
	}
	ic.set = zoneSet{id: old.id + 1, zones: outs}
	set := ic.set
	var started []*session
	for _, cs := range ic.live {
		if cs.link != nil {
			started = append(started, cs.s)
		}
	}
	ic.mu.Unlock()
	if old.id == 0 {
		return set, nil
	}
	for _, s := range started {
		ic.emit(s, "ZonesChanged", Vardict{"zone_set": dbus.MakeVariant(old.id)})
	}
	return set, nil
}

func (ic *inputCapture) emit(s *session, signal string, options Vardict) {
	if err := ic.conn.Emit(ObjectPath, InputCaptureIface+"."+signal, s.path, options); err != nil {
		warnf("inputcapture: emit %s for %s: %v", signal, s.path, err)
	}
}

func (ic *inputCapture) add(s *session) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if len(ic.live) == 0 {
		ticks, stop, err := ic.watchOutputs()
		if err != nil {
			return fmt.Errorf("follow lys-wm's outputs: %w", err)
		}
		ic.stopWatch = stop
		go func() {
			for range ticks {
				if _, err := ic.refreshZones(); err != nil {
					warnf("inputcapture: read the zones after an output change: %v", err)
				}
			}
		}()
	}
	ic.live[s.path] = &captureSession{s: s, keymapReady: make(chan struct{})}
	return nil
}

func (ic *inputCapture) drop(s *session) {
	ic.mu.Lock()
	cs := ic.live[s.path]
	delete(ic.live, s.path)
	var stop func()
	if len(ic.live) == 0 {
		stop, ic.stopWatch = ic.stopWatch, nil
	}
	ic.mu.Unlock()
	if stop != nil {
		stop()
	}
	if cs == nil {
		return
	}
	if cs.link != nil {
		cs.link.Close()
	}
	if cs.recv != nil {
		if err := cs.recv.Close(); err != nil {
			warnf("inputcapture: close the EIS receiver of %s: %v", s.path, err)
		}
	}
}

func (ic *inputCapture) lookup(path dbus.ObjectPath, appID string) (*captureSession, error) {
	if _, err := ic.sessions.lookup(path, appID, kindInputCapture); err != nil {
		return nil, err
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	cs, ok := ic.live[path]
	if !ok {
		return nil, fmt.Errorf("input-capture session %s has ended", path)
	}
	return cs, nil
}

func (ic *inputCapture) started(path dbus.ObjectPath, appID string) (*captureSession, *lyswm.Capture, error) {
	cs, err := ic.lookup(path, appID)
	if err != nil {
		return nil, nil, err
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if cs.link == nil {
		return nil, nil, fmt.Errorf("input-capture session %s is not started", path)
	}
	return cs, cs.link, nil
}

func (ic *inputCapture) pump(cs *captureSession, link *lyswm.Capture) {
	for e := range link.Events() {
		ic.handle(cs, e)
	}
	if !cs.s.closed() {
		warnf("inputcapture: session %s ends with its capture: %v", cs.s.path, link.Err())
		ic.sessions.close(cs.s)
	}
}

func capturePosition(p *wmipc.CapturePoint) dbus.Variant {
	if p == nil {
		return dbus.MakeVariant(struct{ X, Y float64 }{})
	}
	return dbus.MakeVariant(struct{ X, Y float64 }{p.X, p.Y})
}

func (ic *inputCapture) handle(cs *captureSession, e lyswm.CaptureEvent) {
	ic.mu.Lock()
	recv, caps := cs.recv, cs.caps
	ic.mu.Unlock()
	var err error
	switch e.Event {
	case "keymap":
		ic.mu.Lock()
		first := cs.keymap == ""
		cs.keymap, recv = e.Keymap, cs.recv
		ic.mu.Unlock()
		if first {
			close(cs.keymapReady)
		}
		if recv != nil {
			err = recv.SetKeymap(e.Keymap)
		}
	case "activated":
		ic.mu.Lock()
		cs.active, cs.activation = true, e.Activation
		ic.mu.Unlock()
		if recv != nil {
			err = recv.Start(e.Activation)
		}
		ic.emit(cs.s, "Activated", Vardict{
			"activation_id": dbus.MakeVariant(e.Activation), "cursor_position": capturePosition(e.At), "barrier_id": dbus.MakeVariant(e.Barrier),
		})
	case "deactivated":
		err = ic.stopEmulating(cs)
		ic.emit(cs.s, "Deactivated", Vardict{"activation_id": dbus.MakeVariant(e.Activation), "cursor_position": capturePosition(e.At)})
	case "disabled":
		ic.emit(cs.s, "Disabled", Vardict{})
	default:
		err = forwardCaptured(recv, caps, e)
	}
	if err != nil {
		warnf("inputcapture: %s on %s: %v", e.Event, cs.s.path, err)
	}
}

func (ic *inputCapture) stopEmulating(cs *captureSession) error {
	ic.mu.Lock()
	active, recv := cs.active, cs.recv
	cs.active = false
	ic.mu.Unlock()
	if !active || recv == nil {
		return nil
	}
	return recv.Stop()
}

func forwardCaptured(recv *eis.Receiver, caps uint32, e lyswm.CaptureEvent) error {
	if recv == nil {
		return nil
	}
	pointer, keyboard := caps&capturePointer != 0, caps&captureKeyboard != 0
	switch {
	case e.Event == "key" && keyboard:
		return recv.Key(e.Key, e.Pressed)
	case e.Event == "modifiers" && keyboard:
		return recv.Modifiers(e.Depressed, e.Latched, e.Locked, e.Group)
	case !pointer:
		return nil
	}
	switch e.Event {
	case "motion":
		return recv.Motion(e.DX, e.DY)
	case "button":
		return recv.Button(e.Button, e.Pressed)
	case "scroll":
		return recv.Scroll(e.DX, e.DY)
	case "scroll_discrete":
		return recv.ScrollDiscrete(int32(e.DX), int32(e.DY))
	case "scroll_stop":
		return recv.ScrollStop(e.X, e.Y)
	case "frame":
		return recv.PointerFrame()
	}
	return nil
}

type inputCaptureObject struct{ ic *inputCapture }

// CreateSession2 opens a session, not yet started.
func (o inputCaptureObject) CreateSession2(session dbus.ObjectPath, appID string, _ Vardict) (Vardict, *dbus.Error) {
	s, err := o.ic.sessions.create(session, kindInputCapture, appID)
	if err != nil {
		return nil, dbusx.Failed(fmt.Sprintf("inputcapture: %v", err))
	}
	if err := o.ic.add(s); err != nil {
		o.ic.sessions.close(s)
		return nil, dbusx.Failed(fmt.Sprintf("inputcapture: %v", err))
	}
	s.onClose(func() { o.ic.drop(s) })
	return Vardict{}, nil
}

// Start asks the user to let the app capture the capabilities it names,
// then opens the capture on lys-wm and replies the capabilities granted.
func (o inputCaptureObject) Start(handle, session dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	ic := o.ic
	cs, err := ic.lookup(session, appID)
	if err != nil {
		warnf("inputcapture: Start: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	requested, ok := optU32(options, "capabilities")
	granted := requested & captureCapabilities
	if !ok || granted == 0 {
		warnf("inputcapture: Start: capabilities %#x, of %#x offered", requested, captureCapabilities)
		return ResponseOther, Vardict{}, nil
	}
	ic.mu.Lock()
	already := cs.link != nil
	ic.mu.Unlock()
	if already {
		warnf("inputcapture: Start: session %s is started already", session)
		return ResponseOther, Vardict{}, nil
	}
	dialogs := portaldialogs.NewClient(ic.conn)
	c, ok := beginOnSession(ic.conn, handle, cs.s, parent, dialogs.Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	allowed, _, err := dialogs.Access(c.on(portaldialogs.ServiceName), c.caller, portaldialogs.AccessRequest{
		Title:      "Allow input capture?",
		Body:       "It asks to take " + phraseList(devicesNames(granted)) + " whenever the pointer pushes past a screen edge, to use them on another computer. Super+Escape takes them back.",
		GrantLabel: "Allow", DenyLabel: "Deny", Icon: remoteDesktopIcon,
	})
	if code := c.response("inputcapture", allowed, err); code != ResponseSuccess {
		return ResponseCancelled, Vardict{}, nil
	}
	link, err := ic.dial()
	if err != nil {
		warnf("inputcapture: open the capture on lys-wm: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	ic.mu.Lock()
	if cs.s.closed() || cs.link != nil {
		ic.mu.Unlock()
		link.Close()
		return ResponseCancelled, Vardict{}, nil
	}
	cs.link, cs.caps = link, granted
	ic.mu.Unlock()
	go ic.pump(cs, link)
	return ResponseSuccess, Vardict{"capabilities": dbus.MakeVariant(granted)}, nil
}

type zoneWire struct {
	Width, Height uint32
	X, Y          int32
}

// GetZones replies the outputs' logical rectangles and the zone set
// they form.
func (o inputCaptureObject) GetZones(_, session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	if _, err := o.ic.lookup(session, appID); err != nil {
		warnf("inputcapture: GetZones: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	set, err := o.ic.refreshZones()
	if err != nil {
		warnf("inputcapture: GetZones: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	zones := make([]zoneWire, 0, len(set.zones))
	for _, z := range set.zones {
		zones = append(zones, zoneWire{Width: uint32(max(z.width, 0)), Height: uint32(max(z.height, 0)), X: z.x, Y: z.y})
	}
	return ResponseSuccess, Vardict{"zones": dbus.MakeVariant(zones), "zone_set": dbus.MakeVariant(set.id)}, nil
}

func zoneRect(z outputGeometry) image.Rectangle {
	return image.Rect(int(z.x), int(z.y), int(z.x)+int(z.width), int(z.y)+int(z.height))
}

func onZoneEdge(b lyswm.Barrier, zones []outputGeometry) bool {
	x1, x2 := int(min(b.X1, b.X2)), int(max(b.X1, b.X2))
	y1, y2 := int(min(b.Y1, b.Y2)), int(max(b.Y1, b.Y2))
	outside := func(strip image.Rectangle) bool {
		return !slices.ContainsFunc(zones, func(z outputGeometry) bool { return strip.Overlaps(zoneRect(z)) })
	}
	for _, z := range zones {
		r := zoneRect(z)
		vertical := x1 == x2 && y1 >= r.Min.Y && y2 < r.Max.Y
		horizontal := y1 == y2 && x1 >= r.Min.X && x2 < r.Max.X
		switch {
		case vertical && x1 == r.Min.X && outside(image.Rect(x1-1, y1, x1, y2+1)),
			vertical && x1 == r.Max.X && outside(image.Rect(x1, y1, x1+1, y2+1)),
			horizontal && y1 == r.Min.Y && outside(image.Rect(x1, y1-1, x2+1, y1)),
			horizontal && y1 == r.Max.Y && outside(image.Rect(x1, y1, x2+1, y1+1)):
			return true
		}
	}
	return false
}

func parseBarrier(d Vardict) (lyswm.Barrier, uint32, bool) {
	id, ok := optU32(d, "barrier_id")
	if !ok || id == 0 {
		return lyswm.Barrier{}, id, false
	}
	pos, ok := d["position"].Value().([]any)
	if !ok || len(pos) != 4 {
		return lyswm.Barrier{}, id, false
	}
	var c [4]int32
	for i, v := range pos {
		n, ok := v.(int32)
		if !ok {
			return lyswm.Barrier{}, id, false
		}
		c[i] = n
	}
	return lyswm.Barrier{ID: id, X1: c[0], Y1: c[1], X2: c[2], Y2: c[3]}, id, true
}

// SetPointerBarriers replaces the session's barriers with those of
// barriers on an outside edge of the current zone set, and replies the
// others as failed; a stale zone set is refused.
func (o inputCaptureObject) SetPointerBarriers(_, session dbus.ObjectPath, appID string, _ Vardict, barriers []Vardict, zoneSetID uint32) (uint32, Vardict, *dbus.Error) {
	_, link, err := o.ic.started(session, appID)
	if err != nil {
		warnf("inputcapture: SetPointerBarriers: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	set, err := o.ic.refreshZones()
	if err != nil {
		warnf("inputcapture: SetPointerBarriers: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	if zoneSetID != set.id {
		warnf("inputcapture: SetPointerBarriers: zone set %d is stale, the zones are set %d", zoneSetID, set.id)
		return ResponseOther, Vardict{}, nil
	}
	failed := []uint32{}
	var valid []lyswm.Barrier
	for _, d := range barriers {
		b, id, ok := parseBarrier(d)
		if !ok || !onZoneEdge(b, set.zones) || slices.ContainsFunc(valid, func(v lyswm.Barrier) bool { return v.ID == id }) {
			failed = append(failed, id)
			continue
		}
		valid = append(valid, b)
	}
	if err := link.SetBarriers(valid); err != nil {
		warnf("inputcapture: SetPointerBarriers: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{"failed_barriers": dbus.MakeVariant(failed)}, nil
}

// Enable arms the session's barriers; the app connects to EIS first.
func (o inputCaptureObject) Enable(session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	cs, link, err := o.ic.started(session, appID)
	if err == nil {
		o.ic.mu.Lock()
		if cs.recv == nil {
			err = fmt.Errorf("input-capture session %s enabled before ConnectToEIS", session)
		}
		o.ic.mu.Unlock()
	}
	if err == nil {
		err = link.Enable()
	}
	if err != nil {
		warnf("inputcapture: Enable: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

// Disable disarms the session's barriers and ends its capture without
// a signal.
func (o inputCaptureObject) Disable(session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	cs, link, err := o.ic.started(session, appID)
	if err == nil {
		err = errors.Join(link.Disable(), o.ic.stopEmulating(cs))
	}
	if err != nil {
		warnf("inputcapture: Disable: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

func cursorPosition(options Vardict) (*wmipc.CapturePoint, bool) {
	v, ok := options["cursor_position"]
	if !ok {
		return nil, true
	}
	pos, ok := v.Value().([]any)
	if !ok || len(pos) != 2 {
		return nil, false
	}
	x, okX := pos[0].(float64)
	y, okY := pos[1].(float64)
	return &wmipc.CapturePoint{X: x, Y: y}, okX && okY
}

// Release ends the session's capture, warping the pointer to
// cursor_position; a release of an activation that already ended does
// nothing.
func (o inputCaptureObject) Release(session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	cs, link, err := o.ic.started(session, appID)
	if err != nil {
		warnf("inputcapture: Release: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	at, ok := cursorPosition(options)
	if !ok {
		warnf("inputcapture: Release: cursor_position %v is not (dd)", options["cursor_position"])
		return ResponseOther, Vardict{}, nil
	}
	id, named := optU32(options, "activation_id")
	o.ic.mu.Lock()
	current := cs.active && (!named || id == cs.activation)
	o.ic.mu.Unlock()
	if !current {
		return ResponseSuccess, Vardict{}, nil
	}
	if err := link.Release(at); err != nil {
		warnf("inputcapture: Release: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

// ConnectToEIS hands the app the receiving end of the session's EIS
// connection, which carries the captured input; once per session.
func (o inputCaptureObject) ConnectToEIS(session dbus.ObjectPath, appID string, _ Vardict) (*os.File, *dbus.Error) {
	ic := o.ic
	cs, _, err := ic.started(session, appID)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	select {
	case <-cs.keymapReady:
	case <-time.After(captureKeymapBound):
		return nil, dbusx.Failed(fmt.Sprintf("lys-wm sent no keymap within %v", captureKeymapBound))
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if cs.recv != nil {
		return nil, dbusx.Failed(fmt.Sprintf("input-capture session %s is connected to EIS already", session))
	}
	recv, client, err := eis.NewReceiver(shellName, cs.keymap)
	if err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	cs.recv = recv
	return client, nil
}

func offeredInputCapture(conn *dbus.Conn, s *sessions, loop *waylandLoop) *inputCapture {
	offered, err := lyswm.OffersCapture()
	if err != nil {
		warnf("inputcapture: ask lys-wm whether it captures input: %v", err)
	}
	if !offered {
		return nil
	}
	return newInputCapture(conn, s, lyswm.OpenCapture, loop.syncedOutputs, watchLysWMOutputs)
}
