package idle

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
	"github.com/stubbedev/lys-lib/third_party/wayland/wlclient"
	"github.com/stubbedev/lys-lib/wlr"
)

const (
	notifierIface = "ext_idle_notifier_v1"
	powerIface    = "zwlr_output_power_manager_v1"
	seatIface     = "wl_seat"
	outputIface   = "wl_output"
)

// ErrNoIdleNotify is a compositor without ext-idle-notify-v1 or a seat:
// the session can never be seen idle.
var ErrNoIdleNotify = errors.New("idle: the compositor offers no " + notifierIface + " or no seat")

// ErrNoOutputPower is a compositor without wlr-output-power-management:
// the outputs cannot be turned off.
var ErrNoOutputPower = errors.New("idle: the compositor offers no " + powerIface)

// Compositor is the idle service's own Wayland connection: it watches the
// seat through ext-idle-notify-v1, which honours idle inhibitors, and turns
// outputs off and on through wlr-output-power-management-v1. One goroutine
// reads the connection; requests go out from any goroutine under mu, and no
// handler holds mu while it calls back.
type Compositor struct {
	ctx     *wl.Context
	display *wl.Display
	reg     *wl.Registry
	ended   chan struct{}

	mu       sync.Mutex
	notifier *wlr.ExtIdleNotifierV1
	seat     *wl.Seat
	power    *wlr.ZwlrOutputPowerManagerV1
	outputs  map[uint32]*powerOutput
	off      bool
	closing  bool
	finished bool
	err      error
}

type powerOutput struct {
	wl    *wl.Output
	power *wlr.ZwlrOutputPowerV1
}

// Connect connects to the compositor named by WAYLAND_DISPLAY and binds
// what it offers; ErrNoIdleNotify when it cannot watch idle.
func Connect() (*Compositor, error) {
	display, err := wl.Connect("")
	if err != nil {
		return nil, fmt.Errorf("idle: connect: %w", err)
	}
	c := &Compositor{ctx: display.Context(), display: display, ended: make(chan struct{}), outputs: map[uint32]*powerOutput{}}
	if c.reg, err = display.GetRegistry(); err != nil {
		_ = c.ctx.Close()
		return nil, fmt.Errorf("idle: registry: %w", err)
	}
	wlclient.RegistryAddListener(c.reg, c)
	if err := roundtrip(display); err != nil {
		_ = c.ctx.Close()
		return nil, fmt.Errorf("idle: initial roundtrip: %w", err)
	}
	if c.notifier == nil || c.seat == nil {
		_ = c.ctx.Close()
		return nil, ErrNoIdleNotify
	}
	go c.dispatch()
	return c, nil
}

// Watch implements Watcher with one ext_idle_notification_v1 that honours
// idle inhibitors.
func (c *Compositor) Watch(after time.Duration, idle, resume func()) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return nil, errors.New("idle: the compositor connection is closed")
	}
	n, err := c.notifier.GetIdleNotification(intconv.Clamp[uint32](after.Milliseconds()), c.seat)
	if err != nil {
		return nil, fmt.Errorf("idle: get_idle_notification: %w", err)
	}
	h := &notification{idle: idle, resume: resume}
	n.AddIdledHandler(h)
	n.AddResumedHandler(h)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		_ = n.Destroy()
	}, nil
}

type notification struct{ idle, resume func() }

func (n *notification) HandleExtIdleNotificationV1Idled(wlr.ExtIdleNotificationV1IdledEvent) {
	n.idle()
}

func (n *notification) HandleExtIdleNotificationV1Resumed(wlr.ExtIdleNotificationV1ResumedEvent) {
	n.resume()
}

// PowerOff turns every output off, and every output that appears while
// off; the returned restore turns them back on.
func (c *Compositor) PowerOff() (func() error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.power == nil {
		return nil, ErrNoOutputPower
	}
	c.off = true
	if err := c.setModeLocked(wlr.ZwlrOutputPowerV1ModeOff); err != nil {
		return nil, err
	}
	return c.powerOn, nil
}

func (c *Compositor) powerOn() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off = false
	return c.setModeLocked(wlr.ZwlrOutputPowerV1ModeOn)
}

func (c *Compositor) setModeLocked(mode uint32) error {
	var err error
	for name, o := range c.outputs {
		if o.power == nil {
			continue
		}
		if e := o.power.SetMode(mode); e != nil {
			err = errors.Join(err, fmt.Errorf("idle: set output %d power mode %d: %w", name, mode, e))
		}
	}
	return err
}

// Ended closes once the connection is gone; Err then says why.
func (c *Compositor) Ended() <-chan struct{} { return c.ended }

// Err is why the connection ended, nil before it has or after Close.
func (c *Compositor) Err() error {
	select {
	case <-c.ended:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.err
	default:
		return nil
	}
}

// Close powers the outputs back on and closes the connection once a final
// sync comes back.
func (c *Compositor) Close() {
	c.mu.Lock()
	if c.off {
		_ = c.setModeLocked(wlr.ZwlrOutputPowerV1ModeOn)
		c.off = false
	}
	c.closing = true
	cb := wl.NewCallback(c.ctx)
	cb.AddDoneHandler(c)
	_ = c.ctx.SendRequest(c.display, 0, cb)
	c.mu.Unlock()
	<-c.ended
}

// HandleCallbackDone is Close's final sync: the read loop stops.
func (c *Compositor) HandleCallbackDone(wl.CallbackDoneEvent) { c.finished = true }

func (c *Compositor) dispatch() {
	defer close(c.ended)
	defer func() { _ = c.ctx.Close() }()
	for !c.finished {
		err := c.ctx.Run()
		if errors.Is(err, wl.ErrContextRunProxyNil) {
			continue
		}
		if err != nil {
			c.mu.Lock()
			if !c.closing {
				c.err = fmt.Errorf("idle: the compositor connection ended: %w", err)
			}
			c.mu.Unlock()
			return
		}
	}
}

// HandleRegistryGlobal binds the notifier, the first seat, the power
// manager and every output.
func (c *Compositor) HandleRegistryGlobal(ev wl.RegistryGlobalEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch ev.Interface {
	case notifierIface:
		n := wlr.NewExtIdleNotifierV1(c.ctx)
		if c.reg.Bind(ev.Name, ev.Interface, 1, n) == nil {
			c.notifier = n
		}
	case seatIface:
		if c.seat != nil {
			return
		}
		s := wl.NewSeat(c.ctx)
		if c.reg.Bind(ev.Name, ev.Interface, 1, s) == nil {
			c.seat = s
		}
	case powerIface:
		m := wlr.NewZwlrOutputPowerManagerV1(c.ctx)
		if c.reg.Bind(ev.Name, ev.Interface, 1, m) != nil {
			return
		}
		c.power = m
		for _, o := range c.outputs {
			c.controlLocked(o)
		}
	case outputIface:
		wo := wl.NewOutput(c.ctx)
		if c.reg.Bind(ev.Name, ev.Interface, 1, wo) != nil {
			return
		}
		o := &powerOutput{wl: wo}
		c.outputs[ev.Name] = o
		c.controlLocked(o)
		if c.off && o.power != nil {
			_ = o.power.SetMode(wlr.ZwlrOutputPowerV1ModeOff)
		}
	}
}

func (c *Compositor) controlLocked(o *powerOutput) {
	if c.power == nil || o.power != nil {
		return
	}
	p, err := c.power.GetOutputPower(o.wl)
	if err != nil {
		return
	}
	p.AddFailedHandler(failedPower{c: c, o: o})
	o.power = p
}

type failedPower struct {
	c *Compositor
	o *powerOutput
}

// HandleZwlrOutputPowerV1Failed is an output whose power the compositor
// will not control (another client holds it, or the output went away):
// the control is dropped.
func (f failedPower) HandleZwlrOutputPowerV1Failed(wlr.ZwlrOutputPowerV1FailedEvent) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	if f.o.power != nil {
		_ = f.o.power.Destroy()
		f.o.power = nil
	}
}

// HandleRegistryGlobalRemove forgets an unplugged output.
func (c *Compositor) HandleRegistryGlobalRemove(ev wl.RegistryGlobalRemoveEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.outputs[ev.Name]
	if !ok {
		return
	}
	delete(c.outputs, ev.Name)
	if o.power != nil {
		_ = o.power.Destroy()
	}
}

func roundtrip(display *wl.Display) error {
	cb, err := display.Sync()
	if err != nil {
		return err
	}
	return display.Context().RunTill(cb)
}
