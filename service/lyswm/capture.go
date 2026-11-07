package lyswm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/internal/ipcsock"
)

const (
	captureBacklog   = 4096
	captureAckBound  = 5 * time.Second
	captureEventName = "capture"
)

// Barrier is a pointer barrier on an edge of the layout, in logical
// pixels: horizontal when Y1 == Y2, vertical when X1 == X2.
type Barrier struct {
	ID uint32 `json:"id"`
	X1 int32  `json:"x1"`
	Y1 int32  `json:"y1"`
	X2 int32  `json:"x2"`
	Y2 int32  `json:"y2"`
}

// CapturePoint is a point of the layout in logical pixels.
type CapturePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// CaptureEvent is one line of the capture stream; Event names it and
// the fields it carries are set.
type CaptureEvent struct {
	Event      string        `json:"event"`
	Keymap     string        `json:"keymap"`
	Activation uint32        `json:"activation"`
	At         *CapturePoint `json:"at"`
	Barrier    uint32        `json:"barrier"`
	DX         float64       `json:"dx"`
	DY         float64       `json:"dy"`
	X          bool          `json:"x"`
	Y          bool          `json:"y"`
	Button     uint32        `json:"button"`
	Key        uint32        `json:"key"`
	Pressed    bool          `json:"pressed"`
	Depressed  uint32        `json:"depressed"`
	Latched    uint32        `json:"latched"`
	Locked     uint32        `json:"locked"`
	Group      uint32        `json:"group"`
	Error      string        `json:"error"`
}

type captureCommand struct {
	Command  string        `json:"command"`
	Barriers *[]Barrier    `json:"barriers,omitempty"`
	At       *CapturePoint `json:"at,omitempty"`
}

// Capture is an input capture on lys-wm: commands go out one at a time,
// each waiting for lys-wm to apply it, and every other event arrives on
// Events, which closes when the capture ends.
type Capture struct {
	conn    net.Conn
	events  chan CaptureEvent
	applied chan struct{}
	ended   chan struct{}
	sending sync.Mutex
	mu      sync.Mutex
	once    sync.Once
	failure error
}

// OpenCapture starts an input capture.
func OpenCapture() (*Capture, error) {
	req := request{Request: captureEventName}
	conn, reader, err := open(req)
	if err != nil {
		return nil, err
	}
	if _, err := readResponse(reader, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ipcsock.Disarm(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lys-wm: capture: %w", err)
	}
	c := &Capture{conn: conn, events: make(chan CaptureEvent, captureBacklog), applied: make(chan struct{}, 1), ended: make(chan struct{})}
	go c.pump(reader)
	return c, nil
}

// OffersCapture reports whether the advertised lys-wm serves input
// captures: an older one rejects the request, and without lys-wm there
// is nothing to ask.
func OffersCapture() (bool, error) {
	if !IsRunning() {
		return false, nil
	}
	c, err := OpenCapture()
	if _, rejected := errors.AsType[*RejectedError](err); rejected {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c.Close()
	return true, nil
}

func (c *Capture) pump(reader *bufio.Reader) {
	defer close(c.events)
	defer close(c.ended)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			c.end(fmt.Errorf("lys-wm: the capture stream ended: %w", err))
			return
		}
		var e CaptureEvent
		if err := json.Unmarshal(line, &e); err != nil || e.Event == "" {
			c.end(fmt.Errorf("lys-wm: a capture line it cannot read (%w): %.200q", err, line))
			return
		}
		switch e.Event {
		case "applied":
			c.applied <- struct{}{}
		case "error":
			c.end(fmt.Errorf("lys-wm refused a capture command: %s", e.Error))
			return
		default:
			c.events <- e
		}
	}
}

func (c *Capture) end(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil {
		c.failure = err
	}
}

// Err is why the capture ended, once Events closed.
func (c *Capture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure
}

// Events are the capture's events but the acknowledgements.
func (c *Capture) Events() <-chan CaptureEvent { return c.events }

func (c *Capture) command(cmd captureCommand) error {
	c.sending.Lock()
	defer c.sending.Unlock()
	line, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("lys-wm: encode the capture command %s: %w", cmd.Command, err)
	}
	if err := ipcsock.Standard.ArmWrite(c.conn); err != nil {
		return err
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("lys-wm: send the capture command %s: %w", cmd.Command, err)
	}
	select {
	case <-c.applied:
		return nil
	case <-c.ended:
		return fmt.Errorf("lys-wm: the capture ended before %s applied", cmd.Command)
	case <-time.After(captureAckBound):
		return fmt.Errorf("lys-wm: %s did not apply within %v", cmd.Command, captureAckBound)
	}
}

// SetBarriers replaces the capture's barriers and disables it.
func (c *Capture) SetBarriers(barriers []Barrier) error {
	if barriers == nil {
		barriers = []Barrier{}
	}
	return c.command(captureCommand{Command: "barriers", Barriers: &barriers})
}

// Enable arms the barriers.
func (c *Capture) Enable() error { return c.command(captureCommand{Command: "enable"}) }

// Disable disarms the barriers and ends an active capture without an
// event.
func (c *Capture) Disable() error { return c.command(captureCommand{Command: "disable"}) }

// Release ends the active capture, warping the pointer to at first
// when it is set.
func (c *Capture) Release(at *CapturePoint) error {
	return c.command(captureCommand{Command: "release", At: at})
}

// Close ends the capture; lys-wm releases the input it held.
func (c *Capture) Close() {
	c.once.Do(func() { _ = c.conn.Close() })
}
