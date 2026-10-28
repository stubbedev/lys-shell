// Package lockclient is the shell's side of locking: it owns the
// triggers (logind's Lock signal, `lys-shell lock`, lock-on-start, and a
// lock found held at startup) and answers each by starting `lys-login
// lock`, the login screen in its lock mode, as a process of its own. The
// shell links no lock screen code; the lock outlives a shell restart in a
// scope of its own, and a lock process that dies with the session still
// locked is started again.
package lockclient

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/spawn"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/service/logind"
)

// The `lys-login lock` contract: the line it prints on stdout once the
// compositor confirms the lock, and its exit codes beyond 0 (unlocked)
// and 1 (failed).
const (
	LockedLine = "locked"
	// ExitHeld: another lys-login already holds the session locked.
	ExitHeld = 3
	// ExitRefused: the lock never took (disabled, or the compositor
	// refused it).
	ExitRefused = 4
)

// The respawn budget: a lock process that keeps dying is started again
// at most maxRespawns times in respawnWindow, so a broken install cannot
// spin.
const (
	maxRespawns   = 3
	respawnWindow = time.Minute
)

// Client starts and follows the lock process.
type Client struct {
	// program is lys-login's name on the PATH, or a path.
	program string
	enabled func() bool
	// onLocked runs when the compositor confirms a lock this client
	// started (the vault locks with the session).
	onLocked func()
	// locked reports logind's LockedHint; nil without logind.
	locked func() (bool, error)
	// start starts a command and returns its stdout; tests swap it.
	start func(cmd *exec.Cmd) (io.Reader, error)
	now   func() time.Time

	mu       sync.Mutex
	running  bool
	respawns []time.Time
}

// New is a client for the lys-login on the PATH, which lys-login's own
// package installs.
func New(enabled func() bool, onLocked func(), locked func() (bool, error)) *Client {
	return &Client{
		program: "lys-login", enabled: enabled, onLocked: onLocked, locked: locked,
		start: startScoped, now: time.Now,
	}
}

// startScoped starts cmd in a scope of its own, so restarting the shell's
// unit does not end the lock; its log goes to the shell's.
func startScoped(cmd *exec.Cmd) (io.Reader, error) {
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := spawn.Scoped(cmd, "lys-login"); err != nil {
		return nil, err
	}
	return out, nil
}

// Lock starts a lock process unless one this client started runs, or
// locking is disabled in the config.
func (c *Client) Lock() {
	if !c.enabled() {
		log.Printf("lock: disabled in config; ignoring lock request")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return
	}
	c.spawnLocked()
}

func (c *Client) spawnLocked() {
	cmd := exec.Command(c.program, "lock") //nolint:gosec // lys-login, resolved beside the shell
	out, err := c.start(cmd)
	if err != nil {
		log.Printf("lock: cannot start %s lock: %v", c.program, err)
		return
	}
	c.running = true
	go c.follow(cmd, out)
}

// follow reads the lock process's stdout until it exits, then decides on
// a respawn.
func (c *Client) follow(cmd *exec.Cmd, out io.Reader) {
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if sc.Text() == LockedLine && c.onLocked != nil {
			c.onLocked()
		}
	}
	err := cmd.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	var exit *exec.ExitError
	switch {
	case err == nil:
		return
	case errors.As(err, &exit) && exit.ExitCode() == ExitHeld:
		log.Printf("lock: another lys-login already holds the session locked")
		return
	case errors.As(err, &exit) && exit.ExitCode() == ExitRefused:
		log.Printf("lock: the session was not locked (see lys-login's log)")
		return
	}
	log.Printf("lock: lys-login lock ended: %v", err)
	if !c.stillLocked() {
		return
	}
	if !c.mayRespawn() {
		log.Printf("lock: lys-login lock died %d times within %s; not starting it again", maxRespawns, respawnWindow)
		return
	}
	log.Printf("lock: the session is still locked; re-acquiring")
	c.spawnLocked()
}

// stillLocked reports logind's LockedHint; without logind (or a failed
// read) a dead lock client is assumed to have left the session locked,
// failing secure.
func (c *Client) stillLocked() bool {
	if c.locked == nil {
		return true
	}
	locked, err := c.locked()
	return err != nil || locked
}

// mayRespawn spends one of the respawn budget.
func (c *Client) mayRespawn() bool {
	now := c.now()
	kept := c.respawns[:0]
	for _, t := range c.respawns {
		if now.Sub(t) < respawnWindow {
			kept = append(kept, t)
		}
	}
	c.respawns = kept
	if len(c.respawns) >= maxRespawns {
		return false
	}
	c.respawns = append(c.respawns, now)
	return true
}

// Start runs the triggers for a shell: logind's Lock signal, a lock found
// held at startup (a lock client that died with the session locked), and
// lock-on-start once per login session. It returns the client, for
// `lys-shell lock`, and the stop that ends the logind listener.
func Start(current func() *config.Config, onLocked func()) (*Client, func()) {
	ld := logind.SystemClient("lock")
	var hint func() (bool, error)
	if ld != nil {
		hint = ld.LockedHint
	}
	c := New(func() bool { return current().Login.Lock.Enabled }, onLocked, hint)
	stop := func() {}
	if ld != nil {
		ctx, cancel := context.WithCancel(context.Background())
		stop = cancel
		go func() {
			if err := ld.Listen(ctx, c.Lock, func() {}); err != nil {
				log.Printf("lock: logind listener unavailable; its triggers are off: %v", err)
			}
		}()
		go func() {
			if shouldRelock(ld.LockedHint()) {
				log.Printf("lock: session was locked at startup (logind LockedHint); re-acquiring")
				c.Lock()
			}
		}()
	}
	if current().Login.Lock.LockOnStart && claimLockOnStart(os.Getenv("XDG_RUNTIME_DIR")) {
		c.Lock()
	}
	return c, stop
}

// shouldRelock decides from the LockedHint probe whether a fresh shell
// re-acquires the lock: only a confirmed true relocks. False means the
// session was left unlocked, and a failed probe fails soft - an unknown
// state must not lock the user out.
func shouldRelock(locked bool, err error) bool {
	return err == nil && locked
}

// claimLockOnStart claims the once-per-login-session lock-on-start
// gate: true the first time in a session, false on every restart.
// The marker lives under XDG_RUNTIME_DIR, which logind clears when the
// login session ends. Without a runtime dir there is no deduplication,
// so it locks: failing secure for an access gate.
func claimLockOnStart(runtimeDir string) bool {
	if runtimeDir == "" {
		return true
	}
	marker := filepath.Join(runtimeDir, version.Name, "lock-on-start.done")
	_ = os.MkdirAll(filepath.Dir(marker), 0o700) //nolint:gosec // our own runtime dir
	// O_EXCL is the atomic first-writer-wins test: exactly one shell
	// per session sees success.
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // our own runtime marker
	if err == nil {
		_ = f.Close()
		return true
	}
	// An existing marker is a restart; any other failure locks.
	return !errors.Is(err, os.ErrExist)
}
