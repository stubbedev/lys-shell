package dm

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stubbedev/lys-shell/service/auth"
)

// Seat is the seat the display manager runs on.
const Seat = "seat0"

// sessionPath is the PATH a session starts with when PAM sets none.
const sessionPath = "/run/wrappers/bin:/run/current-system/sw/bin:/usr/local/bin:/usr/bin:/bin"

// profileScript starts a session command the way a login does: the
// system and the user's profile first, then the command in its place.
const profileScript = `[ -f /etc/profile ] && . /etc/profile; [ -f "$HOME/.profile" ] && . "$HOME/.profile"; exec "$@"`

// stopGrace is how long a stopped greeter has to exit before it is
// killed.
const stopGrace = 5 * time.Second

// WorkerOptions are what every worker runs with.
type WorkerOptions struct {
	// Service is the PAM service.
	Service string
	// VT is the virtual terminal the sessions run on.
	VT int
}

// sessionEnv is the environment pam_systemd registers a session with.
func (o WorkerOptions) sessionEnv(class string) []string {
	return []string{
		"XDG_SESSION_TYPE=wayland",
		"XDG_SESSION_CLASS=" + class,
		"XDG_SEAT=" + Seat,
		"XDG_VTNR=" + strconv.Itoa(o.VT),
	}
}

// account is who a process runs as.
type account struct {
	name, home, shell string
	uid, gid          uint32
	groups            []uint32
}

func lookupAccount(name string) (account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return account{}, fmt.Errorf("look up %s: %w", name, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return account{}, fmt.Errorf("%s's uid %q: %w", name, u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return account{}, fmt.Errorf("%s's gid %q: %w", name, u.Gid, err)
	}
	ids, err := u.GroupIds()
	if err != nil {
		return account{}, fmt.Errorf("%s's groups: %w", name, err)
	}
	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		g, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return account{}, fmt.Errorf("%s's group %q: %w", name, id, err)
		}
		groups = append(groups, uint32(g))
	}
	shell, err := loginShell(name)
	if err != nil {
		return account{}, err
	}
	return account{name: name, home: u.HomeDir, shell: shell, uid: uint32(uid), gid: uint32(gid), groups: groups}, nil
}

// passwdFile is the account database the shell is read from, as
// os/user reads the rest of the account.
const passwdFile = "/etc/passwd"

// loginShell is name's shell in the account database, /bin/sh when it
// names none.
func loginShell(name string) (string, error) {
	data, err := os.ReadFile(passwdFile)
	if err != nil {
		return "", fmt.Errorf("look up %s's shell: %w", name, err)
	}
	return shellOf(string(data), name), nil
}

// shellOf is name's shell in a passwd file's text, /bin/sh without one.
func shellOf(passwd, name string) string {
	for line := range strings.SplitSeq(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 && fields[0] == name && fields[6] != "" {
			return fields[6]
		}
	}
	return "/bin/sh"
}

// environ is a session's environment: PAM's, then the account's
// basics where PAM set none, then extra.
func (a account) environ(pamEnv, extra []string) []string {
	env := append([]string(nil), pamEnv...)
	set := map[string]bool{}
	for _, kv := range env {
		for i := range len(kv) {
			if kv[i] == '=' {
				set[kv[:i]] = true
				break
			}
		}
	}
	for _, kv := range [][2]string{{"HOME", a.home}, {"USER", a.name}, {"LOGNAME", a.name}, {"SHELL", a.shell}, {"PATH", sessionPath}} {
		if !set[kv[0]] {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	return append(env, extra...)
}

// startAs starts argv as a, in a session of its own with the VT as its
// controlling terminal and standard input; output goes to the worker's
// log.
func (o WorkerOptions) startAs(a account, argv, env []string) (*exec.Cmd, error) {
	tty, err := os.OpenFile(fmt.Sprintf("/dev/tty%d", o.VT), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open the VT: %w", err)
	}
	defer func() { _ = tty.Close() }()
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the session the user chose, run as them
	cmd.Env, cmd.Dir = env, a.home
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, Setctty: true, Ctty: 0,
		Credential: &syscall.Credential{Uid: a.uid, Gid: a.gid, Groups: a.groups},
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s as %s: %w", argv[0], a.name, err)
	}
	return cmd, nil
}

// RunGreeter is the greeter's worker: it opens the greeter user's
// session, runs the compositor as that user, and closes the session when
// the compositor exits or the daemon stops the worker (SIGTERM). The
// worker keeps one OS thread for PAM.
func RunGreeter(o WorkerOptions, greeterUser string, compositor, extraEnv []string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	a, err := lookupAccount(greeterUser)
	if err != nil {
		return err
	}
	login, err := auth.Greeter(o.Service, greeterUser)
	if err != nil {
		return err
	}
	defer func() {
		if err := login.Close(); err != nil {
			log.Printf("dm: greeter: %v", err)
		}
	}()
	pamEnv, err := login.OpenSession(o.sessionEnv("greeter"))
	if err != nil {
		return fmt.Errorf("greeter session: %w", err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	cmd, err := o.startAs(a, compositor, a.environ(pamEnv, extraEnv))
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return fmt.Errorf("the greeter's compositor exited: %w", err)
	case <-stop:
		return stopGroup(cmd, exited)
	}
}

// stopGroup ends cmd's process group: SIGTERM, then SIGKILL after the
// grace.
func stopGroup(cmd *exec.Cmd, exited <-chan error) error {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case err := <-exited:
		log.Printf("dm: the greeter's compositor stopped: %v", err)
		return nil
	case <-time.After(stopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err := <-exited
		return fmt.Errorf("the greeter did not stop in time and was killed: %w", err)
	}
}

// refusalText is what the greeter shows for a failed login: PAM's own
// reason when PAM refused it, the whole error otherwise.
func refusalText(err error) string {
	if refused, ok := errors.AsType[*auth.RefusedError](err); ok {
		return refused.Reason
	}
	return err.Error()
}

// RunSession is one login's worker, serving the daemon over rw: it
// authenticates the user the daemon names, relaying PAM's prompts, and
// once told to start, opens the session, runs the command as the user
// until it exits, and closes the session.
func RunSession(o WorkerOptions, rw io.ReadWriter) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ch := newChannel(rw)
	var req request
	if err := ch.recv(&req); err != nil {
		return err
	}
	if req.Op != opAuth || req.User == "" {
		return fmt.Errorf("dm: a session worker's first request is %q, want auth", req.Op)
	}
	ask := func(p auth.Prompt) (string, bool) {
		if err := ch.send(event{Kind: evPrompt, Prompt: &p}); err != nil {
			return "", false
		}
		var ans request
		if err := ch.recv(&ans); err != nil || ans.Op != opAnswer || ans.Answer == nil {
			return "", false
		}
		return *ans.Answer, true
	}
	login, err := auth.Authenticate(o.Service, req.User, ask)
	if err != nil {
		log.Printf("dm: login of %s: %v", req.User, err)
		return ch.send(event{Kind: evFailed, Text: refusalText(err)})
	}
	defer func() {
		if err := login.Close(); err != nil {
			log.Printf("dm: session of %s: %v", req.User, err)
		}
	}()
	if err := ch.send(event{Kind: evAuthenticated}); err != nil {
		return err
	}
	if err := ch.recv(&req); err != nil {
		return err
	}
	if req.Op != opStart || len(req.Cmd) == 0 {
		return nil
	}
	a, err := lookupAccount(login.User())
	if err != nil {
		return ch.send(event{Kind: evFailed, Text: err.Error()})
	}
	pamEnv, err := login.OpenSession(o.sessionEnv("user"))
	if err != nil {
		return ch.send(event{Kind: evFailed, Text: err.Error()})
	}
	argv := append([]string{"/bin/sh", "-c", profileScript, "sh"}, req.Cmd...)
	cmd, err := o.startAs(a, argv, a.environ(pamEnv, req.Env))
	if err != nil {
		return ch.send(event{Kind: evFailed, Text: err.Error()})
	}
	if err := ch.send(event{Kind: evStarted}); err != nil {
		log.Printf("dm: tell the daemon the session started: %v", err)
	}
	waitErr := cmd.Wait()
	text := "exited"
	if waitErr != nil {
		text = waitErr.Error()
	}
	log.Printf("dm: the session of %s ended: %s", a.name, text)
	return ch.send(event{Kind: evEnded, Text: text})
}
