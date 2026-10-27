// Package dm is the login screen's display manager: `lys-login daemon`,
// run as root by its system service on a VT, shows the greeter there and
// starts the session a user logs in to, with PAM as its only required
// dependency. It replaces greetd, cage and the polkit apply path.
//
// The daemon itself never authenticates or opens a session: each greeter
// and each login runs in a worker, `lys-login worker`, a root process of
// its own that holds the PAM transaction from start to end and leads the
// logind session pam_systemd registers. The greeter runs as the
// unprivileged greeter user inside lys-wm, configured as a kiosk, and
// speaks the greetd protocol to the daemon over a socket only it can
// reach, so the greeter UI is the same one that runs under greetd.
package dm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/stubbedev/lys-shell/service/auth"
)

// A worker and the daemon exchange JSON lines over a socket pair: the
// daemon sends requests, the worker answers with events.

// request is the daemon's side.
type request struct {
	Op string `json:"op"`
	// User is the account to authenticate (auth).
	User string `json:"user,omitempty"`
	// Answer is the reply to the worker's last prompt (answer); nil
	// answers an info or error message, or cancels an input prompt.
	Answer *string `json:"answer,omitempty"`
	// Cmd and Env are the session to start (start).
	Cmd []string `json:"cmd,omitempty"`
	Env []string `json:"env,omitempty"`
}

// The request ops.
const (
	opAuth   = "auth"
	opAnswer = "answer"
	opStart  = "start"
	opCancel = "cancel"
)

// event is the worker's side.
type event struct {
	Kind string `json:"kind"`
	// Prompt is a PAM message to show (prompt).
	Prompt *auth.Prompt `json:"prompt,omitempty"`
	// Text is why the login failed (failed), or how the session ended
	// (ended).
	Text string `json:"text,omitempty"`
}

// The event kinds.
const (
	evPrompt        = "prompt"
	evAuthenticated = "authenticated"
	evFailed        = "failed"
	evStarted       = "started"
	evEnded         = "ended"
)

// channel is one side of a worker's socket pair.
type channel struct {
	r  *bufio.Scanner
	mu sync.Mutex
	w  io.Writer
}

// maxLine bounds one message: the largest is a session's command and
// environment.
const maxLine = 1 << 20

func newChannel(rw io.ReadWriter) *channel {
	sc := bufio.NewScanner(rw)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	return &channel{r: sc, w: rw}
}

func (c *channel) send(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(line, '\n'))
	return err
}

// errClosed is the other side going away.
var errClosed = errors.New("dm: the other side closed the channel")

func (c *channel) recv(v any) error {
	if !c.r.Scan() {
		if err := c.r.Err(); err != nil {
			return fmt.Errorf("dm: read: %w", err)
		}
		return errClosed
	}
	if err := json.Unmarshal(c.r.Bytes(), v); err != nil {
		return fmt.Errorf("dm: decode %q: %w", c.r.Text(), err)
	}
	return nil
}
