package dm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/shlex"
	"github.com/stubbedev/lys-shell/service/auth"
)

// Options configure the daemon.
type Options struct {
	WorkerOptions
	// Self is the lys-login program the workers and the greeter run.
	Self string
	// GreeterUser is the unprivileged account the greeter runs as.
	GreeterUser string
	// Compositor is the greeter's compositor command, lys-wm; the daemon
	// adds its kiosk config.
	Compositor []string
	// GreeterArgs follow `lys-login greet`: the config, the state file,
	// the session directories.
	GreeterArgs []string
	// RuntimeDir holds the greeter's socket and the kiosk config.
	RuntimeDir string
}

// retryPause spaces greeters that fail to come up.
const retryPause = 2 * time.Second

// Run shows the greeter on the VT and starts each session a user logs
// in to, until ctx ends.
func Run(ctx context.Context, o Options) error {
	if err := os.MkdirAll(o.RuntimeDir, 0o755); err != nil { //nolint:gosec // the greeter user reaches its socket through it
		return fmt.Errorf("dm: create %s: %w", o.RuntimeDir, err)
	}
	for ctx.Err() == nil {
		if err := activateVT(o.VT); err != nil {
			log.Printf("dm: %v", err)
		}
		login, err := o.greet(ctx)
		if err != nil {
			log.Printf("dm: greeter: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(retryPause):
			}
			continue
		}
		if err := login.run(); err != nil {
			log.Printf("dm: session: %v", err)
		}
	}
	return nil
}

// The console ioctls of linux/vt.h, which x/sys/unix does not carry.
const (
	vtActivate   = 0x5606
	vtWaitActive = 0x5607
)

// activateVT switches the console to vt and waits until it shows.
func activateVT(vt int) error {
	tty, err := os.OpenFile(fmt.Sprintf("/dev/tty%d", vt), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("open VT %d: %w", vt, err)
	}
	defer func() { _ = tty.Close() }()
	fd := int(tty.Fd())
	if err := unix.IoctlSetInt(fd, vtActivate, vt); err != nil {
		return fmt.Errorf("activate VT %d: %w", vt, err)
	}
	if err := unix.IoctlSetInt(fd, vtWaitActive, vt); err != nil {
		return fmt.Errorf("wait for VT %d: %w", vt, err)
	}
	return nil
}

// kioskConfig is lys-wm's config for the greeter: no binds, so the
// greeter's keys reach only it, and the greeter as the one program.
func kioskConfig(greet []string) string {
	quoted := make([]string, len(greet))
	for i, arg := range greet {
		quoted[i] = shlex.MustQuote(arg)
	}
	return "binds: []\nautostart:\n  - " + yamlString(strings.Join(quoted, " ")) + "\n"
}

// yamlString is s as a double-quoted YAML scalar.
func yamlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// pendingLogin is an authenticated login waiting for its session.
type pendingLogin struct {
	w        *worker
	cmd, env []string
}

// greet runs the greeter until a user is authenticated and asks for a
// session, then stops it.
func (o Options) greet(ctx context.Context) (*pendingLogin, error) {
	sock := filepath.Join(o.RuntimeDir, "greeter.sock")
	ln, err := o.listen(sock)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ln.Close() }()
	cfg := filepath.Join(o.RuntimeDir, "kiosk.yaml")
	greet := append([]string{o.Self, "greet"}, o.GreeterArgs...)
	if err := os.WriteFile(cfg, []byte(kioskConfig(greet)), 0o644); err != nil { //nolint:gosec // the greeter user reads it
		return nil, fmt.Errorf("write %s: %w", cfg, err)
	}
	args := []string{
		"worker", "greeter", "--service", o.Service, "--vt", strconv.Itoa(o.VT), "--user", o.GreeterUser,
		"--env", auth.GreetdSockEnv + "=" + sock, "--",
	}
	args = append(append(args, o.Compositor...), "--config", cfg)
	gw := exec.Command(o.Self, args...) //nolint:gosec // lys-login itself
	gw.Stdout, gw.Stderr = os.Stderr, os.Stderr
	if err := gw.Start(); err != nil {
		return nil, fmt.Errorf("start the greeter worker: %w", err)
	}
	exited := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = gw.Wait()
		close(exited)
	}()
	conns, done := make(chan net.Conn), make(chan struct{})
	defer close(done)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case conns <- c:
			case <-done:
				_ = c.Close()
				return
			}
		}
	}()
	login, err := o.serveConns(ctx, conns, exited)
	_ = gw.Process.Signal(syscall.SIGTERM)
	<-exited
	if login == nil && err == nil {
		err = errors.Join(errors.New("the greeter ended"), exitErr)
	}
	return login, err
}

// serveConns serves the greeter's connections, each on its own, as the
// greeter opens one per login attempt, until one hands over a session to
// start, the greeter's worker ends (neither a login nor an error), or
// ctx does. The connections still open then are closed, which cancels
// their logins.
func (o Options) serveConns(ctx context.Context, conns <-chan net.Conn, exited <-chan struct{}) (*pendingLogin, error) {
	logins := make(chan *pendingLogin)
	open := map[net.Conn]bool{}
	ended := make(chan net.Conn)
	defer func() {
		for c := range open {
			_ = c.Close()
		}
		for remaining := len(open); remaining > 0; {
			select {
			case <-ended:
				remaining--
			case late := <-logins:
				// A second login that finished at the same moment
				// starts nothing.
				late.w.cancel()
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-exited:
			return nil, nil
		case conn := <-conns:
			open[conn] = true
			go func() {
				login, err := o.serveGreeter(conn)
				_ = conn.Close()
				switch {
				case login != nil:
					logins <- login
				case err != nil:
					log.Printf("dm: greeter connection: %v", err)
				}
				ended <- conn
			}()
		case login := <-logins:
			// The winning connection's goroutine still reports its end.
			return login, nil
		case conn := <-ended:
			delete(open, conn)
		}
	}
}

// listen binds the greeter's socket, reachable by the greeter user alone.
func (o Options) listen(path string) (*net.UnixListener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove the stale %s: %w", path, err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	a, err := lookupAccount(o.GreeterUser)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := os.Chown(path, int(a.uid), int(a.gid)); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("give %s to %s: %w", path, o.GreeterUser, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("restrict %s: %w", path, err)
	}
	return ln, nil
}

// serveGreeter answers the greeter's greetd requests, relaying each
// login to a session worker, until a login asks to start its session or
// the greeter goes away.
func (o Options) serveGreeter(conn net.Conn) (*pendingLogin, error) {
	var w *worker
	state := endedLogin
	defer func() {
		if w != nil && state != authenticatedLogin {
			w.cancel()
		}
	}()
	// step relays the worker's next event and forgets a worker that
	// ended.
	step := func() error {
		var err error
		state, err = relay(conn, w)
		if state == endedLogin {
			w = nil
		}
		return err
	}
	for {
		req, err := auth.ReadGreetdRequest(conn)
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read the greeter's request: %w", err)
		}
		switch req.Type {
		case auth.GreetdCreateSession:
			if w != nil {
				w.cancel()
			}
			if w, err = o.startSessionWorker(req.Username); err != nil {
				w, state = nil, endedLogin
				if err := auth.WriteGreetdError(conn, false, err.Error()); err != nil {
					return nil, err
				}
				continue
			}
			if err := step(); err != nil {
				return nil, err
			}
		case auth.GreetdPostResponse:
			if w == nil || state != awaitingAnswer {
				if err := auth.WriteGreetdError(conn, false, "no login is waiting for an answer"); err != nil {
					return nil, err
				}
				continue
			}
			if err := w.ch.send(request{Op: opAnswer, Answer: req.Response}); err != nil {
				return nil, err
			}
			if err := step(); err != nil {
				return nil, err
			}
		case auth.GreetdCancelSession:
			if w != nil {
				w.cancel()
			}
			w, state = nil, endedLogin
			if err := auth.WriteGreetdSuccess(conn); err != nil {
				return nil, err
			}
		case auth.GreetdStartSession:
			if state != authenticatedLogin || len(req.Cmd) == 0 {
				if err := auth.WriteGreetdError(conn, false, "no authenticated login to start, or no command"); err != nil {
					return nil, err
				}
				continue
			}
			if err := auth.WriteGreetdSuccess(conn); err != nil {
				return nil, err
			}
			return &pendingLogin{w: w, cmd: req.Cmd, env: req.Env}, nil
		}
	}
}

// loginState is where a login stands after its worker's last event.
type loginState int

const (
	// awaitingAnswer: a prompt is with the greeter.
	awaitingAnswer loginState = iota
	authenticatedLogin
	// endedLogin: the login failed and its worker is gone.
	endedLogin
)

// relay forwards the worker's next event to the greeter: a prompt
// waits for the greeter's answer, authenticated is a success, a failure
// an authentication error that ends the worker.
func relay(conn net.Conn, w *worker) (loginState, error) {
	var ev event
	if err := w.ch.recv(&ev); err != nil {
		w.cancel()
		return endedLogin, auth.WriteGreetdError(conn, false, "the login ended: "+err.Error())
	}
	switch ev.Kind {
	case evPrompt:
		if ev.Prompt == nil {
			w.cancel()
			return endedLogin, errors.New("dm: a prompt event without a prompt")
		}
		return awaitingAnswer, auth.WriteGreetdPrompt(conn, *ev.Prompt)
	case evAuthenticated:
		return authenticatedLogin, auth.WriteGreetdSuccess(conn)
	case evFailed:
		w.wait()
		return endedLogin, auth.WriteGreetdError(conn, true, ev.Text)
	}
	w.cancel()
	return endedLogin, fmt.Errorf("dm: unexpected event %q while authenticating", ev.Kind)
}

// run starts the authenticated session and waits for it to end.
func (l *pendingLogin) run() error {
	defer l.w.wait()
	if err := l.w.ch.send(request{Op: opStart, Cmd: l.cmd, Env: l.env}); err != nil {
		return err
	}
	for {
		var ev event
		if err := l.w.ch.recv(&ev); err != nil {
			if errors.Is(err, errClosed) {
				return nil
			}
			return err
		}
		switch ev.Kind {
		case evStarted:
			log.Printf("dm: session started: %s", strings.Join(l.cmd, " "))
		case evFailed:
			return fmt.Errorf("the session did not start: %s", ev.Text)
		case evEnded:
			return nil
		}
	}
}

// worker is a session worker process and its channel.
type worker struct {
	cmd *exec.Cmd
	ch  *channel
	f   *os.File
}

// startSessionWorker starts a session worker and asks it to
// authenticate user.
func (o Options) startSessionWorker(user string) (*worker, error) {
	if user == "" {
		return nil, errors.New("no user name")
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socket pair: %w", err)
	}
	mine, theirs := os.NewFile(uintptr(pair[0]), "dm"), os.NewFile(uintptr(pair[1]), "worker")
	defer func() { _ = theirs.Close() }()
	cmd := exec.Command(o.Self, "worker", "session", "--service", o.Service, "--vt", strconv.Itoa(o.VT)) //nolint:gosec // lys-login itself
	cmd.ExtraFiles = []*os.File{theirs}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		_ = mine.Close()
		return nil, fmt.Errorf("start a session worker: %w", err)
	}
	w := &worker{cmd: cmd, ch: newChannel(mine), f: mine}
	if err := w.ch.send(request{Op: opAuth, User: user}); err != nil {
		w.cancel()
		return nil, err
	}
	return w, nil
}

// cancel ends a worker that has not started a session.
func (w *worker) cancel() {
	_ = w.ch.send(request{Op: opCancel})
	w.wait()
}

// wait reaps the worker and closes the channel.
func (w *worker) wait() {
	_ = w.cmd.Wait()
	_ = w.f.Close()
}
