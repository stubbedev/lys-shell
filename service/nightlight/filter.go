package nightlight

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-lib/third_party/wayland/wlclient"
	"github.com/stubbedev/lys-lib/wlr"
	"golang.org/x/sys/unix"
)

// The protocols the filter drives, the colour transform matrix first:
// it composes ahead of the gamma LUT, so it leaves the ramps to whatever
// else sets them.
const (
	ctmIface    = "hyprland_ctm_control_manager_v1"
	gammaIface  = "zwlr_gamma_control_manager_v1"
	outputIface = "wl_output"
)

// The newest versions of each global the bindings know.
const (
	maxCTMVersion    = 2
	maxGammaVersion  = 1
	maxOutputVersion = 4
)

// ErrUnsupported is a compositor that offers neither colour protocol.
var ErrUnsupported = errors.New("nightlight: the compositor offers neither " + ctmIface + " nor " + gammaIface)

// RefusedError is the compositor turning the filter away because another
// client already controls the display colours.
type RefusedError struct {
	Protocol string
	Output   string
}

func (e *RefusedError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("nightlight: another program controls the display colours (%s refused)", e.Protocol)
	}
	return fmt.Sprintf("nightlight: another program controls the display colours (%s refused output %s)", e.Protocol, e.Output)
}

// Filter applies a Setting to every output over a private Wayland
// connection, following outputs as they come and go. The compositor
// restores the identity when the filter clears or the connection ends,
// so a crashed shell never leaves the display tinted.
//
// One goroutine reads the connection and alone closes it; requests go
// out from any goroutine under mu, which the event handlers take too.
type Filter struct {
	ctx     *wl.Context
	display *wl.Display
	reg     *wl.Registry
	changed func()
	ended   chan struct{}

	mu      sync.Mutex
	outputs map[uint32]*output
	// ctmName and gammaName are the protocol globals' registry names,
	// zero when absent; the managers bind only while the filter is on,
	// so an idle shell never blocks another night light.
	ctmName, ctmVersion uint32
	gammaName           uint32
	ctm                 *wlr.CtmControlManagerV1
	gamma               *wlr.ZwlrGammaControlManagerV1
	setting             *Setting
	refused             error
	closing             bool
	err                 error
	// finished is the final sync's reply, read and written on the
	// read loop alone.
	finished bool
}

// output is one wl_output and, under gamma control, its control and
// ramp size.
type output struct {
	name    string
	wl      *wl.Output
	version uint32
	control *wlr.ZwlrGammaControlV1
	size    int
}

// Open connects to the compositor named by WAYLAND_DISPLAY. changed runs
// on the filter's goroutine whenever Refused may have changed.
func Open(changed func()) (*Filter, error) {
	display, err := wl.Connect("")
	if err != nil {
		return nil, fmt.Errorf("nightlight: connect: %w", err)
	}
	f := &Filter{
		ctx: display.Context(), display: display, changed: changed,
		ended: make(chan struct{}), outputs: map[uint32]*output{},
	}
	display.AddDeleteIdHandler(f)
	if f.reg, err = display.GetRegistry(); err != nil {
		_ = f.ctx.Close()
		return nil, fmt.Errorf("nightlight: registry: %w", err)
	}
	wlclient.RegistryAddListener(f.reg, f)
	// The first roundtrip lists the globals, the second brings the
	// outputs' names.
	for range 2 {
		if err := roundtrip(display); err != nil {
			_ = f.ctx.Close()
			return nil, fmt.Errorf("nightlight: initial roundtrip: %w", err)
		}
	}
	if f.ctmName == 0 && f.gammaName == 0 {
		_ = f.ctx.Close()
		return nil, ErrUnsupported
	}
	go f.dispatch()
	return f, nil
}

// Protocol names the protocol the filter drives.
func (f *Filter) Protocol() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctmName != 0 {
		return ctmIface
	}
	return gammaIface
}

// Apply tints every output with s, binding the protocol on first use.
func (f *Filter) Apply(s Setting) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing {
		return errors.New("nightlight: the filter is closed")
	}
	f.setting = &s
	if f.ctmName != 0 {
		return f.applyCTMLocked()
	}
	return f.applyGammaLocked()
}

// Clear lets go of the protocol, which returns every output to the
// identity, and forgets a refusal.
func (f *Filter) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clearLocked()
}

// Refused is why the compositor turned the filter away, nil while it
// applies or is off.
func (f *Filter) Refused() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused
}

// Ended closes once the connection is gone; Err then says why.
func (f *Filter) Ended() <-chan struct{} { return f.ended }

// Err is why the connection ended, nil before it has or after Close.
func (f *Filter) Err() error {
	select {
	case <-f.ended:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.err
	default:
		return nil
	}
}

// Close clears the filter and waits for the connection to close: a
// final sync's reply ends the read loop, which closes it. A request
// that cannot be sent means the connection is already broken, and the
// read loop ends on that instead.
func (f *Filter) Close() {
	f.mu.Lock()
	_ = f.clearLocked()
	f.closing = true
	cb := wl.NewCallback(f.ctx)
	cb.AddDoneHandler(f)
	_ = f.ctx.SendRequest(f.display, 0, cb)
	f.mu.Unlock()
	<-f.ended
}

func (f *Filter) clearLocked() error {
	f.setting, f.refused = nil, nil
	var err error
	if f.ctm != nil {
		err = f.ctm.Destroy()
		f.ctm = nil
	}
	for _, o := range f.outputs {
		err = errors.Join(err, o.dropControl())
	}
	if f.gamma != nil {
		err = errors.Join(err, f.gamma.Destroy())
		f.gamma = nil
	}
	if err != nil {
		return fmt.Errorf("nightlight: clear: %w", err)
	}
	return nil
}

func (f *Filter) applyCTMLocked() error {
	if f.ctm == nil {
		m := wlr.NewCtmControlManagerV1(f.ctx)
		m.AddBlockedHandler(f)
		if err := f.reg.Bind(f.ctmName, ctmIface, f.ctmVersion, m); err != nil {
			return fmt.Errorf("nightlight: bind %s: %w", ctmIface, err)
		}
		f.ctm = m
	}
	return f.commitCTMLocked()
}

// commitCTMLocked sets the matrix on every output and commits; an
// output left out would reset to the identity.
func (f *Filter) commitCTMLocked() error {
	m := f.setting.Matrix()
	for _, o := range f.outputs {
		if err := f.ctm.SetCtmForOutput(o.wl,
			float32(m[0]), float32(m[1]), float32(m[2]),
			float32(m[3]), float32(m[4]), float32(m[5]),
			float32(m[6]), float32(m[7]), float32(m[8])); err != nil {
			return fmt.Errorf("nightlight: set the matrix of output %s: %w", o.name, err)
		}
	}
	if err := f.ctm.Commit(); err != nil {
		return fmt.Errorf("nightlight: commit the matrices: %w", err)
	}
	return nil
}

func (f *Filter) applyGammaLocked() error {
	if f.gamma == nil {
		m := wlr.NewZwlrGammaControlManagerV1(f.ctx)
		if err := f.reg.Bind(f.gammaName, gammaIface, maxGammaVersion, m); err != nil {
			return fmt.Errorf("nightlight: bind %s: %w", gammaIface, err)
		}
		f.gamma = m
	}
	for _, o := range f.outputs {
		if err := f.gammaOutputLocked(o); err != nil {
			return err
		}
	}
	return nil
}

// gammaOutputLocked asks for o's control, or, once its ramp size is
// known, sends the ramps.
func (f *Filter) gammaOutputLocked(o *output) error {
	if o.control == nil {
		c := wlr.NewZwlrGammaControlV1(f.ctx)
		h := &gammaHandler{f: f, o: o}
		c.AddGammaSizeHandler(h)
		c.AddFailedHandler(h)
		o.control, o.size = c, 0
		if err := f.ctx.SendRequest(f.gamma, 0, c, o.wl); err != nil {
			return fmt.Errorf("nightlight: gamma control for output %s: %w", o.name, err)
		}
		return nil
	}
	if o.size == 0 {
		return nil
	}
	return o.sendRamps(f.setting.Ramps(o.size))
}

// sendRamps hands the compositor the ramps in a sealed memfd.
func (o *output) sendRamps(ramps []uint16) error {
	fd, err := unix.MemfdCreate("lys-nightlight", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return fmt.Errorf("nightlight: ramp memfd for output %s: %w", o.name, err)
	}
	defer func() { _ = unix.Close(fd) }()
	buf := make([]byte, 2*len(ramps))
	for i, v := range ramps {
		binary.NativeEndian.PutUint16(buf[2*i:], v)
	}
	if _, err := unix.Write(fd, buf); err != nil {
		return fmt.Errorf("nightlight: write the ramps of output %s: %w", o.name, err)
	}
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return fmt.Errorf("nightlight: rewind the ramps of output %s: %w", o.name, err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE|unix.F_SEAL_SEAL); err != nil {
		return fmt.Errorf("nightlight: seal the ramps of output %s: %w", o.name, err)
	}
	if err := o.control.SetGamma(uintptr(fd)); err != nil {
		return fmt.Errorf("nightlight: set the ramps of output %s: %w", o.name, err)
	}
	return nil
}

func (o *output) dropControl() error {
	if o.control == nil {
		return nil
	}
	err := o.control.Destroy()
	o.control, o.size = nil, 0
	return err
}

// refuseLocked records the first refusal.
func (f *Filter) refuseLocked(err error) {
	if f.refused == nil {
		f.refused = err
	}
}

func (f *Filter) notify() {
	if f.changed != nil {
		f.changed()
	}
}

func (f *Filter) dispatch() {
	defer close(f.ended)
	defer func() { _ = f.ctx.Close() }()
	for !f.finished {
		err := f.ctx.Run()
		if errors.Is(err, wl.ErrContextRunProxyNil) {
			continue
		}
		if err != nil {
			f.mu.Lock()
			if !f.closing {
				f.err = fmt.Errorf("nightlight: the compositor connection ended: %w", err)
			}
			f.mu.Unlock()
			f.notify()
			return
		}
	}
}

// HandleCallbackDone is the final sync of Close: the read loop stops.
func (f *Filter) HandleCallbackDone(wl.CallbackDoneEvent) { f.finished = true }

// HandleRegistryGlobal records the colour protocols and binds outputs.
func (f *Filter) HandleRegistryGlobal(ev wl.RegistryGlobalEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch ev.Interface {
	case ctmIface:
		f.ctmName, f.ctmVersion = ev.Name, min(ev.Version, maxCTMVersion)
	case gammaIface:
		f.gammaName = ev.Name
	case outputIface:
		f.addOutputLocked(ev)
	}
}

func (f *Filter) addOutputLocked(ev wl.RegistryGlobalEvent) {
	wo := wl.NewOutput(f.ctx)
	version := min(ev.Version, maxOutputVersion)
	if err := f.reg.Bind(ev.Name, ev.Interface, version, wo); err != nil {
		return
	}
	o := &output{name: strconv.FormatUint(uint64(ev.Name), 10), wl: wo, version: version}
	if version >= 4 {
		wo.AddNameHandler(outputName{o})
	}
	f.outputs[ev.Name] = o
	if f.setting == nil || f.refused != nil {
		return
	}
	var err error
	switch {
	case f.ctm != nil:
		err = f.commitCTMLocked()
	case f.gamma != nil:
		err = f.gammaOutputLocked(o)
	}
	if err != nil {
		f.refuseLocked(err)
	}
}

// HandleRegistryGlobalRemove forgets an unplugged output.
func (f *Filter) HandleRegistryGlobalRemove(ev wl.RegistryGlobalRemoveEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.outputs[ev.Name]
	if !ok {
		return
	}
	delete(f.outputs, ev.Name)
	_ = o.dropControl()
	if o.version >= 3 {
		_ = o.wl.Release()
	}
}

// HandleDisplayDeleteId frees a destroyed handle's id.
func (f *Filter) HandleDisplayDeleteId(ev wl.DisplayDeleteIdEvent) {
	f.ctx.Unregister(wl.ProxyId(ev.Id))
}

// HandleCtmControlManagerV1Blocked is another CTM manager owning the
// outputs: the compositor ignores this one's matrices.
func (f *Filter) HandleCtmControlManagerV1Blocked(wlr.CtmControlManagerV1BlockedEvent) {
	f.mu.Lock()
	f.refuseLocked(&RefusedError{Protocol: ctmIface})
	f.mu.Unlock()
	f.notify()
}

type outputName struct{ o *output }

func (n outputName) HandleOutputName(ev wl.OutputNameEvent) { n.o.name = ev.Name }

// gammaHandler is one output's gamma control.
type gammaHandler struct {
	f *Filter
	o *output
}

// HandleZwlrGammaControlV1GammaSize sends the ramps once their size is
// known.
func (h *gammaHandler) HandleZwlrGammaControlV1GammaSize(ev wlr.ZwlrGammaControlV1GammaSizeEvent) {
	h.f.mu.Lock()
	var err error
	if h.o.control != nil && h.f.setting != nil {
		h.o.size = int(ev.Size)
		err = h.o.sendRamps(h.f.setting.Ramps(h.o.size))
	}
	if err != nil {
		h.f.refuseLocked(err)
	}
	h.f.mu.Unlock()
	if err != nil {
		h.f.notify()
	}
}

// HandleZwlrGammaControlV1Failed is the compositor refusing the
// output's ramps: another client holds them, or the output has none.
func (h *gammaHandler) HandleZwlrGammaControlV1Failed(wlr.ZwlrGammaControlV1FailedEvent) {
	h.f.mu.Lock()
	_ = h.o.dropControl()
	h.f.refuseLocked(&RefusedError{Protocol: gammaIface, Output: h.o.name})
	h.f.mu.Unlock()
	h.f.notify()
}

func roundtrip(display *wl.Display) error {
	cb, err := display.Sync()
	if err != nil {
		return err
	}
	return display.Context().RunTill(cb)
}
